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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CalypsoSys/babalu_vault/internal/config"
	"github.com/CalypsoSys/babalu_vault/internal/retention"
)

type Runner struct {
	Config *config.Config
	Logger *slog.Logger
}

type SummaryRow struct {
	Server      string
	Database    string
	Method      string
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
	regexp.MustCompile(`(?i)(password=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(password: )([^\s'"]+)`),
	regexp.MustCompile(`(?i)(passphrase=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(token=)([^\s'"]+)`),
	regexp.MustCompile(`(?i)(secret=)([^\s'"]+)`),
}

func (r *Runner) Run(ctx context.Context, serverFilter, databaseFilter string, dryRun bool) ([]SummaryRow, error) {
	selected := r.Config.Filter(serverFilter, databaseFilter)
	if len(selected) == 0 {
		return nil, fmt.Errorf("no databases matched server=%q database=%q", serverFilter, databaseFilter)
	}

	if err := os.MkdirAll(r.Config.Backup.TempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	if err := os.MkdirAll(r.Config.Backup.RootDir, 0o755); err != nil {
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

func (r *Runner) runOne(ctx context.Context, item config.SelectedDatabase, dryRun bool) SummaryRow {
	start := time.Now().UTC()
	method := item.Server.Type
	row := SummaryRow{
		Server:   item.Server.Name,
		Database: item.Database.Name,
		Method:   method,
		Status:   "ok",
	}

	logger := r.Logger.With(
		slog.String("server", item.Server.Name),
		slog.String("database", item.Database.Name),
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

	filename := BuildFilename(retention.TierDaily, item.Server.Name, item.Database.Name, start)
	localPath := filepath.Join(r.Config.Backup.TempDir, filename)
	row.LocalFile = localPath
	row.StoredPaths = []string{filepath.Join(r.Config.Backup.RootDir, item.Server.Name, item.Database.Name, filename)}
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

func (r *Runner) createBackupFile(ctx context.Context, item config.SelectedDatabase, localPath, preview string, logOperation func(string, string, ...slog.Attr)) error {
	outputFile, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp backup file: %w", err)
	}
	defer outputFile.Close()
	logOperation("info", "created temp output file", slog.String("local_file", localPath))

	gzipWriter, err := gzip.NewWriterLevel(outputFile, r.Config.Backup.GzipLevel)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	logOperation("info", "gzip writer initialized", slog.Int("gzip_level", r.Config.Backup.GzipLevel))

	cmd, stdout, stderrBuf, err := buildDumpCommand(ctx, item)
	if err != nil {
		return err
	}
	logOperation("info", "starting backup command", slog.String("command", preview))

	if err := cmd.Start(); err != nil {
		return wrapCommandStartError(item.Server.Type, err)
	}
	logOperation("info", "backup command started")

	_, copyErr := io.Copy(gzipWriter, stdout)
	if closeErr := gzipWriter.Close(); copyErr == nil && closeErr != nil {
		copyErr = fmt.Errorf("close gzip writer: %w", closeErr)
	}
	waitErr := cmd.Wait()

	if copyErr != nil {
		return fmt.Errorf("stream pg_dump output: %w", copyErr)
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("pg_dump failed: %s", msg)
	}
	logOperation("info", "backup command finished successfully")
	return nil
}

func buildDumpCommand(ctx context.Context, item config.SelectedDatabase) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	password := os.Getenv(item.Server.PasswordEnv)
	var stderr bytes.Buffer
	switch item.Server.Type {
	case "tcp":
		if _, err := exec.LookPath("pg_dump"); err != nil {
			return nil, nil, nil, fmt.Errorf("pg_dump not found in PATH: %w", err)
		}
		args := []string{
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--host", item.Server.Host,
			"--port", fmt.Sprintf("%d", item.Server.Port),
			"--username", item.Server.Username,
			item.Database.Name,
		}
		cmd := exec.CommandContext(ctx, "pg_dump", args...)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe pg_dump stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	case "docker":
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, nil, nil, fmt.Errorf("docker not found in PATH: %w", err)
		}
		args := []string{
			"exec",
			"-e", "PGPASSWORD=" + password,
			item.Server.Container,
			"pg_dump",
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--username", item.Server.Username,
			item.Database.Name,
		}
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pipe docker stdout: %w", err)
		}
		return cmd, stdout, &stderr, nil
	case "ssh":
		if _, err := exec.LookPath("ssh"); err != nil {
			return nil, nil, nil, fmt.Errorf("ssh not found in PATH: %w", err)
		}
		remoteCommand, err := buildSSHRemoteCommand(item, password)
		if err != nil {
			return nil, nil, nil, err
		}

		args := buildSSHArgs(item, remoteCommand)

		cmd := exec.CommandContext(ctx, "ssh", args...)
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

func buildSSHRemoteCommand(item config.SelectedDatabase, password string) (string, error) {
	switch item.Server.SSHRemoteType {
	case "", "tcp":
		args := []string{
			"PGPASSWORD=" + shellQuote(password),
			"pg_dump",
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--host", shellQuote(item.Server.Host),
			"--port", shellQuote(fmt.Sprintf("%d", item.Server.Port)),
			"--username", shellQuote(item.Server.Username),
			shellQuote(item.Database.Name),
		}
		return strings.Join(args, " "), nil
	case "docker":
		args := []string{
			"docker",
			"exec",
			"-e", shellQuote("PGPASSWORD=" + password),
			shellQuote(item.Server.Container),
			"pg_dump",
			"--format=plain",
			"--no-owner",
			"--no-acl",
			"--no-password",
			"--username", shellQuote(item.Server.Username),
			shellQuote(item.Database.Name),
		}
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported ssh_remote_type %q", item.Server.SSHRemoteType)
	}
}

func buildSSHArgs(item config.SelectedDatabase, remoteCommand string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
	}
	if item.Server.SSHPort > 0 {
		args = append(args, "-p", fmt.Sprintf("%d", item.Server.SSHPort))
	}
	args = append(args, item.Server.SSHTarget, remoteCommand)
	return args
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

func CommandPreview(item config.SelectedDatabase) (string, error) {
	switch item.Server.Type {
	case "tcp":
		return fmt.Sprintf(
			"PGPASSWORD=%s pg_dump --format=plain --no-owner --no-acl --no-password --host %s --port %d --username %s %s",
			envPlaceholder(item.Server.PasswordEnv),
			item.Server.Host,
			item.Server.Port,
			item.Server.Username,
			item.Database.Name,
		), nil
	case "docker":
		return fmt.Sprintf(
			"docker exec -e PGPASSWORD=%s %s pg_dump --format=plain --no-owner --no-acl --no-password --username %s %s",
			envPlaceholder(item.Server.PasswordEnv),
			item.Server.Container,
			item.Server.Username,
			item.Database.Name,
		), nil
	case "ssh":
		remoteCommand, err := buildSSHRemoteCommand(item, envPlaceholder(item.Server.PasswordEnv))
		if err != nil {
			return "", err
		}
		args := append([]string{"ssh"}, buildSSHArgs(item, shellQuote(remoteCommand))...)
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported server type %q", item.Server.Type)
	}
}

func envPlaceholder(name string) string {
	if name == "" {
		return "<password>"
	}
	return "${" + name + "}"
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

func (r *Runner) storeAndRetain(item config.SelectedDatabase, localPath, filename string, now time.Time, logOperation func(string, string, ...slog.Attr)) error {
	dailyPath := filepath.Join(r.Config.Backup.RootDir, item.Server.Name, item.Database.Name, filename)
	logOperation("info", "storing daily backup", slog.String("source", localPath), slog.String("destination", dailyPath))
	if err := copyFile(localPath, dailyPath); err != nil {
		return fmt.Errorf("store daily backup: %w", err)
	}
	logOperation("info", "daily backup stored", slog.String("path", dailyPath))

	planner := &retention.Planner{
		RootDir: r.Config.Backup.RootDir,
		DryRun:  false,
	}
	logOperation("info", "applying retention policy", slog.Int("daily_keep", item.Retention.DailyKeep), slog.Int("weekly_keep", item.Retention.WeeklyKeep), slog.Int("monthly_keep", item.Retention.MonthlyKeep))
	if err := planner.ApplyAt(item.Server.Name, item.Database.Name, item.Retention, now); err != nil {
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

func (r *Runner) logRetentionPlan(item config.SelectedDatabase, now time.Time, logOperation func(string, string, ...slog.Attr)) error {
	logOperation("info", "dry-run would apply retention policy", slog.Int("daily_keep", item.Retention.DailyKeep), slog.Int("weekly_keep", item.Retention.WeeklyKeep), slog.Int("monthly_keep", item.Retention.MonthlyKeep))
	planner := &retention.Planner{
		RootDir: r.Config.Backup.RootDir,
		DryRun:  true,
	}
	if err := planner.ApplyAt(item.Server.Name, item.Database.Name, item.Retention, now); err != nil {
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

func wrapCommandStartError(serverType string, err error) error {
	switch serverType {
	case "tcp":
		return fmt.Errorf("start pg_dump: %w", err)
	case "docker":
		return fmt.Errorf("start docker exec pg_dump: %w", err)
	case "ssh":
		return fmt.Errorf("start ssh pg_dump: %w", err)
	default:
		return err
	}
}

func ToolingHint() string {
	if runtime.GOOS == "windows" {
		return "ensure pg_dump or ssh is installed and available in PATH, or use docker mode"
	}
	return "ensure pg_dump, docker, or ssh is installed and available in PATH"
}
