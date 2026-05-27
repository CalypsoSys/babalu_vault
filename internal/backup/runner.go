package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CalypsoSys/babalu_vault/internal/config"
	"github.com/CalypsoSys/babalu_vault/internal/retention"
)

type Runner struct {
	Config   *config.Config
	Logger   *slog.Logger
	Progress func(SummaryRow)
}

type SummaryRow struct {
	Server      string
	Backup      string
	Database    string
	Method      string
	Retention   config.RetentionPolicy
	Status      string
	LocalFile   string
	StoredPaths []string
	SizeBytes   int64
	Duration    time.Duration
	Error       string
	Operations  []OperationEntry
}

type OperationEntry struct {
	Level   string
	Message string
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(PGPASSWORD=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(MYSQL_PWD=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(password=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(password: )([^\s'"]+)`),
	regexp.MustCompile(`(?i)(passphrase=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(token=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(secret=)([^\s'"]+)`),
}

var defaultIgnoredPostgresDatabases = []string{"postgres", "template0", "template1"}
var listDatabasesFn = listBackupDatabases

func (r *Runner) Run(ctx context.Context, serverFilter, backupFilter, targetFilter string, dryRun bool) ([]SummaryRow, error) {
	selected, err := ResolveTargets(ctx, r.Config, serverFilter, backupFilter, targetFilter)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no targets matched server=%q backup=%q target=%q", serverFilter, backupFilter, targetFilter)
	}

	if err := os.MkdirAll(r.Config.Settings.TempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	if err := os.MkdirAll(r.Config.Settings.RootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup root dir: %w", err)
	}

	var rows []SummaryRow
	var anyFailure bool
	for _, item := range selected {
		row := r.runOne(ctx, item, dryRun)
		rows = append(rows, row)
		if row.Status != "ok" && row.Status != "dry-run" {
			anyFailure = true
		}
	}
	if anyFailure {
		return rows, errors.New("one or more backups failed")
	}
	return rows, nil
}

func (r *Runner) runOne(ctx context.Context, item config.SelectedTarget, dryRun bool) SummaryRow {
	start := time.Now().UTC()
	method := methodLabel(item)
	targetName := item.Target.Name
	groupName := item.Backup.Name
	row := SummaryRow{
		Server:    item.Server.Name,
		Backup:    groupName,
		Database:  targetName,
		Method:    method,
		Retention: item.Retention,
		Status:    "ok",
	}

	logger := r.Logger.With(
		slog.String("server", item.Server.Name),
		slog.String("backup", item.Backup.Name),
		slog.String("target", item.Target.Name),
		slog.String("engine", item.Backup.Engine),
		slog.String("method", method),
	)
	var operations []OperationEntry
	logOperation := func(level, message string, attrs ...slog.Attr) {
		safeAttrs := sanitizeAttrs(attrs)
		operations = append(operations, OperationEntry{Level: level, Message: formatOperationMessage(message, safeAttrs...)})
		args := make([]any, 0, len(attrs))
		for _, attr := range safeAttrs {
			args = append(args, attr)
		}
		switch level {
		case "error":
			logger.Error(message, args...)
		case "warn":
			logger.Warn(message, args...)
		default:
			logger.Info(message, args...)
		}
	}
	logOperation("info", "backup started")

	filename := BuildFilename(retention.TierDaily, groupName, targetName, start, artifactExtension(item))
	localPath := filepath.Join(r.Config.Settings.TempDir, filename)
	row.LocalFile = localPath
	row.StoredPaths = []string{filepath.Join(r.Config.Settings.RootDir, groupName, targetName, filename)}
	r.emitProgress(SummaryRow{
		Server:      row.Server,
		Backup:      row.Backup,
		Database:    row.Database,
		Method:      row.Method,
		Retention:   row.Retention,
		Status:      "running",
		LocalFile:   row.LocalFile,
		StoredPaths: append([]string(nil), row.StoredPaths...),
	})
	logOperation("info", "temp backup path prepared", slog.String("local_file", localPath))

	preview, previewErr := CommandPreview(item)
	if previewErr != nil {
		row.Status = "error"
		row.Error = sanitizeSensitiveString(previewErr.Error())
		row.Duration = time.Since(start)
		row.Operations = operations
		logOperation("error", "backup failed", slog.Any("error", previewErr))
		return row
	}
	logOperation("info", "backup command prepared", slog.String("command", preview))

	if dryRun {
		logOperation("warn", "dry-run command preview", slog.String("command", preview))
		logOperation("info", "dry-run would create temp backup file", slog.String("local_file", localPath))
		logOperation("info", "dry-run would store daily backup", slog.String("path", row.StoredPaths[0]))
		if err := r.logRetentionPlan(item, start, logOperation); err != nil {
			row.Status = "error"
			row.Error = sanitizeSensitiveString(err.Error())
			row.Duration = time.Since(start)
			row.Operations = operations
			logOperation("error", "dry-run retention planning failed", slog.Any("error", err))
			return row
		}
		row.Status = "dry-run"
		row.Duration = time.Since(start)
		row.Operations = operations
		return row
	}

	if err := r.createBackupFile(ctx, item, localPath, preview, logOperation); err != nil {
		row.Status = "error"
		row.Error = sanitizeSensitiveString(err.Error())
		row.Duration = time.Since(start)
		row.Operations = operations
		logOperation("error", "backup failed", slog.Any("error", err))
		return row
	}
	defer os.Remove(localPath)

	info, err := os.Stat(localPath)
	if err != nil {
		row.Status = "error"
		row.Error = sanitizeSensitiveString(fmt.Sprintf("stat backup file: %v", err))
		row.Duration = time.Since(start)
		row.Operations = operations
		logOperation("error", "backup failed", slog.Any("error", err))
		return row
	}
	row.SizeBytes = info.Size()
	logOperation("info", "temp backup file created", slog.Int64("size_bytes", row.SizeBytes), slog.String("local_file", localPath))

	if err := r.storeAndRetain(item, localPath, filename, start, logOperation); err != nil {
		row.Status = "error"
		row.Error = sanitizeSensitiveString(err.Error())
		row.Duration = time.Since(start)
		row.Operations = operations
		logOperation("error", "store/retention failed", slog.Any("error", err))
		return row
	}

	row.Duration = time.Since(start)
	row.Operations = operations
	logOperation("info", "backup completed", slog.String("local_file", localPath), slog.Int64("size_bytes", row.SizeBytes), slog.Duration("duration", row.Duration))
	return row
}

func (r *Runner) emitProgress(row SummaryRow) {
	if r.Progress == nil {
		return
	}
	r.Progress(row)
}

func ResolveTargets(ctx context.Context, cfg *config.Config, serverFilter, backupFilter, targetFilter string) ([]config.SelectedTarget, error) {
	servers := make(map[string]config.ServerConfig, len(cfg.Servers))
	for _, server := range cfg.Servers {
		servers[server.Name] = server
	}

	var selected []config.SelectedTarget
	for _, backup := range cfg.Backups {
		server, ok := servers[backup.Server]
		if !ok {
			continue
		}
		if serverFilter != "" && server.Name != serverFilter {
			continue
		}
		if backupFilter != "" && backup.Name != backupFilter {
			continue
		}

		if backup.Engine == "postgres" && backup.Discovery.Databases {
			seed := config.SelectedTarget{Server: server, Backup: backup}
			names, err := listDatabasesFn(ctx, seed)
			if err != nil {
				return nil, fmt.Errorf("discover databases for backup %q: %w", backup.Name, err)
			}
			ignored := ignoredDatabaseSet(backup)
			for _, name := range names {
				if _, skip := ignored[name]; skip {
					continue
				}
				target := config.TargetConfig{Name: name, Database: name}
				if explicit, ok := backup.TargetConfigFor(name); ok {
					target = explicit
					if target.Database == "" {
						target.Database = name
					}
				}
				if targetFilter != "" && target.Name != targetFilter {
					continue
				}
				selected = append(selected, config.SelectedTarget{
					Server:    server,
					Backup:    backup,
					Target:    target,
					Retention: cfg.RetentionFor(target),
				})
			}
			continue
		}

		for _, target := range backup.Targets {
			target = normalizeTarget(backup, target)
			if targetFilter != "" && target.Name != targetFilter {
				continue
			}
			selected = append(selected, config.SelectedTarget{
				Server:    server,
				Backup:    backup,
				Target:    target,
				Retention: cfg.RetentionFor(target),
			})
		}
	}
	return selected, nil
}

func normalizeTarget(backup config.BackupConfig, target config.TargetConfig) config.TargetConfig {
	if (backup.Engine == "postgres" || backup.Engine == "mysql") && target.Database == "" {
		target.Database = target.Name
	}
	return target
}

func ignoredDatabaseSet(backup config.BackupConfig) map[string]struct{} {
	ignored := make(map[string]struct{}, len(defaultIgnoredPostgresDatabases)+len(backup.Discovery.Ignore))
	for _, name := range defaultIgnoredPostgresDatabases {
		ignored[name] = struct{}{}
	}
	for _, name := range backup.Discovery.Ignore {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		ignored[trimmed] = struct{}{}
	}
	return ignored
}

func listBackupDatabases(ctx context.Context, item config.SelectedTarget) ([]string, error) {
	cmd, stdout, stderrBuf, err := buildListDatabasesCommand(ctx, item)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, wrapCommandStartError(item, err)
	}
	out, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("read database list output: %w", readErr)
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return nil, fmt.Errorf("database list failed: %s", msg)
	}

	seen := make(map[string]struct{})
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (r *Runner) createBackupFile(ctx context.Context, item config.SelectedTarget, localPath, preview string, logOperation func(string, string, ...slog.Attr)) error {
	outputFile, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp backup file: %w", err)
	}
	defer outputFile.Close()
	logOperation("info", "created temp output file", slog.String("local_file", localPath))

	gzipWriter, err := gzip.NewWriterLevel(outputFile, r.Config.Settings.GzipLevel)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	logOperation("info", "gzip writer initialized", slog.Int("gzip_level", r.Config.Settings.GzipLevel))

	cmd, stdout, stderrBuf, err := buildArtifactCommand(ctx, item)
	if err != nil {
		return err
	}
	logOperation("info", "starting backup command", slog.String("command", preview))

	if err := cmd.Start(); err != nil {
		return wrapCommandStartError(item, err)
	}
	logOperation("info", "backup command started")

	_, copyErr := io.Copy(gzipWriter, stdout)
	if closeErr := gzipWriter.Close(); copyErr == nil && closeErr != nil {
		copyErr = fmt.Errorf("close gzip writer: %w", closeErr)
	}
	waitErr := cmd.Wait()

	if copyErr != nil {
		return fmt.Errorf("stream backup output: %w", copyErr)
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("%s backup command failed: %s", item.Backup.Engine, msg)
	}
	logOperation("info", "backup command finished successfully")
	return nil
}

func buildArtifactCommand(ctx context.Context, item config.SelectedTarget) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	switch item.Backup.Engine {
	case "postgres":
		return buildPostgresDumpCommand(ctx, item)
	case "mysql":
		return buildMySQLDumpCommand(ctx, item)
	case "files":
		return buildFileArchiveCommand(ctx, item)
	default:
		return nil, nil, nil, fmt.Errorf("unsupported backup engine %q", item.Backup.Engine)
	}
}

func buildPostgresDumpCommand(ctx context.Context, item config.SelectedTarget) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	source := item.Backup.Source
	if source == nil {
		return nil, nil, nil, fmt.Errorf("backup %q source is required", item.Backup.Name)
	}
	password, _, err := config.ResolveConfiguredSecret(source.Password)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve password: %w", err)
	}
	var stderr bytes.Buffer
	switch item.Server.Type {
	case "tcp", "docker":
		cmd, err := buildLocalPostgresDumpCommand(ctx, item, password, &stderr)
		if err != nil {
			return nil, nil, nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe postgres stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	case "ssh":
		if _, err := exec.LookPath("ssh"); err != nil {
			return nil, nil, nil, fmt.Errorf("ssh not found in PATH: %w", err)
		}
		remoteCommand, err := buildPostgresRemoteCommand(item, password)
		if err != nil {
			return nil, nil, nil, err
		}
		cmd := exec.CommandContext(ctx, "ssh", buildServerSSHArgs(item.Server, remoteCommand)...)
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe ssh stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported server type %q", item.Server.Type)
	}
}

func buildLocalPostgresDumpCommand(ctx context.Context, item config.SelectedTarget, password string, stderr *bytes.Buffer) (*exec.Cmd, error) {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		if _, err := exec.LookPath("pg_dump"); err != nil {
			return nil, fmt.Errorf("pg_dump not found in PATH: %w", err)
		}
		args := []string{
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--host", effectiveHost(item),
			"--port", fmt.Sprintf("%d", source.Port),
			"--username", source.Username,
			item.Target.DatabaseName(),
		}
		cmd := exec.CommandContext(ctx, "pg_dump", args...)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		cmd.Stderr = stderr
		return cmd, nil
	case "docker":
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, fmt.Errorf("docker not found in PATH: %w", err)
		}
		args := []string{
			"exec",
			"-e", "PGPASSWORD=" + password,
			source.Container,
			"pg_dump",
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--username", source.Username,
			item.Target.DatabaseName(),
		}
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stderr = stderr
		return cmd, nil
	default:
		return nil, fmt.Errorf("unsupported postgres source mode %q", source.Mode)
	}
}

