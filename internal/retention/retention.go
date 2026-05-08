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

	"github.com/CalypsoSys/babalu_vault/internal/config"
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

var managedFilenamePattern = regexp.MustCompile(`^(daily|weekly|monthly)_.+_.+_\d{4}-\d{2}-\d{2}\.gz$`)

func SelectExcess(files []LocalFile, keep int, tier Tier, serverName, databaseName string) []LocalFile {
	if keep < 0 {
		keep = 0
	}
	var managed []Candidate
	for _, file := range files {
		fileTier, ts, ok := parseManagedFile(file.Name, serverName, databaseName)
		if !ok {
			continue
		}
		if fileTier != tier {
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

	baseDir := filepath.Join(p.RootDir, serverName, databaseName)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("create retention directory for %s/%s: %w", serverName, databaseName, err)
	}

	dailyFiles, err := ListLocalFiles(baseDir)
	if err != nil {
		return fmt.Errorf("list daily files: %w", err)
	}
	weeklyFiles, err := ListLocalFiles(baseDir)
	if err != nil {
		return fmt.Errorf("list weekly files: %w", err)
	}
	monthlyFiles, err := ListLocalFiles(baseDir)
	if err != nil {
		return fmt.Errorf("list monthly files: %w", err)
	}

	if policy.WeeklyKeep > 0 {
		if err := p.ensurePeriodSnapshots(dailyFiles, &weeklyFiles, serverName, databaseName, now, TierDaily, TierWeekly, baseDir); err != nil {
			return err
		}
		if err := p.refreshExpiredSnapshot(dailyFiles, &weeklyFiles, serverName, databaseName, now, TierDaily, TierWeekly, baseDir); err != nil {
			return err
		}
	}
	if policy.MonthlyKeep > 0 {
		if err := p.refreshExpiredSnapshot(weeklyFiles, &monthlyFiles, serverName, databaseName, now, TierWeekly, TierMonthly, baseDir); err != nil {
			return err
		}
		if err := p.ensurePeriodSnapshots(dailyFiles, &monthlyFiles, serverName, databaseName, now, TierDaily, TierMonthly, baseDir); err != nil {
			return err
		}
	}

	dailyFiles, err = ListLocalFiles(baseDir)
	if err != nil {
		return fmt.Errorf("relist daily files: %w", err)
	}
	weeklyFiles, err = ListLocalFiles(baseDir)
	if err != nil {
		return fmt.Errorf("relist weekly files: %w", err)
	}
	monthlyFiles, err = ListLocalFiles(baseDir)
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
		excess := SelectExcess(item.files, item.keep, item.tier, serverName, databaseName)
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

func (p *Planner) ensurePeriodSnapshots(sourceFiles []LocalFile, destFiles *[]LocalFile, serverName, databaseName string, now time.Time, fromTier, toTier Tier, destDir string) error {
	candidates := managedCandidates(sourceFiles, fromTier, serverName, databaseName)
	for _, candidate := range candidates {
		if !isPastTierPeriod(candidate.Time, now, toTier) {
			continue
		}
		if hasPeriodAt(*destFiles, toTier, candidate.Time, serverName, databaseName) {
			continue
		}

		if err := p.promoteSnapshot(candidate, destFiles, serverName, databaseName, fromTier, toTier, destDir); err != nil {
			return err
		}
	}
	return nil
}

func (p *Planner) refreshExpiredSnapshot(sourceFiles []LocalFile, destFiles *[]LocalFile, serverName, databaseName string, now time.Time, fromTier, toTier Tier, destDir string) error {
	sources := managedCandidates(sourceFiles, fromTier, serverName, databaseName)
	if len(sources) == 0 {
		return nil
	}
	destinations := managedCandidates(*destFiles, toTier, serverName, databaseName)
	if len(destinations) == 0 {
		return nil
	}
	if !isExpiredTierSnapshot(destinations[0].Time, now, toTier) {
		return nil
	}
	if hasSnapshotAt(*destFiles, toTier, sources[0].Time, serverName, databaseName) {
		return nil
	}
	return p.promoteSnapshot(sources[0], destFiles, serverName, databaseName, fromTier, toTier, destDir)
}

func (p *Planner) promoteSnapshot(candidate Candidate, destFiles *[]LocalFile, serverName, databaseName string, fromTier, toTier Tier, destDir string) error {
	destName := buildManagedFilename(toTier, serverName, databaseName, candidate.Time)
	destPath := filepath.Join(destDir, destName)
	p.PromoteLog = append(p.PromoteLog, Promotion{
		From: fromTier,
		To:   toTier,
		File: LocalFile{Path: destPath, Name: destName},
	})
	if p.DryRun {
		*destFiles = append(*destFiles, LocalFile{Path: destPath, Name: destName})
		return nil
	}
	if err := copyFile(candidate.File.Path, destPath); err != nil {
		return fmt.Errorf("promote %s backup %s to %s: %w", fromTier, candidate.File.Name, toTier, err)
	}
	*destFiles = append(*destFiles, LocalFile{Path: destPath, Name: destName})
	return nil
}

func managedCandidates(files []LocalFile, tier Tier, serverName, databaseName string) []Candidate {
	var managed []Candidate
	for _, file := range files {
		fileTier, ts, ok := parseManagedFile(file.Name, serverName, databaseName)
		if !ok {
			continue
		}
		if fileTier != tier {
			continue
		}
		managed = append(managed, Candidate{File: file, Time: ts})
	}
	sort.Slice(managed, func(i, j int) bool {
		return managed[i].Time.After(managed[j].Time)
	})
	return managed
}

func hasSnapshotAt(files []LocalFile, tier Tier, ts time.Time, serverName, databaseName string) bool {
	for _, file := range files {
		fileTier, fileTime, ok := parseManagedFile(file.Name, serverName, databaseName)
		if !ok || fileTier != tier {
			continue
		}
		if fileTime.Equal(ts) {
			return true
		}
	}
	return false
}

func hasPeriodAt(files []LocalFile, tier Tier, ts time.Time, serverName, databaseName string) bool {
	for _, file := range files {
		if samePeriod(file.Name, tier, ts, serverName, databaseName) {
			return true
		}
	}
	return false
}

func isPastTierPeriod(candidateTime, now time.Time, tier Tier) bool {
	switch tier {
	case TierWeekly:
		cy, cw := candidateTime.ISOWeek()
		ny, nw := now.ISOWeek()
		return cy != ny || cw != nw
	case TierMonthly:
		return candidateTime.Year() != now.Year() || candidateTime.Month() != now.Month()
	default:
		return false
	}
}

func isExpiredTierSnapshot(snapshotTime, now time.Time, tier Tier) bool {
	switch tier {
	case TierWeekly:
		return !snapshotTime.AddDate(0, 0, 7).After(now)
	case TierMonthly:
		return !snapshotTime.AddDate(0, 1, 0).After(now)
	default:
		return false
	}
}

func samePeriod(filename string, tier Tier, ts time.Time, serverName, databaseName string) bool {
	fileTier, fileTime, ok := parseManagedFile(filename, serverName, databaseName)
	if !ok {
		return false
	}
	if fileTier != tier {
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

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
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
	return nil
}

func parseManagedFile(filename, serverName, databaseName string) (Tier, time.Time, bool) {
	for _, tier := range []Tier{TierDaily, TierWeekly, TierMonthly} {
		prefix := string(tier) + "_" + serverName + "_" + databaseName + "_"
		if !strings.HasPrefix(filename, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(filename, prefix)
		if !managedFilenamePattern.MatchString(filename) {
			return "", time.Time{}, false
		}
		tsPart := strings.TrimSuffix(suffix, ".gz")
		ts, err := time.Parse("2006-01-02", tsPart)
		if err != nil {
			return "", time.Time{}, false
		}
		return tier, ts, true
	}
	return "", time.Time{}, false
}

func buildManagedFilename(tier Tier, serverName, databaseName string, ts time.Time) string {
	return fmt.Sprintf("%s_%s_%s_%s.gz", tier, serverName, databaseName, ts.Format("2006-01-02"))
}
