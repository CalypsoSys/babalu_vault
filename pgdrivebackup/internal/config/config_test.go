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
  run_interval: "30m"
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
	if cfg.Backup.RunInterval != "30m" {
		t.Fatalf("unexpected run interval %q", cfg.Backup.RunInterval)
	}
	if cfg.Backup.LogPath != filepath.Join(dir, "logs", "pgdrivebackup.log") {
		t.Fatalf("unexpected log path %q", cfg.Backup.LogPath)
	}
}

func TestFilterAndRetentionOverride(t *testing.T) {
	cfg := &Config{
		Retention: RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []ServerConfig{
			{
				Name:     "localdev",
				Type:     "tcp",
				Host:     "localhost",
				Port:     5432,
				Username: "postgres",
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
	if selected[0].Retention.DailyKeep != 30 || selected[0].Retention.MonthlyKeep != 24 {
		t.Fatalf("unexpected retention override: %+v", selected[0].Retention)
	}
}

func TestRunIntervalValidation(t *testing.T) {
	cfg := &Config{
		Backup: BackupConfig{RootDir: "/tmp/backups", RunInterval: "not-a-duration"},
		Servers: []ServerConfig{
			{
				Name:     "localdev",
				Type:     "tcp",
				Host:     "localhost",
				Port:     5432,
				Username: "postgres",
				Databases: []DatabaseConfig{
					{Name: "db1"},
				},
			},
		},
		Retention: RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid run interval to fail validation")
	}
}

func TestValidateAllowsSSHRemoteDocker(t *testing.T) {
	cfg := &Config{
		Backup:    BackupConfig{RootDir: "/tmp/backups", RunInterval: "1h", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{
				Name:          "remote",
				Type:          "ssh",
				SSHTarget:     "backup@example-host",
				SSHRemoteType: "docker",
				Container:     "ommadb-postgres",
				Username:      "postgres",
				PasswordEnv:   "REMOTE_POSTGRES_PASSWORD",
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

func TestValidateRejectsSSHWithoutTarget(t *testing.T) {
	cfg := &Config{
		Backup:    BackupConfig{RootDir: "/tmp/backups", RunInterval: "1h", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{
				Name:          "remote",
				Type:          "ssh",
				SSHRemoteType: "docker",
				Container:     "ommadb-postgres",
				Username:      "postgres",
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
