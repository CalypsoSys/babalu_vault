package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestShouldRunOnStartup(t *testing.T) {
	now := time.Date(2026, 4, 19, 9, 0, 0, 0, time.Local)

	if !shouldRunOnStartup(time.Time{}, now) {
		t.Fatal("expected zero last run to trigger startup backup")
	}
	if !shouldRunOnStartup(now.AddDate(0, 0, -1), now) {
		t.Fatal("expected previous day run to trigger startup backup")
	}
	if shouldRunOnStartup(now.Add(-2*time.Hour), now) {
		t.Fatal("expected same-day run to skip startup backup")
	}
}

func TestShouldRunScheduledBackup(t *testing.T) {
	now := time.Date(2026, 4, 19, 2, 30, 0, 0, time.Local)

	if !shouldRunScheduledBackup(time.Time{}, now, 2, 0) {
		t.Fatal("expected scheduler to run after daily time when no run happened today")
	}
	if shouldRunScheduledBackup(now.Add(-30*time.Minute), now, 2, 0) {
		t.Fatal("expected scheduler to skip when it already ran today")
	}
	if shouldRunScheduledBackup(time.Time{}, now, 3, 0) {
		t.Fatal("expected scheduler to wait until the configured time")
	}
}

func TestNextScheduledRun(t *testing.T) {
	now := time.Date(2026, 4, 19, 1, 0, 0, 0, time.Local)
	next := nextScheduledRun(time.Time{}, now, 2, 0)
	want := time.Date(2026, 4, 19, 2, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("nextScheduledRun() = %v, want %v", next, want)
	}

	now = time.Date(2026, 4, 19, 3, 0, 0, 0, time.Local)
	next = nextScheduledRun(time.Time{}, now, 2, 0)
	if !next.IsZero() {
		t.Fatalf("expected zero next run when backup is due now, got %v", next)
	}

	next = nextScheduledRun(time.Date(2026, 4, 19, 0, 30, 0, 0, time.Local), now, 2, 0)
	want = time.Date(2026, 4, 20, 2, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("nextScheduledRun() after same-day run = %v, want %v", next, want)
	}
}

func TestSaveAndLoadSchedulerState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "scheduler.json")
	want := schedulerState{LastRunAt: time.Date(2026, 4, 19, 2, 15, 0, 0, time.UTC)}

	if err := saveSchedulerState(path, want); err != nil {
		t.Fatalf("saveSchedulerState() error = %v", err)
	}

	got, err := loadSchedulerState(path)
	if err != nil {
		t.Fatalf("loadSchedulerState() error = %v", err)
	}
	if !got.LastRunAt.Equal(want.LastRunAt) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, want.LastRunAt)
	}
}
