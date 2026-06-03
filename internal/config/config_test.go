package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesSettingsPathsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
settings:
  temp_dir: "./tmp"
  root_dir: "./backups"
  log_path: "./logs/babalu-vault.log"
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
backups:
  - name: "local-postgres"
    server: "local"
    engine: "postgres"
    source:
      mode: "tcp"
      port: 5432
      username: "postgres"
      password: "${LOCAL_PASSWORD}"
    targets:
      - name: "app"
        database: "app"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Settings.GzipLevel != 6 {
		t.Fatalf("expected default gzip level 6, got %d", cfg.Settings.GzipLevel)
	}
	if cfg.Settings.RootDir != filepath.Join(dir, "backups") {
		t.Fatalf("unexpected backup root path %q", cfg.Settings.RootDir)
	}
	if cfg.Settings.TimeOfDay != "03:15" {
		t.Fatalf("unexpected time of day %q", cfg.Settings.TimeOfDay)
	}
	if cfg.Settings.LogPath != filepath.Join(dir, "logs", "babalu-vault.log") {
		t.Fatalf("unexpected log path %q", cfg.Settings.LogPath)
	}
	if cfg.Settings.StatePath != filepath.Join(dir, "state", "scheduler.json") {
		t.Fatalf("unexpected scheduler state path %q", cfg.Settings.StatePath)
	}
}

func TestFilterUsesBackupsAndTargets(t *testing.T) {
	cfg := &Config{
		Retention: RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []ServerConfig{
			{Name: "localdev", Type: "tcp", Host: "localhost"},
		},
		Backups: []BackupConfig{
			{
				Name:   "local-postgres",
				Server: "localdev",
				Engine: "postgres",
				Source: &SourceConfig{
					Mode:     "tcp",
					Port:     5432,
					Username: "postgres",
					Password: "${LOCALDEV_POSTGRES_PASSWORD}",
				},
				Targets: []TargetConfig{
					{Name: "db1", Database: "db1"},
					{Name: "db2", Database: "db2", Retention: &RetentionPolicy{DailyKeep: 30, WeeklyKeep: 12, MonthlyKeep: 24}},
				},
			},
		},
	}

	selected := cfg.Filter("localdev", "local-postgres", "db2")
	if len(selected) != 1 {
		t.Fatalf("expected 1 target, got %d", len(selected))
	}
	if selected[0].Backup.Name != "local-postgres" || selected[0].Target.Name != "db2" {
		t.Fatalf("unexpected selected target %+v", selected[0])
	}
	if selected[0].Retention.DailyKeep != 14 || selected[0].Retention.MonthlyKeep != 12 {
		t.Fatalf("expected global retention, got %+v", selected[0].Retention)
	}
}

func TestTimeOfDayValidation(t *testing.T) {
	cfg := validConfig()
	cfg.Settings.TimeOfDay = "25:99"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid time of day to fail validation")
	}
}

func TestValidateAllowsSSHServerWithDockerSource(t *testing.T) {
	cfg := validConfig()
	cfg.Servers = []ServerConfig{
		{Name: "remote", Type: "ssh", SSHTarget: "backup@example-host"},
	}
	cfg.Backups = []BackupConfig{
		{
			Name:   "remote-postgres",
			Server: "remote",
			Engine: "postgres",
			Source: &SourceConfig{
				Mode:      "docker",
				Container: "ommadb-postgres",
				Username:  "postgres",
				Password:  "${REMOTE_POSTGRES_PASSWORD}",
			},
			Targets: []TargetConfig{{Name: "mma-data-web", Database: "mma_data_web"}},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected ssh docker config to validate, got %v", err)
	}
}

func TestValidateAllowsDiscoverDatabasesWithoutStaticTargets(t *testing.T) {
	cfg := validConfig()
	cfg.Backups[0].Discovery = DiscoveryConfig{
		Databases: true,
		Ignore:    []string{"postgres", "template0", "template1"},
	}
	cfg.Backups[0].Targets = nil

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected discovery config to validate, got %v", err)
	}
}

func TestValidateRejectsUnknownServerReference(t *testing.T) {
	cfg := validConfig()
	cfg.Backups[0].Server = "missing"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected backup with unknown server to fail validation")
	}
}

