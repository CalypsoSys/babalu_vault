package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type schedulerState struct {
	LastRunAt time.Time `json:"last_run_at"`
}

func loadSchedulerState(path string) (schedulerState, error) {
	var state schedulerState
	if path == "" {
		return state, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, fmt.Errorf("read scheduler state: %w", err)
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("parse scheduler state: %w", err)
	}
	return state, nil
}

func saveSchedulerState(path string, state schedulerState) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create scheduler state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal scheduler state: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write scheduler state: %w", err)
	}
	return nil
}

func scheduledTime(now time.Time, hour, minute int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
}

func sameLocalDate(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	a = a.In(b.Location())
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func shouldRunOnStartup(lastRunAt, now time.Time) bool {
	return !sameLocalDate(lastRunAt, now)
}

func shouldRunScheduledBackup(lastRunAt, now time.Time, hour, minute int) bool {
	if sameLocalDate(lastRunAt, now) {
		return false
	}
	return !now.Before(scheduledTime(now, hour, minute))
}

func nextScheduledRun(lastRunAt, now time.Time, hour, minute int) time.Time {
	today := scheduledTime(now, hour, minute)
	if sameLocalDate(lastRunAt, now) {
		return today.AddDate(0, 0, 1)
	}
	if now.Before(today) {
		return today
	}
	return time.Time{}
}