func buildMySQLDumpCommand(ctx context.Context, item config.SelectedTarget) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	source := item.Backup.Source
	if source == nil {
		return nil, nil, nil, fmt.Errorf("backup %q source is required", item.Backup.Name)
	}
	password, _, err := config.ResolveConfiguredSecret(source.Password)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve password: %w", err)
	}
	var stderr bytes.Buffer
	switch item.Server.Type {
	case "tcp", "docker":
		cmd, err := buildLocalMySQLDumpCommand(ctx, item, password, &stderr)
		if err != nil {
			return nil, nil, nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe mysql stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	case "ssh":
		if _, err := exec.LookPath("ssh"); err != nil {
			return nil, nil, nil, fmt.Errorf("ssh not found in PATH: %w", err)
		}
		remoteCommand, err := buildMySQLRemoteCommand(item, password)
		if err != nil {
			return nil, nil, nil, err
		}
		cmd := exec.CommandContext(ctx, "ssh", buildServerSSHArgs(item.Server, remoteCommand)...)
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe ssh stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported server type %q", item.Server.Type)
	}
}

func buildLocalMySQLDumpCommand(ctx context.Context, item config.SelectedTarget, password string, stderr *bytes.Buffer) (*exec.Cmd, error) {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		if _, err := exec.LookPath("mysqldump"); err != nil {
			return nil, fmt.Errorf("mysqldump not found in PATH: %w", err)
		}
		args := append(mysqlDumpOptions(),
			"--host", effectiveHost(item),
			"--port", fmt.Sprintf("%d", source.Port),
			"--user", source.Username,
			item.Target.DatabaseName(),
		)
		cmd := exec.CommandContext(ctx, "mysqldump", args...)
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+password)
		cmd.Stderr = stderr
		return cmd, nil
	case "docker":
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, fmt.Errorf("docker not found in PATH: %w", err)
		}
		if source.PasswordEnv != "" {
			cmd := exec.CommandContext(ctx, "docker", "exec", source.Container, "sh", "-c", buildMySQLDockerEnvScript(item))
			cmd.Stderr = stderr
			return cmd, nil
		}
		args := []string{
			"exec",
			"-e", "MYSQL_PWD=" + password,
			source.Container,
			"mysqldump",
		}
		args = append(args, mysqlDumpOptions()...)
		args = append(args, "-u"+source.Username, item.Target.DatabaseName())
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stderr = stderr
		return cmd, nil
	default:
		return nil, fmt.Errorf("unsupported mysql source mode %q", source.Mode)
	}
}

