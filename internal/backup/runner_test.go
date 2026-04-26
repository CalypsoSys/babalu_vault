package backup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/CalypsoSys/babalu_vault/internal/config"
)

func TestSanitizeSensitiveStringMasksPasswordForms(t *testing.T) {
	input := "PGPASSWORD=supersecret password=hunter2 password: swordfish token=abc123 secret=xyz"
	got := sanitizeSensitiveString(input)

	if got == input {
		t.Fatal("expected sensitive values to be masked")
	}
	if containsAny(got, []string{"supersecret", "hunter2", "swordfish", "abc123", "xyz"}) {
		t.Fatalf("expected secrets to be removed, got %q", got)
	}
}

func TestFormatOperationMessageMasksErrorAndCommandValues(t *testing.T) {
	msg := formatOperationMessage(
		"starting backup command",
		slog.String("command", "docker exec -e PGPASSWORD=supersecret db pg_dump app"),
		slog.Any("error", errors.New("connection failed password=hunter2")),
	)

	if containsAny(msg, []string{"supersecret", "hunter2"}) {
		t.Fatalf("expected formatted operation message to redact secrets, got %q", msg)
	}
}

func TestCommandPreviewUsesNoPasswordMode(t *testing.T) {
	tests := []config.SelectedDatabase{
		{
			Server: config.ServerConfig{
				Name:     "local",
				Type:     "tcp",
				Host:     "localhost",
				Port:     5432,
				Username: "postgres",
				Password: "${LOCALDEV_POSTGRES_PASSWORD}",
			},
			Database: config.DatabaseConfig{Name: "app"},
		},
		{
			Server: config.ServerConfig{
				Name:      "dockerdev",
				Type:      "docker",
				Container: "dev-postgres",
				Username:  "postgres",
				Password:  "${LOCALDEV_POSTGRES_PASSWORD}",
			},
			Database: config.DatabaseConfig{Name: "app"},
		},
		{
			Server: config.ServerConfig{
				Name:          "remote",
				Type:          "ssh",
				SSHTarget:     "backup@example-host",
				SSHRemoteType: "docker",
				Container:     "dev-postgres",
				Username:      "postgres",
				Password:      "${REMOTE_POSTGRES_PASSWORD}",
			},
			Database: config.DatabaseConfig{Name: "app"},
		},
	}

	for _, item := range tests {
		preview, err := CommandPreview(item)
		if err != nil {
			t.Fatalf("CommandPreview(%s) error = %v", item.Server.Type, err)
		}
		if !strings.Contains(preview, "--no-password") {
			t.Fatalf("expected --no-password in preview for %s, got %q", item.Server.Type, preview)
		}
		if item.Server.Type == "docker" && strings.Contains(preview, "docker exec -i") {
			t.Fatalf("did not expect docker preview to keep stdin open: %q", preview)
		}
		if item.Server.Type == "ssh" {
			if !strings.Contains(preview, "BatchMode=yes") {
				t.Fatalf("expected ssh preview to disable interactive prompts, got %q", preview)
			}
			if !strings.Contains(preview, "StrictHostKeyChecking=yes") {
				t.Fatalf("expected ssh preview to require a trusted host key, got %q", preview)
			}
		}
	}
}

func TestRunOneEmitsRunningProgress(t *testing.T) {
	var progress []SummaryRow
	runner := &Runner{
		Config: &config.Config{
			Backup: config.BackupConfig{
				RootDir: t.TempDir(),
				TempDir: t.TempDir(),
			},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Progress: func(row SummaryRow) {
			progress = append(progress, row)
		},
	}

	row := runner.runOne(context.Background(), config.SelectedDatabase{
		Server: config.ServerConfig{
			Name:     "local",
			Type:     "tcp",
			Host:     "localhost",
			Port:     5432,
			Username: "postgres",
			Password: "${MISSING_PASSWORD}",
		},
		Database: config.DatabaseConfig{Name: "app"},
	}, false)

	if row.Status != "error" {
		t.Fatalf("expected preview failure to end in error, got %+v", row)
	}
	if len(progress) != 1 {
		t.Fatalf("expected 1 progress event, got %d", len(progress))
	}
	if progress[0].Status != "running" {
		t.Fatalf("expected running progress status, got %+v", progress[0])
	}
	if progress[0].Server != "local" || progress[0].Database != "app" {
		t.Fatalf("unexpected progress target %+v", progress[0])
	}
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
