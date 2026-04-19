package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joe/calypso_pgvault/pgdrivebackup/internal/config"
)

func TestSelectExcessKeepsNewestManagedFilesOnly(t *testing.T) {
	files := []LocalFile{
		{Path: "/tmp/1", Name: "local_db_2026-04-01_02-00-00.sql.gz"},
		{Path: "/tmp/2", Name: "local_db_2026-04-02_02-00-00.sql.gz"},
		{Path: "/tmp/3", Name: "local_db_2026-04-03_02-00-00.sql.gz"},
		{Path: "/tmp/4", Name: "notes.txt"},
	}
	excess := SelectExcess(files, 2, "local", "db")
	if len(excess) != 1 || excess[0].Path != "/tmp/1" {
		t.Fatalf("unexpected excess files: %+v", excess)
	}
}

func TestHasPeriodBackup(t *testing.T) {
	now := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	files := []LocalFile{{Path: "/tmp/file", Name: "local_db_2026-04-14_02-00-00.sql.gz"}}
	if !HasPeriodBackup(files, TierWeekly, now, "local", "db") {
		t.Fatal("expected weekly backup to exist")
	}
	if !HasPeriodBackup(files, TierMonthly, now, "local", "db") {
		t.Fatal("expected monthly backup to exist")
	}
}

func TestPlannerDryRunDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	dailyDir := filepath.Join(root, "local", "db", "daily")
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"local_db_2026-04-01_02-00-00.sql.gz",
		"local_db_2026-04-02_02-00-00.sql.gz",
	} {
		if err := os.WriteFile(filepath.Join(dailyDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root, DryRun: true}
	err := planner.Apply("local", "db", config.RetentionPolicy{DailyKeep: 1, WeeklyKeep: 0, MonthlyKeep: 0})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dailyDir, "local_db_2026-04-01_02-00-00.sql.gz")); err != nil {
		t.Fatalf("expected file to remain in dry-run, stat error = %v", err)
	}
	if len(planner.DeleteLog) != 1 {
		t.Fatalf("expected 1 planned deletion, got %d", len(planner.DeleteLog))
	}
}

func TestPlannerPromotesDailyToWeeklyAndWeeklyToMonthly(t *testing.T) {
	root := t.TempDir()
	dailyDir := filepath.Join(root, "local", "db", "daily")
	weeklyDir := filepath.Join(root, "local", "db", "weekly")
	monthlyDir := filepath.Join(root, "local", "db", "monthly")
	for _, dir := range []string{dailyDir, weeklyDir, monthlyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"local_db_2026-04-18_02-00-00.sql.gz",
		"local_db_2026-04-17_02-00-00.sql.gz",
		"local_db_2026-04-16_02-00-00.sql.gz",
		"local_db_2026-04-15_02-00-00.sql.gz",
		"local_db_2026-04-10_02-00-00.sql.gz",
	} {
		if err := os.WriteFile(filepath.Join(dailyDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(weeklyDir, "local_db_2026-03-10_02-00-00.sql.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 1}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(weeklyDir, "local_db_2026-04-10_02-00-00.sql.gz")); err != nil {
		t.Fatalf("expected promoted weekly file, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(monthlyDir, "local_db_2026-03-10_02-00-00.sql.gz")); err != nil {
		t.Fatalf("expected promoted monthly file, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dailyDir, "local_db_2026-04-10_02-00-00.sql.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected promoted daily file to leave daily tier, stat err = %v", err)
	}
}