func buildFileArchiveCommand(ctx context.Context, item config.SelectedTarget) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	if item.Server.Type != "ssh" {
		return nil, nil, nil, fmt.Errorf("files backup %q requires ssh server", item.Backup.Name)
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		return nil, nil, nil, fmt.Errorf("ssh not found in PATH: %w", err)
	}
	var stderr bytes.Buffer
	remoteCommand := buildTarRemoteCommand(item.Target)
	cmd := exec.CommandContext(ctx, "ssh", buildServerSSHArgs(item.Server, remoteCommand)...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pipe ssh stdout: %w", err)
	}
	return cmd, stdout, &stderr, nil
}

func buildListDatabasesCommand(ctx context.Context, item config.SelectedTarget) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	if item.Backup.Engine != "postgres" {
		return nil, nil, nil, fmt.Errorf("database discovery is only supported for postgres")
	}
	source := item.Backup.Source
	if source == nil {
		return nil, nil, nil, fmt.Errorf("backup %q source is required", item.Backup.Name)
	}
	password, _, err := config.ResolveConfiguredSecret(source.Password)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve password: %w", err)
	}
	var stderr bytes.Buffer
	sql := "SELECT datname FROM pg_database ORDER BY datname"
	switch item.Server.Type {
	case "tcp", "docker":
		cmd, err := buildLocalPostgresListCommand(ctx, item, password, sql, &stderr)
		if err != nil {
			return nil, nil, nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe postgres list stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	case "ssh":
		if _, err := exec.LookPath("ssh"); err != nil {
			return nil, nil, nil, fmt.Errorf("ssh not found in PATH: %w", err)
		}
		remoteCommand, err := buildPostgresRemoteListCommand(item, password, sql)
		if err != nil {
			return nil, nil, nil, err
		}
		cmd := exec.CommandContext(ctx, "ssh", buildServerSSHArgs(item.Server, remoteCommand)...)
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe ssh stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported server type %q", item.Server.Type)
	}
}

