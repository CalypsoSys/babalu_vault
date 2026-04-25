package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/config"
)

func TestReconcileStatusesPreservesExistingResults(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []config.ServerConfig{
			{
				Name:     "local",
				Type:     "tcp",
				Host:     "localhost",
				Port:     5432,
				Username: "postgres",
				Password: "${DB_PASSWORD}",
				Databases: []config.DatabaseConfig{
					{Name: "app"},
					{Name: "audit", Retention: &config.RetentionPolicy{DailyKeep: 30, WeeklyKeep: 12, MonthlyKeep: 6}},
				},
			},
		},
	}
	previous := []databaseStatus{
		{
			Server:      "local",
			Database:    "app",
			Method:      "tcp",
			Retention:   config.RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
			LastStatus:  "ok",
			LastError:   "",
			LastSize:    1234,
			LastElapsed: 2 * time.Second,
			LastRun:     time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
			LastPaths:   []string{"/tmp/app.dump.gz"},
		},
	}

	got := reconcileStatuses(previous, cfg, false)
	if len(got) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(got))
	}
	if got[0].Database != "app" || got[0].LastStatus != "ok" || got[0].LastSize != 1234 {
		t.Fatalf("expected existing status to be preserved, got %+v", got[0])
	}
	if got[1].Database != "audit" || got[1].LastStatus != "pending" {
		t.Fatalf("expected new database to start pending, got %+v", got[1])
	}
	if got[1].Retention.DailyKeep != 30 || got[1].Retention.MonthlyKeep != 6 {
		t.Fatalf("expected updated retention override, got %+v", got[1].Retention)
	}
}

func TestReloadConfigIfChangedAppliesNewScheduleAndTargets(t *testing.T) {
	t.Setenv("DB_PASSWORD", "secret")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeConfig := func(content string) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	writeConfig(`
backup:
  root_dir: "./backups"
  time_of_day: "02:00"
servers:
  - name: "local"
    type: "tcp"
    host: "localhost"
    port: 5432
    username: "postgres"
    password: "${DB_PASSWORD}"
    databases:
      - name: "app"
`)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load initial config: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := newModel(configPath, cfg, false, logger)
	m.statuses[0].LastStatus = "ok"
	m.statuses[0].LastSize = 42

	time.Sleep(1100 * time.Millisecond)
	writeConfig(`
backup:
  root_dir: "./backups"
  time_of_day: "04:30"
servers:
  - name: "local"
    type: "docker"
    username: "postgres"
    password: "${DB_PASSWORD}"
    container: "postgres"
    databases:
      - name: "app"
      - name: "audit"
`)

	m.reloadConfigIfChanged()

	if m.scheduleHour != 4 || m.scheduleMin != 30 {
		t.Fatalf("expected reloaded schedule 04:30, got %02d:%02d", m.scheduleHour, m.scheduleMin)
	}
	if len(m.statuses) != 2 {
		t.Fatalf("expected 2 statuses after reload, got %d", len(m.statuses))
	}
	if m.statuses[0].Method != "docker" || m.statuses[0].LastStatus != "ok" || m.statuses[0].LastSize != 42 {
		t.Fatalf("expected existing target state to survive reload, got %+v", m.statuses[0])
	}
	if m.statuses[1].Database != "audit" || m.statuses[1].LastStatus != "pending" {
		t.Fatalf("expected new target to start pending, got %+v", m.statuses[1])
	}
}
