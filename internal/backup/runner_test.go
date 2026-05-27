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
	input := "PGPASSWORD=supersecret MYSQL_PWD=dbsecret password=hunter2 password: swordfish token=abc123 secret=xyz"
	got := sanitizeSensitiveString(input)

	if got == input {
		t.Fatal("expected sensitive values to be masked")
	}
	if containsAny(got, []string{"supersecret", "dbsecret", "hunter2", "swordfish", "abc123", "xyz"}) {
		t.Fatalf("expected secrets to be removed, got %q", got)
	}
}

func TestFormatOperationMessageMasksErrorAndCommandValues(t *testing.T) {
	msg := formatOperationMessage(
		"starting backup command",
		slog.String("command", "docker exec -e MYSQL_PWD=supersecret db mysqldump app"),
		slog.Any("error", errors.New("connection failed password=hunter2")),
	)

	if containsAny(msg, []string{"supersecret", "hunter2"}) {
		t.Fatalf("expected formatted operation message to redact secrets, got %q", msg)
	}
}

func TestCommandPreviewBuildsExpectedShapes(t *testing.T) {
	tests := []struct {
		name     string
		item     config.SelectedTarget
		contains []string
	}{
		{
			name: "postgres tcp",
			item: selectedPostgresTCP(),
			contains: []string{
				"PGPASSWORD=${LOCALDEV_POSTGRES_PASSWORD}",
				"pg_dump",
				"--no-password",
				"--host localhost",
			},
		},
		{
			name: "postgres ssh docker",
			item: selectedPostgresSSHDocker(),
			contains: []string{
				"ssh",
				"BatchMode=yes",
				"StrictHostKeyChecking=yes",
				"docker exec",
				"PGPASSWORD=${REMOTE_POSTGRES_PASSWORD}",
			},
		},
		{
			name: "mysql ssh docker",
			item: selectedMySQLSSHDocker(),
			contains: []string{
				"ssh",
				"docker exec",
				`MYSQL_PWD="$MYSQL_ROOT_PASSWORD"`,
				"sh -c",
				"mysqldump",
				"--single-transaction",
			},
		},
		{
			name: "files ssh",
			item: selectedFileSSH(),
			contains: []string{
				"ssh",
				"tar --numeric-owner --one-file-system",
				"--exclude 'wp-content/cache/**'",
				"-cf - .",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview, err := CommandPreview(tt.item)
			if err != nil {
				t.Fatalf("CommandPreview() error = %v", err)
			}
			for _, needle := range tt.contains {
				if !strings.Contains(preview, needle) {
					t.Fatalf("expected preview to contain %q, got %q", needle, preview)
				}
			}
			if strings.Contains(preview, "docker exec -i") {
				t.Fatalf("did not expect docker preview to keep stdin open: %q", preview)
			}
		})
	}
}

func TestCommandPreviewRejectsDatabaseBackupWithoutSource(t *testing.T) {
	item := selectedPostgresTCP()
	item.Backup.Source = nil

	if _, err := CommandPreview(item); err == nil {
		t.Fatal("expected missing database source to fail")
	}
}