func buildLocalPostgresListCommand(ctx context.Context, item config.SelectedTarget, password, sql string, stderr *bytes.Buffer) (*exec.Cmd, error) {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		if _, err := exec.LookPath("psql"); err != nil {
			return nil, fmt.Errorf("psql not found in PATH: %w", err)
		}
		args := postgresListArgs(source.Username, "postgres", sql)
		args = append([]string{"--host", effectiveHost(item), "--port", fmt.Sprintf("%d", source.Port)}, args...)
		cmd := exec.CommandContext(ctx, "psql", args...)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		cmd.Stderr = stderr
		return cmd, nil
	case "docker":
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, fmt.Errorf("docker not found in PATH: %w", err)
		}
		args := []string{"exec", "-e", "PGPASSWORD=" + password, source.Container, "psql"}
		args = append(args, postgresListArgs(source.Username, "postgres", sql)...)
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stderr = stderr
		return cmd, nil
	default:
		return nil, fmt.Errorf("unsupported postgres source mode %q", source.Mode)
	}
}

func buildPostgresRemoteCommand(item config.SelectedTarget, password string) (string, error) {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		args := []string{
			"PGPASSWORD=" + shellQuote(password),
			"pg_dump",
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--host", shellQuote(effectiveHost(item)),
			"--port", shellQuote(fmt.Sprintf("%d", source.Port)),
			"--username", shellQuote(source.Username),
			shellQuote(item.Target.DatabaseName()),
		}
		return strings.Join(args, " "), nil
	case "docker":
		args := []string{
			"docker",
			"exec",
			"-e", shellQuote("PGPASSWORD=" + password),
			shellQuote(source.Container),
			"pg_dump",
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--username", shellQuote(source.Username),
			shellQuote(item.Target.DatabaseName()),
		}
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported postgres source mode %q", source.Mode)
	}
}

