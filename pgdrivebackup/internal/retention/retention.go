package retention

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/joe/calypso_pgvault/pgdrivebackup/internal/config"
)

type Tier string

const (
	TierDaily   Tier = "daily"
	TierWeekly  Tier = "weekly"
	TierMonthly Tier = "monthly"
)

type Candidate struct {
	File LocalFile
	Time time.Time
}

type Deletion struct {
	Tier Tier
	File LocalFile
}

type Promotion struct {
	From Tier
	To   Tier
	File LocalFile
}

type Planner struct {
	RootDir    string
	DryRun     bool
	DeleteLog  []Deletion
	PromoteLog []Promotion
}

type LocalFile struct {
	Path string
	Name string
}

var managedFilenamePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.dump\.gz$`)

func SelectExcess(files []LocalFile, keep int, serverName, databaseName string) []LocalFile {
	if keep < 0 {
		keep = 0
	}
	var managed []Candidate
	for _, file := range files {
		ts, ok := parseManagedTimestamp(file.Name, serverName, databaseName)
		if !ok {
			continue
		}
		managed = append(managed, Candidate{File: file, Time: ts})
	}
	sort.Slice(managed, func(i, j int) bool {
		return managed[i].Time.After(managed[j].Time)
	})
	if len(managed) <= keep {
		return nil
	}
	var excess []LocalFile
	for _, candidate := range managed[keep:] {
		excess = append(excess, candidate.File)
	}
	return excess
}

func HasPeriodBackup(files []LocalFile, tier Tier, now time.Time, serverName, databaseName string) bool {
	for _, file := range files {
		if samePeriod(file.Name, tier, now, serverName, databaseName) {
			return true
		}
	}
	return false
}

func (p *Planner) Apply(serverName, databaseName string, policy config.RetentionPolicy) error {
	return p.ApplyAt(serverName, databaseName, policy, time.Now().UTC())
}

func (p *Planner) ApplyAt(serverName, databaseName string, policy config.RetentionPolicy, now time.Time) error {
	p.DeleteLog = nil
	p.PromoteLog = nil

	dirs := map[Tier]string{
		TierDaily:   filepath.Join(p.RootDir, serverName, databaseName, string(TierDaily)),
		TierWeekly:  filepath.Join(p.RootDir, serverName, databaseName, string(TierWeekly)),
		TierMonthly: filepath.Join(p.RootDir, serverName, databaseName, string(TierMonthly)),
	}
	for tier, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create retention directory for %s/%s/%s: %w", serverName, databaseName, tier, err)
		}
	}

	dailyFiles, err := ListLocalFiles(dirs[TierDaily])
	if err != nil {
		return fmt.Errorf("list daily files: %w", err)
	}
	weeklyFiles, err := ListLocalFiles(dirs[TierWeekly])
	if err != nil {
		return fmt.Errorf("list weekly files: %w", err)
	}
	monthlyFiles, err := ListLocalFiles(dirs[TierMonthly])
	if err != nil {
		return fmt.Errorf("list monthly files: %w", err)
	}

	if err := p.promoteOverflow(dailyFiles, &weeklyFiles, serverName, databaseName, policy.DailyKeep, now, 7*24*time.Hour, TierDaily, TierWeekly, dirs[TierWeekly]); err != nil {
		return err
	}
	if err := p.promoteOverflow(weeklyFiles, &monthlyFiles, serverName, databaseName, policy.WeeklyKeep, now, 30*24*time.Hour, TierWeekly, TierMonthly, dirs[TierMonthly]); err != nil {
		return err
	}

	dailyFiles, err = ListLocalFiles(dirs[TierDaily])
	if err != nil {
		return fmt.Errorf("relist daily files: %w", err)
	}
	weeklyFiles, err = ListLocalFiles(dirs[TierWeekly])
	if err != nil {
		return fmt.Errorf("relist weekly files: %w", err)
	}
	monthlyFiles, err = ListLocalFiles(dirs[TierMonthly])
	if err != nil {
		return fmt.Errorf("relist monthly files: %w", err)
	}

	for _, item := range []struct {
		tier  Tier
		keep  int
		files []LocalFile
	}{
		{tier: TierDaily, keep: policy.DailyKeep, files: dailyFiles},
		{tier: TierWeekly, keep: policy.WeeklyKeep, files: weeklyFiles},
		{tier: TierMonthly, keep: policy.MonthlyKeep, files: monthlyFiles},
	} {
		excess := SelectExcess(item.files, item.keep, serverName, databaseName)
		for _, file := range excess {
			p.DeleteLog = append(p.DeleteLog, Deletion{Tier: item.tier, File: file})
			if p.DryRun {
				continue
			}
			if err := os.Remove(file.Path); err != nil {
				return fmt.Errorf("delete expired file %s: %w", file.Name, err)
			}
		}
	}
	return nil
}

func ListLocalFiles(dir string) ([]LocalFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]LocalFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, LocalFile{
			Path: filepath.Join(dir, entry.Name()),
			Name: entry.Name(),
		})
	}
	return files, nil
}

func (p *Planner) promoteOverflow(sourceFiles []LocalFile, destFiles *[]LocalFile, serverName, databaseName string, keep int, now time.Time, minAge time.Duration, fromTier, toTier Tier, destDir string) error {
	overflow := overflowCandidates(sourceFiles, keep, serverName, databaseName)
	for _, candidate := range overflow {
		if now.Sub(candidate.Time) < minAge {
			continue
		}
		if hasPeriodAt(*destFiles, toTier, candidate.Time, serverName, databaseName) {
			continue
		}

		destPath := filepath.Join(destDir, candidate.File.Name)
		p.PromoteLog = append(p.PromoteLog, Promotion{
			From: fromTier,
			To:   toTier,
			File: LocalFile{Path: destPath, Name: candidate.File.Name},
		})
		if p.DryRun {
			*destFiles = append(*destFiles, LocalFile{Path: destPath, Name: candidate.File.Name})
			continue
		}
		if err := moveFile(candidate.File.Path, destPath); err != nil {
			return fmt.Errorf("promote %s backup %s to %s: %w", fromTier, candidate.File.Name, toTier, err)
		}
		*destFiles = append(*destFiles, LocalFile{Path: destPath, Name: candidate.File.Name})
	}
	return nil
}

func overflowCandidates(files []LocalFile, keep int, serverName, databaseName string) []Candidate {
	if keep < 0 {
		keep = 0
	}
	candidates := managedCandidates(files, serverName, databaseName)
	if len(candidates) <= keep {
		return nil
	}
	overflow := append([]Candidate(nil), candidates[keep:]...)
	sort.Slice(overflow, func(i, j int) bool {
		return overflow[i].Time.Before(overflow[j].Time)
	})
	return overflow
}

func managedCandidates(files []LocalFile, serverName, databaseName string) []Candidate {
	var managed []Candidate
	for _, file := range files {
		ts, ok := parseManagedTimestamp(file.Name, serverName, databaseName)
		if !ok {
			continue
		}
		managed = append(managed, Candidate{File: file, Time: ts})
	}
	sort.Slice(managed, func(i, j int) bool {
		return managed[i].Time.After(managed[j].Time)
	})
	return managed
}

func hasPeriodAt(files []LocalFile, tier Tier, ts time.Time, serverName, databaseName string) bool {
	for _, file := range files {
		if samePeriod(file.Name, tier, ts, serverName, databaseName) {
			return true
		}
	}
	return false
}

func samePeriod(filename string, tier Tier, ts time.Time, serverName, databaseName string) bool {
	fileTime, ok := parseManagedTimestamp(filename, serverName, databaseName)
	if !ok {
		return false
	}
	switch tier {
	case TierWeekly:
		y1, w1 := fileTime.ISOWeek()
		y2, w2 := ts.ISOWeek()
		return y1 == y2 && w1 == w2
	case TierMonthly:
		return fileTime.Year() == ts.Year() && fileTime.Month() == ts.Month()
	default:
		return false
	}
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func parseManagedTimestamp(filename, serverName, databaseName string) (time.Time, bool) {
	prefix := serverName + "_" + databaseName + "_"
	if !strings.HasPrefix(filename, prefix) {
		return time.Time{}, false
	}
	suffix := strings.TrimPrefix(filename, prefix)
	if !managedFilenamePattern.MatchString(suffix) {
		return time.Time{}, false
	}
	tsPart := strings.TrimSuffix(suffix, ".dump.gz")
	ts, err := time.Parse("2006-01-02_15-04-05", tsPart)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