func TestValidateRejectsFileBackupWithoutSSHServer(t *testing.T) {
	cfg := validConfig()
	cfg.Servers = []ServerConfig{
		{Name: "local", Type: "docker"},
	}
	cfg.Backups = []BackupConfig{
		{
			Name:    "local-files",
			Server:  "local",
			Engine:  "files",
			Targets: []TargetConfig{{Name: "html", Path: "/srv/site/html"}},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected files backup without ssh server to fail validation")
	}
}

func TestLoadParsesFileTargetSanityChecks(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
settings:
  root_dir: "./backups"
servers:
  - name: "hasimojoe"
    type: "ssh"
    ssh_target: "backup@hasimojoe"
backups:
  - name: "hasimojoe-logs"
    server: "hasimojoe"
    engine: "files"
    targets:
      - name: "srv-logs"
        path: "/srv/logs"
        excludes:
          - "**/*.tmp"
        sanity_checks:
          enabled: true
          scan_rotated: false
          patterns:
            - name: "/.env"
              match: '/\.env'
            - name: "http 5xx"
              match: ' 5[0-9][0-9] '
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	checks := cfg.Backups[0].Targets[0].SanityChecks
	if !checks.Enabled {
		t.Fatal("expected sanity checks to be enabled")
	}
	if checks.ScanRotated {
		t.Fatal("expected scan_rotated to parse as false")
	}
	if len(checks.Patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(checks.Patterns))
	}
	if checks.Patterns[0].Name != "/.env" || checks.Patterns[0].Match != `/\.env` {
		t.Fatalf("unexpected first sanity pattern %+v", checks.Patterns[0])
	}
}

func TestValidateRejectsInvalidSanityCheckPattern(t *testing.T) {
	cfg := validFileConfig()
	cfg.Backups[0].Targets[0].SanityChecks = SanityChecksConfig{
		Enabled: true,
		Patterns: []SanityPatternConfig{
			{Name: "broken", Match: "["},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid sanity check regex to fail validation")
	}
}

func TestValidateRejectsMySQLDiscoveryForNow(t *testing.T) {
	cfg := validConfig()
	cfg.Backups[0].Engine = "mysql"
	cfg.Backups[0].Discovery.Databases = true
	cfg.Backups[0].Targets = nil

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected mysql discovery to fail validation")
	}
}

func TestValidateAllowsMySQLDockerPasswordFromContainerEnv(t *testing.T) {
	cfg := validConfig()
	cfg.Servers[0] = ServerConfig{Name: "remote", Type: "ssh", SSHTarget: "backup@example-host"}
	cfg.Backups[0] = BackupConfig{
		Name:   "site-wp-mysql",
		Server: "remote",
		Engine: "mysql",
		Source: &SourceConfig{
			Mode:        "docker",
			Container:   "site_wp_db",
			Username:    "root",
			PasswordEnv: "MYSQL_ROOT_PASSWORD",
		},
		Targets: []TargetConfig{{Name: "site-db", Database: "wordpress"}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected mysql docker password_env config to validate, got %v", err)
	}
}

func TestValidateRejectsPasswordEnvOutsideMySQLDocker(t *testing.T) {
	cfg := validConfig()
	cfg.Backups[0].Source.Password = ""
	cfg.Backups[0].Source.PasswordEnv = "POSTGRES_PASSWORD"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected password_env outside mysql docker to fail validation")
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

func validConfig() *Config {
	return &Config{
		Settings:  SettingsConfig{RootDir: "/tmp/backups", TimeOfDay: "02:00", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{Name: "localdev", Type: "tcp", Host: "localhost"},
		},
		Backups: []BackupConfig{
			{
				Name:   "local-postgres",
				Server: "localdev",
				Engine: "postgres",
				Source: &SourceConfig{
					Mode:     "tcp",
					Port:     5432,
					Username: "postgres",
					Password: "${LOCALDEV_POSTGRES_PASSWORD}",
				},
				Targets: []TargetConfig{{Name: "db1", Database: "db1"}},
			},
		},
	}
}

func validFileConfig() *Config {
	return &Config{
		Settings:  SettingsConfig{RootDir: "/tmp/backups", TimeOfDay: "02:00", GzipLevel: 6},
		Retention: RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1},
		Servers: []ServerConfig{
			{Name: "hasimojoe", Type: "ssh", SSHTarget: "backup@hasimojoe"},
		},
		Backups: []BackupConfig{
			{
				Name:   "hasimojoe-logs",
				Server: "hasimojoe",
				Engine: "files",
				Targets: []TargetConfig{
					{Name: "srv-logs", Path: "/srv/logs"},
				},
			},
		},
	}
}