func buildPostgresRemoteListCommand(item config.SelectedTarget, password, sql string) (string, error) {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		args := []string{
			"PGPASSWORD=" + shellQuote(password),
			"psql",
			"-X",
			"-A",
			"-t",
			"-q",
			"-P", shellQuote("pager=off"),
			"--no-password",
			"--host", shellQuote(effectiveHost(item)),
			"--port", shellQuote(fmt.Sprintf("%d", source.Port)),
			"--username", shellQuote(source.Username),
			"--dbname", shellQuote("postgres"),
			"-c", shellQuote(sql),
		}
		return strings.Join(args, " "), nil
	case "docker":
		args := []string{
			"docker",
			"exec",
			"-e", shellQuote("PGPASSWORD=" + password),
			shellQuote(source.Container),
			"psql",
			"-X",
			"-A",
			"-t",
			"-q",
			"-P", shellQuote("pager=off"),
			"--no-password",
			"--username", shellQuote(source.Username),
			"--dbname", shellQuote("postgres"),
			"-c", shellQuote(sql),
		}
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported postgres source mode %q", source.Mode)
	}
}

func buildMySQLRemoteCommand(item config.SelectedTarget, password string) (string, error) {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		args := []string{
			"MYSQL_PWD=" + shellQuote(password),
			"mysqldump",
		}
		args = append(args, shellQuoteArgs(mysqlDumpOptions())...)
		args = append(args,
			"--host", shellQuote(effectiveHost(item)),
			"--port", shellQuote(fmt.Sprintf("%d", source.Port)),
			"--user", shellQuote(source.Username),
			shellQuote(item.Target.DatabaseName()),
		)
		return strings.Join(args, " "), nil
	case "docker":
		if source.PasswordEnv != "" {
			args := []string{
				"docker",
				"exec",
				shellQuote(source.Container),
				"sh",
				"-c",
				shellQuote(buildMySQLDockerEnvScript(item)),
			}
			return strings.Join(args, " "), nil
		}
		args := []string{
			"docker",
			"exec",
			"-e", shellQuote("MYSQL_PWD=" + password),
			shellQuote(source.Container),
			"mysqldump",
		}
		args = append(args, shellQuoteArgs(mysqlDumpOptions())...)
		args = append(args, shellQuote("-u"+source.Username), shellQuote(item.Target.DatabaseName()))
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported mysql source mode %q", source.Mode)
	}
}

