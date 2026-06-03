package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/backup"
	"github.com/CalypsoSys/babalu_vault/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestReconcileStatusesPreservesExistingResults(t *testing.T) {
	cfg := &config.Config{
		Retention: config.RetentionPolicy{DailyKeep: 14, WeeklyKeep: 8, MonthlyKeep: 12},
		Servers: []config.ServerConfig{
			{Name: "local", Type: "tcp", Host: "localhost"},
		},
		Backups: []config.BackupConfig{
			{
				Name:   "local-postgres",
				Server: "local",
				Engine: "postgres",
				Source: &config.SourceConfig{Mode: "tcp", Port: 5432, Username: "postgres", Password: "${DB_PASSWORD}"},
				Targets: []config.TargetConfig{
					{Name: "app", Database: "app"},
					{Name: "audit", Database: "audit", Retention: &config.RetentionPolicy{DailyKeep: 30, WeeklyKeep: 12, MonthlyKeep: 6}},
				},
			},
		},
	}
	previous := []databaseStatus{
		{
			Server:      "local",
			Backup:      "local-postgres",
			Database:    "app",
			Method:      "postgres",
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
		t.Fatalf("expected new target to start pending, got %+v", got[1])
	}
	if got[1].Retention.DailyKeep != 14 || got[1].Retention.MonthlyKeep != 12 {
		t.Fatalf("expected global retention, got %+v", got[1].Retention)
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
settings:
  root_dir: "./backups"
  time_of_day: "02:00"
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
      password: "${DB_PASSWORD}"
    targets:
      - name: "app"
        database: "app"
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
settings:
  root_dir: "./backups"
  time_of_day: "04:30"
servers:
  - name: "local"
    type: "docker"
backups:
  - name: "local-postgres"
    server: "local"
    engine: "postgres"
    source:
      mode: "docker"
      username: "postgres"
      password: "${DB_PASSWORD}"
      container: "postgres"
    targets:
      - name: "app"
        database: "app"
      - name: "audit"
        database: "audit"
`)

	m.reloadConfigIfChanged()

	if m.scheduleHour != 4 || m.scheduleMin != 30 {
		t.Fatalf("expected reloaded schedule 04:30, got %02d:%02d", m.scheduleHour, m.scheduleMin)
	}
	if len(m.statuses) != 2 {
		t.Fatalf("expected 2 statuses after reload, got %d", len(m.statuses))
	}
	if m.statuses[0].Method != "postgres" || m.statuses[0].LastStatus != "ok" || m.statuses[0].LastSize != 42 {
		t.Fatalf("expected existing target state to survive reload, got %+v", m.statuses[0])
	}
	if m.statuses[1].Database != "audit" || m.statuses[1].LastStatus != "pending" {
		t.Fatalf("expected new target to start pending, got %+v", m.statuses[1])
	}
}

func TestBackupProgressMarksMatchingTargetRunning(t *testing.T) {
	m := model{
		statuses: []databaseStatus{
			{
				Server:     "local",
				Backup:     "local-postgres",
				Database:   "app",
				Method:     "postgres",
				LastStatus: "pending",
			},
		},
	}

	m.markStatusRunning(backup.SummaryRow{
		Server:   "local",
		Backup:   "local-postgres",
		Database: "app",
		Method:   "postgres",
		Status:   "running",
	})

	if m.statuses[0].LastStatus != "running" {
		t.Fatalf("expected target to become running, got %+v", m.statuses[0])
	}
}

func TestRenderReportsContentShowsLogRuns(t *testing.T) {
	reportDate := time.Date(2026, 6, 3, 13, 57, 0, 0, time.UTC)
	m := model{
		statuses: []databaseStatus{
			{
				Server:   "hasimojoe",
				Backup:   "homelab-logs",
				Database: "srv-logs",
				LastReport: &backup.SanityReportSummary{
					Server:            "hasimojoe",
					Backup:            "homelab-logs",
					Target:            "srv-logs",
					SourcePath:        "/srv/logs",
					Date:              reportDate,
					ArchivedFileCount: 18,
					ScannedFileCount:  12,
					SkippedFileCount:  6,
					TotalMatchedLines: 55,
					HTTPStatusCounts: []backup.StatusCount{
						{Status: "200", Count: 2},
						{Status: "404", Count: 53},
					},
					TopSourceIPs: []backup.SourceIPCount{
						{IP: "203.0.113.10", Count: 30},
					},
					Findings: []backup.SanityFinding{
						{
							Server:  "hasimojoe",
							Backup:  "homelab-logs",
							Target:  "srv-logs",
							Pattern: "/.env",
							Count:   46,
							HTTPStatusCounts: []backup.StatusCount{
								{Status: "200", Count: 2},
								{Status: "404", Count: 44},
							},
						},
						{
							Server:  "hasimojoe",
							Backup:  "homelab-logs",
							Target:  "srv-logs",
							Pattern: "/.env.local",
							Count:   9,
							HTTPStatusCounts: []backup.StatusCount{
								{Status: "404", Count: 9},
							},
						},
					},
					ReportPath: "/tmp/report.txt",
				},
			},
		},
	}

	reports := m.reports()
	if len(reports) != 1 || reports[0].Backup != "homelab-logs" {
		t.Fatalf("expected one report run, got %+v", reports)
	}

	content, selectedLine := renderReportsContent(m)
	if selectedLine != 0 {
		t.Fatalf("expected selected report line 0, got %d", selectedLine)
	}
	for _, expected := range []string{
		"> homelab-logs/srv-logs",
		"  hasimojoe  matched 55  200:2 404:53",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("expected reports content to contain %q, got:\n%s", expected, content)
		}
	}

	selected := m.selectedReport()
	if selected == nil || selected.TotalMatchedLines != 55 || selected.ReportPath != "/tmp/report.txt" {
		t.Fatalf("unexpected selected report %+v", selected)
	}

	overlay := renderReportDetailsOverlay("base", m, tuiPalette())
	for _, expected := range []string{
		"Log Run Summary",
		"matched lines 55",
		"Pattern Counts",
		"/.env: 46  200:2 404:44",
		"Report File",
	} {
		if !strings.Contains(overlay, expected) {
			t.Fatalf("expected report overlay to contain %q, got:\n%s", expected, overlay)
		}
	}
}

func TestBackupFinishedSkipsDuplicateStartedActivity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := newModel("configs/example.yaml", &config.Config{}, false, logger)
	m.running = true
	m.backupMsgs = make(chan tea.Msg, 1)

	updated, _ := m.Update(backupFinishedMsg{
		rows: []backup.SummaryRow{
			{
				Server:   "local",
				Backup:   "local-postgres",
				Database: "app",
				Method:   "postgres",
				Status:   "ok",
				Operations: []backup.OperationEntry{
					{Level: "info", Message: "backup started"},
					{Level: "info", Message: "backup completed"},
				},
			},
		},
		startedAt:  time.Date(2026, 4, 26, 9, 0, 0, 0, time.UTC),
		finishedAt: time.Date(2026, 4, 26, 9, 0, 2, 0, time.UTC),
	})
	got := updated.(model)

	count := 0
	for _, event := range got.events {
		if event.Message == "local/local-postgres/app [postgres] backup started" {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("expected completed update to skip duplicate started event, got %d", count)
	}
}
