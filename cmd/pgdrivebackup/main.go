package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/backup"
	"github.com/CalypsoSys/babalu_vault/internal/config"
	"github.com/CalypsoSys/babalu_vault/internal/logging"
	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.2.0"

func main() {
	logger, closer, err := newConfiguredLogger(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger setup failed: %v\n", err)
		os.Exit(1)
	}
	defer closer.Close()
	if err := run(logger, os.Args[1:]); err != nil {
		logger.Error("command failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func newConfiguredLogger(args []string) (*slog.Logger, interface{ Close() error }, error) {
	configPath := defaultConfigPath()
	for len(args) > 0 {
		switch args[0] {
		case "--config":
			if len(args) < 2 {
				break
			}
			configPath = args[1]
			args = args[2:]
		default:
			args = args[1:]
		}
	}
	if cfg, err := config.Load(configPath); err == nil {
		return logging.New(cfg.Backup.LogPath)
	}
	return logging.New("")
}

func run(logger *slog.Logger, args []string) error {
	configPath := defaultConfigPath()
	uiDryRun := false

	for len(args) > 0 {
		switch args[0] {
		case "--config":
			if len(args) < 2 {
				return errors.New("--config requires a value")
			}
			configPath = args[1]
			args = args[2:]
		case "--dry-run":
			uiDryRun = true
			args = args[1:]
		default:
			goto parsedGlobalFlags
		}
	}
parsedGlobalFlags:

	if len(args) == 0 {
		return runUI(configPath, uiDryRun)
	}

	switch args[0] {
	case "help", "--help", "-h":
		printRootUsage()
		return nil
	case "version":
		fmt.Println(version)
		return nil
	case "ui":
		return runUI(configPath, uiDryRun)
	case "list":
		return runList(configPath)
	case "backup":
		return runBackup(logger, configPath, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func defaultConfigPath() string {
	return filepath.Join("configs", "example.yaml")
}

func runUI(configPath string, dryRun bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger, closer, err := logging.NewForTUI(cfg.Backup.LogPath)
	if err != nil {
		return err
	}
	defer closer.Close()
	program := tea.NewProgram(newModel(configPath, cfg, dryRun, logger), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func runList(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVER\tTYPE\tDATABASE\tRETENTION")
	for _, server := range cfg.Servers {
		for _, database := range server.Databases {
			r := cfg.RetentionFor(database)
			fmt.Fprintf(w, "%s\t%s\t%s\tdaily=%d weekly=%d monthly=%d\n", server.Name, server.Type, database.Name, r.DailyKeep, r.WeeklyKeep, r.MonthlyKeep)
		}
	}
	return w.Flush()
}

func runBackup(logger *slog.Logger, configPath string, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	serverName := fs.String("server", "", "Limit to one configured server")
	databaseName := fs.String("database", "", "Limit to one configured database")
	dryRun := fs.Bool("dry-run", false, "Print planned actions without creating backups or deleting old backups")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("backup does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	rows, runErr := executeBackup(logger, cfg, *serverName, *databaseName, *dryRun || cfg.Backup.DryRun)
	printSummary(rows)
	if runErr != nil {
		var pathErr *os.PathError
		if errors.As(runErr, &pathErr) {
			return fmt.Errorf("%w; tooling hint: %s", runErr, backup.ToolingHint())
		}
		return runErr
	}
	return nil
}

func executeBackup(logger *slog.Logger, cfg *config.Config, serverName, databaseName string, dryRun bool) ([]backup.SummaryRow, error) {
	return executeBackupWithProgress(logger, cfg, serverName, databaseName, dryRun, nil)
}

func executeBackupWithProgress(logger *slog.Logger, cfg *config.Config, serverName, databaseName string, dryRun bool, progress func(backup.SummaryRow)) ([]backup.SummaryRow, error) {
	runner := &backup.Runner{
		Config:   cfg,
		Logger:   logger,
		Progress: progress,
	}
	return runner.Run(context.Background(), serverName, databaseName, dryRun)
}

func printRootUsage() {
	fmt.Fprintf(os.Stdout, "pgdrivebackup backs up PostgreSQL databases to local storage.\n\n")
	fmt.Fprintf(os.Stdout, "Usage:\n")
	fmt.Fprintf(os.Stdout, "  pgdrivebackup [--config PATH] [command] [flags]\n\n")
	fmt.Fprintf(os.Stdout, "Commands:\n")
	fmt.Fprintf(os.Stdout, "  ui         Launch the continuous terminal UI (default)\n")
	fmt.Fprintf(os.Stdout, "  list       List configured servers and databases\n")
	fmt.Fprintf(os.Stdout, "  backup     Back up configured databases to local storage\n")
	fmt.Fprintf(os.Stdout, "  version    Print version\n")
	fmt.Fprintf(os.Stdout, "\nGlobal flags:\n")
	fmt.Fprintf(os.Stdout, "  --config PATH  Use a specific config file\n")
	fmt.Fprintf(os.Stdout, "  --dry-run      Start the TUI in dry-run mode\n")
}

func printSummary(rows []backup.SummaryRow) {
	if len(rows) == 0 {
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVER\tDATABASE\tMETHOD\tSTATUS\tSIZE\tDURATION\tDETAIL")
	for _, row := range rows {
		detail := strings.Join(row.StoredPaths, ", ")
		if row.Error != "" {
			detail = row.Error
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			row.Server,
			row.Database,
			row.Method,
			row.Status,
			row.SizeBytes,
			row.Duration.Round(time.Millisecond),
			detail,
		)
	}
	_ = w.Flush()
}