func TestRunOneEmitsRunningProgress(t *testing.T) {
	var progress []SummaryRow
	runner := &Runner{
		Config: &config.Config{
			Settings: config.SettingsConfig{
				RootDir: t.TempDir(),
				TempDir: t.TempDir(),
			},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Progress: func(row SummaryRow) {
			progress = append(progress, row)
		},
	}

	item := selectedPostgresTCP()
	item.Backup.Source.Password = "${MISSING_PASSWORD}"
	row := runner.runOne(context.Background(), item, false)

	if row.Status != "error" {
		t.Fatalf("expected preview failure to end in error, got %+v", row)
	}
	if len(progress) != 1 {
		t.Fatalf("expected 1 progress event, got %d", len(progress))
	}
	if progress[0].Status != "running" {
		t.Fatalf("expected running progress status, got %+v", progress[0])
	}
	if progress[0].Server != "local" || progress[0].Backup != "local-postgres" || progress[0].Database != "app" {
		t.Fatalf("unexpected progress target %+v", progress[0])
	}
}

func TestResolveTargetsDiscoversDatabasesAndAppliesIgnoreList(t *testing.T) {
	original := listDatabasesFn
	listDatabasesFn = func(ctx context.Context, item config.SelectedTarget) ([]string, error) {
		return []string{"template1", "app2", "postgres", "app1"}, nil
	}
	defer func() {
		listDatabasesFn = original
	}()

	cfg := &config.Config{
		Retention: config.RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []config.ServerConfig{
			{Name: "localdev", Type: "tcp", Host: "localhost"},
		},
		Backups: []config.BackupConfig{
			{
				Name:   "local-postgres",
				Server: "localdev",
				Engine: "postgres",
				Source: &config.SourceConfig{
					Mode:     "tcp",
					Port:     5432,
					Username: "postgres",
					Password: "${LOCALDEV_POSTGRES_PASSWORD}",
				},
				Discovery: config.DiscoveryConfig{Databases: true, Ignore: []string{"app2"}},
				Targets: []config.TargetConfig{
					{Name: "app1", Database: "app1", Retention: &config.RetentionPolicy{DailyKeep: 30, WeeklyKeep: 12, MonthlyKeep: 24}},
				},
			},
		},
	}

	selected, err := ResolveTargets(context.Background(), cfg, "", "", "")
	if err != nil {
		t.Fatalf("ResolveTargets() error = %v", err)
	}
	if len(selected) != 1 {
		t.Fatalf("expected 1 selected target, got %d", len(selected))
	}
	if selected[0].Target.Name != "app1" || selected[0].Target.DatabaseName() != "app1" {
		t.Fatalf("expected app1, got %+v", selected[0].Target)
	}
	if selected[0].Retention.DailyKeep != 14 || selected[0].Retention.MonthlyKeep != 12 {
		t.Fatalf("expected global retention for discovered target, got %+v", selected[0].Retention)
	}
}

func TestResolveTargetsSupportsBackupAndTargetFilter(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []config.ServerConfig{
			{Name: "localdev", Type: "tcp", Host: "localhost"},
		},
		Backups: []config.BackupConfig{
			{
				Name:   "local-postgres",
				Server: "localdev",
				Engine: "postgres",
				Source: &config.SourceConfig{
					Mode:     "tcp",
					Port:     5432,
					Username: "postgres",
					Password: "${LOCALDEV_POSTGRES_PASSWORD}",
				},
				Targets: []config.TargetConfig{{Name: "app1", Database: "app1"}, {Name: "app2", Database: "app2"}},
			},
		},
	}

	selected, err := ResolveTargets(context.Background(), cfg, "localdev", "local-postgres", "app2")
	if err != nil {
		t.Fatalf("ResolveTargets() error = %v", err)
	}
	if len(selected) != 1 || selected[0].Target.Name != "app2" {
		t.Fatalf("expected only app2, got %+v", selected)
	}
}

func selectedPostgresTCP() config.SelectedTarget {
	return config.SelectedTarget{
		Server: config.ServerConfig{Name: "local", Type: "tcp", Host: "localhost"},
		Backup: config.BackupConfig{
			Name:   "local-postgres",
			Server: "local",
			Engine: "postgres",
			Source: &config.SourceConfig{
				Mode:     "tcp",
				Port:     5432,
				Username: "postgres",
				Password: "${LOCALDEV_POSTGRES_PASSWORD}",
			},
		},
		Target: config.TargetConfig{Name: "app", Database: "app"},
	}
}

func selectedPostgresSSHDocker() config.SelectedTarget {
	return config.SelectedTarget{
		Server: config.ServerConfig{Name: "remote", Type: "ssh", SSHTarget: "backup@example-host"},
		Backup: config.BackupConfig{
			Name:   "remote-postgres",
			Server: "remote",
			Engine: "postgres",
			Source: &config.SourceConfig{
				Mode:      "docker",
				Container: "dev-postgres",
				Username:  "postgres",
				Password:  "${REMOTE_POSTGRES_PASSWORD}",
			},
		},
		Target: config.TargetConfig{Name: "app", Database: "app"},
	}
}

func selectedMySQLSSHDocker() config.SelectedTarget {
	return config.SelectedTarget{
		Server: config.ServerConfig{Name: "homelab", Type: "ssh", SSHTarget: "backup@homelab"},
		Backup: config.BackupConfig{
			Name:   "dndboomer-mysql",
			Server: "homelab",
			Engine: "mysql",
			Source: &config.SourceConfig{
				Mode:        "docker",
				Container:   "dndboomer_wp_db",
				Username:    "root",
				PasswordEnv: "MYSQL_ROOT_PASSWORD",
			},
		},
		Target: config.TargetConfig{Name: "dndboomer-db", Database: "wordpress"},
	}
}

func selectedFileSSH() config.SelectedTarget {
	return config.SelectedTarget{
		Server: config.ServerConfig{Name: "homelab", Type: "ssh", SSHTarget: "backup@homelab"},
		Backup: config.BackupConfig{Name: "homelab-wp-files", Server: "homelab", Engine: "files"},
		Target: config.TargetConfig{
			Name: "dndboomer-html",
			Path: "/srv/stacks/wordpress/sites/dndboomer/html",
			Excludes: []string{
				"wp-content/cache/**",
				"wp-content/debug.log",
			},
		},
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