func buildMySQLDockerEnvScript(item config.SelectedTarget) string {
	source := *item.Backup.Source
	args := []string{
		"mysqldump",
	}
	args = append(args, shellQuoteArgs(mysqlDumpOptions())...)
	args = append(args, shellQuote("-u"+source.Username), shellQuote(item.Target.DatabaseName()))
	return fmt.Sprintf("MYSQL_PWD=\"$%s\" exec %s", source.PasswordEnv, strings.Join(args, " "))
}

func buildTarRemoteCommand(target config.TargetConfig) string {
	args := []string{
		"tar",
		"--numeric-owner",
		"--one-file-system",
		"-C", shellQuote(target.Path),
	}
	for _, pattern := range target.Excludes {
		args = append(args, "--exclude", shellQuote(pattern))
	}
	args = append(args, "-cf", "-", ".")
	return strings.Join(args, " ")
}

func buildServerSSHArgs(server config.ServerConfig, remoteCommand string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
	}
	if server.SSHPort > 0 {
		args = append(args, "-p", fmt.Sprintf("%d", server.SSHPort))
	}
	args = append(args, server.SSHTarget, remoteCommand)
	return args
}

func effectiveHost(item config.SelectedTarget) string {
	if item.Backup.Source != nil && item.Backup.Source.Host != "" {
		return item.Backup.Source.Host
	}
	return item.Server.Host
}

func postgresListArgs(username, dbName, sql string) []string {
	return []string{
		"-X",
		"-A",
		"-t",
		"-q",
		"-P", "pager=off",
		"--no-password",
		"--username", username,
		"--dbname", dbName,
		"-c", sql,
	}
}

func mysqlDumpOptions() []string {
	return []string{
		"--single-transaction",
		"--quick",
		"--routines",
		"--triggers",
		"--events",
		"--hex-blob",
		"--default-character-set=utf8mb4",
	}
}

func shellQuoteArgs(values []string) []string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellQuote(value))
	}
	return quoted
}

func methodLabel(item config.SelectedTarget) string {
	switch item.Backup.Engine {
	case "files":
		return "files/" + item.Server.Type
	default:
		if item.Backup.Source == nil {
			return item.Backup.Engine
		}
		if item.Server.Type == "ssh" {
			return item.Backup.Engine + "/ssh-" + item.Backup.Source.Mode
		}
		return item.Backup.Engine + "/" + item.Backup.Source.Mode
	}
}

