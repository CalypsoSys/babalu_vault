package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesPathsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
backup:
  temp_dir: "./tmp"
  root_dir: "./backups"
  log_path: "./logs/pgdrivebackup.log"
  time_of_day: "03:15"
  state_path: "./state/scheduler.json"
retention:
  daily_keep: 14
  weekly_keep: 8
  monthly_keep: 12
servers:
  - name: "local"
    type: "tcp"
    host: "localhost"
    port: 5432
    username: "postgres"
    password: "${LOCAL_PASSWORD}"
    databases:
      - name: "app"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Backup.GzipLevel != 6 {
		t.Fatalf("expected default gzip level 6, got %d", cfg.Backup.GzipLevel)
	}
	if cfg.Backup.RootDir != filepath.Join(dir, "backups") {
		t.Fatalf("unexpected backup root path %q", cfg.Backup.RootDir)
	}
	if cfg.Backup.TimeOfDay != "03:15" {
		t.Fatalf("unexpected time of day %q", cfg.Backup.TimeOfDay)
	}
	if cfg.Backup.LogPath != filepath.Join(dir, "logs", "pgdrivebackup.log") {
		t.Fatalf("unexpected log path %q", cfg.Backup.LogPath)
	}
	if cfg.Backup.StatePath != filepath.Join(dir, "state", "scheduler.json") {
		t.Fatalf("unexpected scheduler state path %q", cfg.Backup.StatePath)
	}
}

func TestFilterUsesGlobalRetention(t *testing.T) {
	cfg := &Config{
		Retention: RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []ServerConfig{
			{
				Name:     "localdev",
				Type:     "tcp",
				Host:     "localhost",
				Port:     5432,
				Username: "postgres",
				Password: "${LOCALDEV_POSTGRES_PASSWORD}",
				Databases: []DatabaseConfig{
					{Name: "db1"},
					{Name: "db2", Retention: &RetentionPolicy{DailyKeep: 30, WeeklyKeep: 12, MonthlyKeep: 24}},
				},
			},
		},
	}

	selected := cfg.Filter("localdev", "db2")
	if len(selected) != 1 {
		t.Fatalf("expected 1 database, got %d", len(selected))
	}
	if selected[0].Retention.DailyKeep != 14 || selected[0].Retention.MonthlyKeep != 12 {
		t.Fatalf("expected global retention, got %+v", selected[0].Retention)
	}
}

func TestTimeOfDayValidation(t *testing.T) {
	cfg := &Config{
		Backup: BackupConfig{RootDir: "/tmp/backups", TimeOfDay: "25:99"},
		Servers: []ServerConfig{
			{
				Name:     "localdev",
				Type:     "tcp",
				Host:     "localhost",
				Port:     5432,
				Username: "postgres",
				Password: "${LOCALDEV_POSTGRES_PASSWORD}",
				Databases: []DatabaseConfig{
					{Name: "db1"},
				},
			},
		},
		Retention: RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid time of day to fail validation")
	}
}

func TestValidateAllowsSSHRemoteDocker(t *testing.T) {
	cfg := &Config{
		Backup:    BackupConfig{RootDir: "/tmp/backups", TimeOfDay: "02:00", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{
				Name:          "remote",
				Type:          "ssh",
				SSHTarget:     "backup@example-host",
				SSHRemoteType: "docker",
				Container:     "ommadb-postgres",
				Username:      "postgres",
				Password:      "${REMOTE_POSTGRES_PASSWORD}",
				Databases: []DatabaseConfig{
					{Name: "mma_data_web"},
				},
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected ssh docker config to validate, got %v", err)
	}
}

func TestValidateAllowsDiscoverDatabasesWithoutStaticList(t *testing.T) {
	cfg := &Config{
		Backup:    BackupConfig{RootDir: "/tmp/backups", TimeOfDay: "02:00", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{
				Name:              "localdev",
				Type:              "tcp",
				Host:              "localhost",
				Port:              5432,
				Username:          "postgres",
				Password:          "${LOCALDEV_POSTGRES_PASSWORD}",
				DiscoverDatabases: true,
				IgnoreDatabases:   []string{"postgres", "template0", "template1"},
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected discover_databases config to validate, got %v", err)
	}
}

func TestValidateRejectsSSHWithoutTarget(t *testing.T) {
	cfg := &Config{
		Backup:    BackupConfig{RootDir: "/tmp/backups", TimeOfDay: "02:00", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{
				Name:          "remote",
				Type:          "ssh",
				SSHRemoteType: "docker",
				Container:     "ommadb-postgres",
				Username:      "postgres",
				Password:      "${REMOTE_POSTGRES_PASSWORD}",
				Databases: []DatabaseConfig{
					{Name: "mma_data_web"},
				},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected ssh config without ssh_target to fail validation")
	}
}

func TestResolveConfiguredSecret(t *testing.T) {
	t.Setenv("APP_PASSWORD", "secret")

	resolved, placeholder, err := ResolveConfiguredSecret("${APP_PASSWORD}")
	if err != nil {
		t.Fatalf("ResolveConfiguredSecret(env) error = %v", err)
	}
	if resolved != "secret" || placeholder != "${APP_PASSWORD}" {
		t.Fatalf("unexpected env secret resolution: resolved=%q placeholder=%q", resolved, placeholder)
	}

	resolved, placeholder, err = ResolveConfiguredSecret("literal")
	if err != nil {
		t.Fatalf("ResolveConfiguredSecret(literal) error = %v", err)
	}
	if resolved != "literal" || placeholder != "<password>" {
		t.Fatalf("unexpected literal secret resolution: resolved=%q placeholder=%q", resolved, placeholder)
	}

	if _, _, err := ResolveConfiguredSecret("$APP_PASSWORD"); err == nil {
		t.Fatal("expected invalid env reference syntax to fail")
	}
}
