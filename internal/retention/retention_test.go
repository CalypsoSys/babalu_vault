package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/config"
)

func TestSelectExcessKeepsNewestManagedFilesOnly(t *testing.T) {
	files := []LocalFile{
		{Path: "/tmp/1", Name: "daily_local_db_2026-04-01.gz"},
		{Path: "/tmp/2", Name: "daily_local_db_2026-04-02.gz"},
		{Path: "/tmp/3", Name: "daily_local_db_2026-04-03.gz"},
		{Path: "/tmp/4", Name: "notes.txt"},
	}
	excess := SelectExcess(files, 2, TierDaily, "local", "db")
	if len(excess) != 1 || excess[0].Path != "/tmp/1" {
		t.Fatalf("unexpected excess files: %+v", excess)
	}
}

func TestHasPeriodBackup(t *testing.T) {
	now := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	files := []LocalFile{{Path: "/tmp/file", Name: "weekly_local_db_2026-04-14.gz"}}
	if !HasPeriodBackup(files, TierWeekly, now, "local", "db") {
		t.Fatal("expected weekly backup to exist")
	}
}

func TestPlannerDryRunDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"daily_local_db_2026-04-01.gz",
		"daily_local_db_2026-04-02.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root, DryRun: true}
	err := planner.Apply("local", "db", config.RetentionPolicy{DailyKeep: 1, WeeklyKeep: 0, MonthlyKeep: 0})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "daily_local_db_2026-04-01.gz")); err != nil {
		t.Fatalf("expected file to remain in dry-run, stat error = %v", err)
	}
	if len(planner.DeleteLog) != 1 {
		t.Fatalf("expected 1 planned deletion, got %d", len(planner.DeleteLog))
	}
}

func TestPlannerCreatesWeeklySnapshotForPreviousWeek(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 4, 27, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_local_db_2026-04-27.gz",
		"daily_local_db_2026-04-26.gz",
		"daily_local_db_2026-04-25.gz",
		"daily_local_db_2026-04-24.gz",
		"daily_local_db_2026-04-23.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "weekly_local_db_2026-04-26.gz")); err != nil {
		t.Fatalf("expected weekly snapshot for prior ISO week, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "daily_local_db_2026-04-26.gz")); err != nil {
		t.Fatalf("expected source daily file to remain after weekly snapshot, stat error = %v", err)
	}
}

func TestPlannerCreatesMonthlySnapshotForPreviousMonth(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 2, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_local_db_2026-05-02.gz",
		"daily_local_db_2026-05-01.gz",
		"daily_local_db_2026-04-30.gz",
		"daily_local_db_2026-04-29.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "monthly_local_db_2026-04-30.gz")); err != nil {
		t.Fatalf("expected monthly snapshot for prior calendar month, stat error = %v", err)
	}
}

func TestPlannerBootstrapsWeeklySnapshotWhenTierEmpty(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 4, 26, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_local_db_2026-04-26.gz",
		"daily_local_db_2026-04-25.gz",
		"daily_local_db_2026-04-24.gz",
		"daily_local_db_2026-04-23.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "weekly_local_db_2026-04-23.gz")); err != nil {
		t.Fatalf("expected weekly bootstrap snapshot from oldest daily, stat error = %v", err)
	}
}

func TestPlannerBootstrapsMonthlySnapshotWhenTierEmpty(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 4, 26, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_local_db_2026-04-26.gz",
		"daily_local_db_2026-04-25.gz",
		"daily_local_db_2026-04-24.gz",
		"daily_local_db_2026-04-23.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "monthly_local_db_2026-04-23.gz")); err != nil {
		t.Fatalf("expected monthly bootstrap snapshot from oldest daily, stat error = %v", err)
	}
}
