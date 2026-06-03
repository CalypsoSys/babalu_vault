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

func TestPlannerPreservesTypedExtensionWhenPromotingSnapshots(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "mysql-backup", "wordpress")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 4, 27, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_mysql-backup_wordpress_2026-04-27.sql.gz",
		"daily_mysql-backup_wordpress_2026-04-26.sql.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root, Extension: ".sql.gz"}
	if err := planner.ApplyAt("mysql-backup", "wordpress", config.RetentionPolicy{DailyKeep: 2, WeeklyKeep: 1, MonthlyKeep: 0}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "weekly_mysql-backup_wordpress_2026-04-26.sql.gz")); err != nil {
		t.Fatalf("expected weekly snapshot to keep .sql.gz extension, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "weekly_mysql-backup_wordpress_2026-04-26.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy .gz promotion not to be created, stat error = %v", err)
	}
}

func TestPlannerAppliesReportRetentionSeparatelyFromArchives(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "hasimojoe-logs", "srv-logs")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"daily_hasimojoe-logs_srv-logs_2026-06-01.tar.gz",
		"daily_hasimojoe-logs_srv-logs_2026-06-02.tar.gz",
		"daily_hasimojoe-logs_srv-logs_2026-06-01.report.txt",
		"daily_hasimojoe-logs_srv-logs_2026-06-02.report.txt",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC)
	archivePlanner := &Planner{RootDir: root, Extension: ".tar.gz"}
	if err := archivePlanner.ApplyAt("hasimojoe-logs", "srv-logs", config.RetentionPolicy{DailyKeep: 1, WeeklyKeep: 0, MonthlyKeep: 0}, now); err != nil {
		t.Fatalf("archive ApplyAt() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "daily_hasimojoe-logs_srv-logs_2026-06-01.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected old archive to be pruned, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "daily_hasimojoe-logs_srv-logs_2026-06-01.report.txt")); err != nil {
		t.Fatalf("expected archive retention to leave report file, stat error = %v", err)
	}

	reportPlanner := &Planner{RootDir: root, Extension: ".report.txt"}
	if err := reportPlanner.ApplyAt("hasimojoe-logs", "srv-logs", config.RetentionPolicy{DailyKeep: 1, WeeklyKeep: 0, MonthlyKeep: 0}, now); err != nil {
		t.Fatalf("report ApplyAt() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "daily_hasimojoe-logs_srv-logs_2026-06-01.report.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected old report to be pruned, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "daily_hasimojoe-logs_srv-logs_2026-06-02.tar.gz")); err != nil {
		t.Fatalf("expected report retention to leave archive file, stat error = %v", err)
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

func TestPlannerRefreshesExpiredWeeklySnapshotFromNewestDaily(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "dockerdev-wsl", "inctrak_control")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 7, 2, 5, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_dockerdev-wsl_inctrak_control_2026-05-04.gz",
		"daily_dockerdev-wsl_inctrak_control_2026-05-05.gz",
		"daily_dockerdev-wsl_inctrak_control_2026-05-06.gz",
		"daily_dockerdev-wsl_inctrak_control_2026-05-07.gz",
		"weekly_dockerdev-wsl_inctrak_control_2026-04-28.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("dockerdev-wsl", "inctrak_control", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 0}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "weekly_dockerdev-wsl_inctrak_control_2026-05-07.gz")); err != nil {
		t.Fatalf("expected refreshed weekly snapshot from newest daily, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "weekly_dockerdev-wsl_inctrak_control_2026-04-28.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected expired weekly snapshot to be pruned, stat error = %v", err)
	}
}

func TestPlannerDoesNotRefreshWeeklySnapshotBeforeItExpires(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 5, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_local_db_2026-05-05.gz",
		"weekly_local_db_2026-04-29.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 1, MonthlyKeep: 0}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "weekly_local_db_2026-05-05.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected no refreshed weekly snapshot before existing one expires, stat error = %v", err)
	}
}

func TestPlannerRefreshesExpiredMonthlySnapshotFromNewestWeekly(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "local", "db")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 7, 2, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"daily_local_db_2026-05-07.gz",
		"daily_local_db_2026-04-30.gz",
		"weekly_local_db_2026-05-07.gz",
		"monthly_local_db_2026-04-07.gz",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	planner := &Planner{RootDir: root}
	if err := planner.ApplyAt("local", "db", config.RetentionPolicy{DailyKeep: 4, WeeklyKeep: 0, MonthlyKeep: 1}, now); err != nil {
		t.Fatalf("ApplyAt() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "monthly_local_db_2026-05-07.gz")); err != nil {
		t.Fatalf("expected refreshed monthly snapshot from newest weekly, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "monthly_local_db_2026-04-30.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected prior-month daily snapshot to lose to newer weekly refresh, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "monthly_local_db_2026-04-07.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected expired monthly snapshot to be pruned, stat error = %v", err)
	}
}

func TestPlannerDoesNotCreateWeeklySnapshotUntilPreviousWeekExists(t *testing.T) {
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

	if _, err := os.Stat(filepath.Join(baseDir, "weekly_local_db_2026-04-23.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected no weekly snapshot before a prior week exists, stat error = %v", err)
	}
}

func TestPlannerDoesNotCreateMonthlySnapshotUntilPreviousMonthExists(t *testing.T) {
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

	if _, err := os.Stat(filepath.Join(baseDir, "monthly_local_db_2026-04-23.gz")); !os.IsNotExist(err) {
		t.Fatalf("expected no monthly snapshot before a prior month exists, stat error = %v", err)
	}
}
