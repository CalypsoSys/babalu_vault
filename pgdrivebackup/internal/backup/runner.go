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
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/joe/calypso_pgvault/pgdrivebackup/internal/config"
	"github.com/joe/calypso_pgvault/pgdrivebackup/internal/retention"
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
	logger.Info("backup started")

	filename := BuildFilename(item.Server.Name, item.Database.Name, start)
	localPath := filepath.Join(r.Config.Backup.TempDir, filename)
	row.LocalFile = localPath
	row.StoredPaths = []string{filepath.Join(r.Config.Backup.RootDir, item.Server.Name, item.Database.Name, string(retention.TierDaily), filename)}

	if dryRun {
		logger.Info("dry-run backup planned", slog.String("local_file", localPath), slog.Any("stored_paths", row.StoredPaths))
		row.Status = "dry-run"
		row.Duration = time.Since(start)
		return row
	}

	if err := r.createBackupFile(ctx, item, localPath); err != nil {
		row.Status = "error"
		row.Error = err.Error()
		row.Duration = time.Since(start)
		logger.Error("backup failed", slog.Any("error", err))
		return row
	}
	defer os.Remove(localPath)

	info, err := os.Stat(localPath)
	if err != nil {
		row.Status = "error"
		row.Error = fmt.Sprintf("stat backup file: %v", err)
		row.Duration = time.Since(start)
		logger.Error("backup failed", slog.Any("error", err))
		return row
	}
	row.SizeBytes = info.Size()

	if err := r.storeAndRetain(item, localPath, filename, start); err != nil {
		row.Status = "error"
		row.Error = err.Error()
		row.Duration = time.Since(start)
		logger.Error("store/retention failed", slog.Any("error", err))
		return row
	}

	row.Duration = time.Since(start)
	logger.Info("backup completed", slog.String("local_file", localPath), slog.Int64("size_bytes", row.SizeBytes), slog.Duration("duration", row.Duration))
	return row
}

func (r *Runner) createBackupFile(ctx context.Context, item config.SelectedDatabase, localPath string) error {
	outputFile, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp backup file: %w", err)
	}
	defer outputFile.Close()

	gzipWriter, err := gzip.NewWriterLevel(outputFile, r.Config.Backup.GzipLevel)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}

	cmd, stdout, stderrBuf, err := buildDumpCommand(ctx, item)
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return wrapCommandStartError(item.Server.Type, err)
	}

	var wg sync.WaitGroup
	copyErrCh := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, copyErr := io.Copy(gzipWriter, stdout)
		if closeErr := gzipWriter.Close(); copyErr == nil && closeErr != nil {
			copyErr = fmt.Errorf("close gzip writer: %w", closeErr)
		}
		copyErrCh <- copyErr
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	copyErr := <-copyErrCh

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
			"--format=custom",
			"--no-owner",
			"--no-acl",
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
			"-i",
			"-e", "PGPASSWORD=" + password,
			item.Server.Container,
			"pg_dump",
			"--format=custom",
			"--no-owner",
			"--no-acl",
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

		args := make([]string, 0, 6)
		if item.Server.SSHPort > 0 {
			args = append(args, "-p", fmt.Sprintf("%d", item.Server.SSHPort))
		}
		args = append(args, item.Server.SSHTarget, remoteCommand)

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
			"--format=custom",
			"--no-owner",
			"--no-acl",
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
			"-i",
			"-e", shellQuote("PGPASSWORD=" + password),
			shellQuote(item.Server.Container),
			"pg_dump",
			"--format=custom",
			"--no-owner",
			"--no-acl",
			"--username", shellQuote(item.Server.Username),
			shellQuote(item.Database.Name),
		}
		return strings.Join(args, " "), nil
	default:
		return "", fmt.Errorf("unsupported ssh_remote_type %q", item.Server.SSHRemoteType)
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

func CommandPreview(item config.SelectedDatabase) (string, error) {
	switch item.Server.Type {
	case "tcp":
		return fmt.Sprintf(
			"PGPASSWORD=%s pg_dump --format=custom --no-owner --no-acl --host %s --port %d --username %s %s",
			envPlaceholder(item.Server.PasswordEnv),
			item.Server.Host,
			item.Server.Port,
			item.Server.Username,
			item.Database.Name,
		), nil
	case "docker":
		return fmt.Sprintf(
			"docker exec -i -e PGPASSWORD=%s %s pg_dump --format=custom --no-owner --no-acl --username %s %s",
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
		args := make([]string, 0, 6)
		args = append(args, "ssh")
		if item.Server.SSHPort > 0 {
			args = append(args, "-p", fmt.Sprintf("%d", item.Server.SSHPort))
		}
		args = append(args, item.Server.SSHTarget, shellQuote(remoteCommand))
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

func (r *Runner) storeAndRetain(item config.SelectedDatabase, localPath, filename string, now time.Time) error {
	dailyPath := filepath.Join(r.Config.Backup.RootDir, item.Server.Name, item.Database.Name, string(retention.TierDaily), filename)
	if err := copyFile(localPath, dailyPath); err != nil {
		return fmt.Errorf("store daily backup: %w", err)
	}

	planner := &retention.Planner{
		RootDir: r.Config.Backup.RootDir,
		DryRun:  false,
	}
	if err := planner.ApplyAt(item.Server.Name, item.Database.Name, item.Retention, now); err != nil {
		return err
	}
	for _, promotion := range planner.PromoteLog {
		r.Logger.Info("retention promotion",
			slog.String("server", item.Server.Name),
			slog.String("database", item.Database.Name),
			slog.String("from_tier", string(promotion.From)),
			slog.String("to_tier", string(promotion.To)),
			slog.String("file", promotion.File.Name),
		)
	}
	for _, deletion := range planner.DeleteLog {
		r.Logger.Info("retention deletion", slog.String("server", item.Server.Name), slog.String("database", item.Database.Name), slog.String("tier", string(deletion.Tier)), slog.String("file", deletion.File.Name))
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