func artifactExtension(item config.SelectedTarget) string {
	switch item.Backup.Engine {
	case "files":
		return ".tar.gz"
	default:
		return ".sql.gz"
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\r'\"\\$`!&|;<>()[]{}*?~#") && utf8.ValidString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func CommandPreview(item config.SelectedTarget) (string, error) {
	switch item.Backup.Engine {
	case "postgres":
		return databaseCommandPreview(item, "PGPASSWORD")
	case "mysql":
		return databaseCommandPreview(item, "MYSQL_PWD")
	case "files":
		if item.Server.Type != "ssh" {
			return "", fmt.Errorf("files backup %q requires ssh server", item.Backup.Name)
		}
		args := append([]string{"ssh"}, buildServerSSHArgs(item.Server, buildTarRemoteCommand(item.Target))...)
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported backup engine %q", item.Backup.Engine)
	}
}

func databaseCommandPreview(item config.SelectedTarget, passwordEnvName string) (string, error) {
	source := item.Backup.Source
	if source == nil {
		return "", fmt.Errorf("backup %q source is required", item.Backup.Name)
	}
	_, placeholder, err := config.ResolveConfiguredSecret(source.Password)
	if err != nil {
		return "", fmt.Errorf("resolve password placeholder: %w", err)
	}
	switch item.Backup.Engine {
	case "postgres":
		switch item.Server.Type {
		case "tcp", "docker":
			return localPostgresPreview(item, placeholder), nil
		case "ssh":
			remoteCommand, err := buildPostgresRemoteCommand(item, placeholder)
			if err != nil {
				return "", err
			}
			args := append([]string{"ssh"}, buildServerSSHArgs(item.Server, remoteCommand)...)
			return strings.Join(args, " "), nil
		}
	case "mysql":
		switch item.Server.Type {
		case "tcp", "docker":
			return localMySQLPreview(item, placeholder), nil
		case "ssh":
			remoteCommand, err := buildMySQLRemoteCommand(item, placeholder)
			if err != nil {
				return "", err
			}
			args := append([]string{"ssh"}, buildServerSSHArgs(item.Server, remoteCommand)...)
			return strings.Join(args, " "), nil
		}
	}
	return "", fmt.Errorf("unsupported %s server type %q", passwordEnvName, item.Server.Type)
}

func localPostgresPreview(item config.SelectedTarget, placeholder string) string {
	source := *item.Backup.Source
	switch source.Mode {
	case "tcp":
		return fmt.Sprintf(
			"PGPASSWORD=%s pg_dump --format=plain --no-owner --no-acl --no-password --host %s --port %d --username %s %s",
			placeholder,
			effectiveHost(item),
			source.Port,
			source.Username,
			item.Target.DatabaseName(),
		)
	case "docker":
		return fmt.Sprintf(
			"docker exec -e PGPASSWORD=%s %s pg_dump --format=plain --no-owner --no-acl --no-password --username %s %s",
			placeholder,
			source.Container,
			source.Username,
			item.Target.DatabaseName(),
		)
	default:
		return fmt.Sprintf("unsupported postgres source mode %q", source.Mode)
	}
}

func localMySQLPreview(item config.SelectedTarget, placeholder string) string {
	source := *item.Backup.Source
	options := strings.Join(mysqlDumpOptions(), " ")
	switch source.Mode {
	case "tcp":
		return fmt.Sprintf(
			"MYSQL_PWD=%s mysqldump %s --host %s --port %d --user %s %s",
			placeholder,
			options,
			effectiveHost(item),
			source.Port,
			source.Username,
			item.Target.DatabaseName(),
		)
	case "docker":
		if source.PasswordEnv != "" {
			return fmt.Sprintf(
				"docker exec %s sh -c %s",
				source.Container,
				shellQuote(buildMySQLDockerEnvScript(item)),
			)
		}
		return fmt.Sprintf(
			"docker exec -e MYSQL_PWD=%s %s mysqldump %s -u%s %s",
			placeholder,
			source.Container,
			options,
			source.Username,
			item.Target.DatabaseName(),
		)
	default:
		return fmt.Sprintf("unsupported mysql source mode %q", source.Mode)
	}
}

func formatOperationMessage(message string, attrs ...slog.Attr) string {
	if len(attrs) == 0 {
		return sanitizeSensitiveString(message)
	}
	parts := []string{sanitizeSensitiveString(message)}
	for _, attr := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%v", attr.Key, sanitizeAttrValue(attr.Value.Any())))
	}
	return strings.Join(parts, " ")
}

func sanitizeAttrs(attrs []slog.Attr) []slog.Attr {
	safe := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		safe = append(safe, sanitizeAttr(attr))
	}
	return safe
}

func sanitizeAttr(attr slog.Attr) slog.Attr {
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, sanitizeSensitiveString(attr.Value.String()))
	case slog.KindAny:
		return slog.Any(attr.Key, sanitizeAttrValue(attr.Value.Any()))
	default:
		return attr
	}
}

func sanitizeAttrValue(value any) any {
	switch v := value.(type) {
	case string:
		return sanitizeSensitiveString(v)
	case error:
		return errors.New(sanitizeSensitiveString(v.Error()))
	default:
		return value
	}
}

func sanitizeSensitiveString(value string) string {
	safe := value
	for _, pattern := range sensitivePatterns {
		safe = pattern.ReplaceAllString(safe, `${1}***`)
	}
	return safe
}

func (r *Runner) storeAndRetain(item config.SelectedTarget, localPath, filename string, now time.Time, logOperation func(string, string, ...slog.Attr)) error {
	groupName := item.Backup.Name
	targetName := item.Target.Name
	dailyPath := filepath.Join(r.Config.Settings.RootDir, groupName, targetName, filename)
	logOperation("info", "storing daily backup", slog.String("source", localPath), slog.String("destination", dailyPath))
	if err := copyFile(localPath, dailyPath); err != nil {
		return fmt.Errorf("store daily backup: %w", err)
	}
	logOperation("info", "daily backup stored", slog.String("path", dailyPath))

	planner := &retention.Planner{
		RootDir:   r.Config.Settings.RootDir,
		DryRun:    false,
		Extension: artifactExtension(item),
	}
	logOperation("info", "applying retention policy", slog.Int("daily_keep", item.Retention.DailyKeep), slog.Int("weekly_keep", item.Retention.WeeklyKeep), slog.Int("monthly_keep", item.Retention.MonthlyKeep))
	if err := planner.ApplyAt(groupName, targetName, item.Retention, now); err != nil {
		return err
	}
	for _, promotion := range planner.PromoteLog {
		logOperation("info", "retention promotion",
			slog.String("from_tier", string(promotion.From)),
			slog.String("to_tier", string(promotion.To)),
			slog.String("file", promotion.File.Name),
		)
	}
	for _, deletion := range planner.DeleteLog {
		logOperation("info", "retention deletion", slog.String("tier", string(deletion.Tier)), slog.String("file", deletion.File.Name))
	}
	return nil
}

func (r *Runner) logRetentionPlan(item config.SelectedTarget, now time.Time, logOperation func(string, string, ...slog.Attr)) error {
	logOperation("info", "dry-run would apply retention policy", slog.Int("daily_keep", item.Retention.DailyKeep), slog.Int("weekly_keep", item.Retention.WeeklyKeep), slog.Int("monthly_keep", item.Retention.MonthlyKeep))
	planner := &retention.Planner{
		RootDir:   r.Config.Settings.RootDir,
		DryRun:    true,
		Extension: artifactExtension(item),
	}
	if err := planner.ApplyAt(item.Backup.Name, item.Target.Name, item.Retention, now); err != nil {
		return err
	}
	for _, promotion := range planner.PromoteLog {
		logOperation("info", "dry-run would promote backup",
			slog.String("from_tier", string(promotion.From)),
			slog.String("to_tier", string(promotion.To)),
			slog.String("file", promotion.File.Name),
		)
	}
	for _, deletion := range planner.DeleteLog {
		logOperation("info", "dry-run would delete backup", slog.String("tier", string(deletion.Tier)), slog.String("file", deletion.File.Name))
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func wrapCommandStartError(item config.SelectedTarget, err error) error {
	switch item.Backup.Engine {
	case "postgres":
		return fmt.Errorf("start postgres backup command: %w", err)
	case "mysql":
		return fmt.Errorf("start mysql backup command: %w", err)
	case "files":
		return fmt.Errorf("start file archive command: %w", err)
	default:
		return err
	}
}

func ToolingHint() string {
	if runtime.GOOS == "windows" {
		return "ensure database clients or ssh are installed and available in PATH, or use docker mode"
	}
	return "ensure pg_dump, psql, mysqldump, docker, ssh, and tar are installed where required"
}
