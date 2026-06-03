package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/config"
)

const sanityReportExtension = ".report.txt"

type compiledSanityPattern struct {
	name  string
	regex *regexp.Regexp
}

type StatusCount struct {
	Status string
	Count  int
}

type SourceIPCount struct {
	IP    string
	Count int
}

type SanityFinding struct {
	Server           string
	Backup           string
	Target           string
	Pattern          string
	Count            int
	HTTPStatusCounts []StatusCount
	TopSourceIPs     []SourceIPCount
	ReportPath       string
}

type SanityReportSummary struct {
	Server            string
	Backup            string
	Target            string
	SourcePath        string
	Date              time.Time
	ArchiveSizeBytes  int64
	ArchivedFileCount int
	ScannedFileCount  int
	SkippedFileCount  int
	ScanRotated       bool
	TotalMatchedLines int
	HTTPStatusCounts  []StatusCount
	TopSourceIPs      []SourceIPCount
	Findings          []SanityFinding
	ReportPath        string
}

type sanityPatternCount struct {
	Name         string
	Count        int
	statusCounts map[string]int
	ipCounts     map[string]int
}

type sanityReport struct {
	Server            string
	Backup            string
	Target            string
	SourcePath        string
	Date              time.Time
	ArchiveSizeBytes  int64
	ArchivedFileCount int
	ScannedFileCount  int
	SkippedFileCount  int
	ScanRotated       bool
	PatternCounts     []sanityPatternCount
	TotalMatchedLines int
	HTTPStatusCounts  []StatusCount
	TopSourceIPs      []SourceIPCount
}

var (
	rotatedNumberPattern = regexp.MustCompile(`\.\d+$`)
	rotatedDatePattern   = regexp.MustCompile(`(?:\.|-)\d{4}-\d{2}-\d{2}$`)
	sourceIPPattern      = regexp.MustCompile(`^((?:\d{1,3}\.){3}\d{1,3}|[0-9A-Fa-f:]{2,})\s+`)
	httpStatusPattern    = regexp.MustCompile(`"\s+(\d{3})(?:\s|$)`)
)

func buildSanityReportFromArchive(item config.SelectedTarget, archivePath string, archiveSizeBytes int64, now time.Time) (sanityReport, error) {
	patterns, err := compileSanityPatterns(item.Target.SanityChecks.Patterns)
	if err != nil {
		return sanityReport{}, err
	}

	report := sanityReport{
		Server:           item.Server.Name,
		Backup:           item.Backup.Name,
		Target:           item.Target.Name,
		SourcePath:       item.Target.Path,
		Date:             now,
		ArchiveSizeBytes: archiveSizeBytes,
		ScanRotated:      item.Target.SanityChecks.ScanRotated,
		PatternCounts:    make([]sanityPatternCount, len(patterns)),
	}
	for i, pattern := range patterns {
		report.PatternCounts[i] = sanityPatternCount{
			Name:         pattern.name,
			statusCounts: make(map[string]int),
			ipCounts:     make(map[string]int),
		}
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return sanityReport{}, fmt.Errorf("open file archive for sanity report: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return sanityReport{}, fmt.Errorf("open gzip archive for sanity report: %w", err)
	}
	defer gzipReader.Close()

	ipCounts := make(map[string]int)
	statusCounts := make(map[string]int)
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sanityReport{}, fmt.Errorf("read tar archive for sanity report: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}

		report.ArchivedFileCount++
		if !report.ScanRotated && shouldSkipSanityLog(header.Name) {
			report.SkippedFileCount++
			continue
		}

		report.ScannedFileCount++
		if err := scanSanityLog(tarReader, patterns, &report, ipCounts, statusCounts); err != nil {
			return sanityReport{}, fmt.Errorf("scan %s for sanity report: %w", header.Name, err)
		}
	}

	report.HTTPStatusCounts = sortedStatusCounts(statusCounts)
	report.TopSourceIPs = topSourceIPs(ipCounts, 5)
	return report, nil
}

func writeSanityReport(reportPath string, report sanityReport) error {
	if err := os.WriteFile(reportPath, []byte(renderSanityReport(report)), 0o600); err != nil {
		return fmt.Errorf("write sanity report: %w", err)
	}
	return nil
}

func compileSanityPatterns(patterns []config.SanityPatternConfig) ([]compiledSanityPattern, error) {
	compiled := make([]compiledSanityPattern, 0, len(patterns))
	for _, pattern := range patterns {
		name := strings.TrimSpace(pattern.Name)
		match := strings.TrimSpace(pattern.Match)
		regex, err := regexp.Compile("(?i)" + match)
		if err != nil {
			return nil, fmt.Errorf("compile sanity pattern %q: %w", name, err)
		}
		compiled = append(compiled, compiledSanityPattern{
			name:  name,
			regex: regex,
		})
	}
	return compiled, nil
}

func scanSanityLog(reader io.Reader, patterns []compiledSanityPattern, report *sanityReport, ipCounts map[string]int, statusCounts map[string]int) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		matched := false
		status := httpStatusBucket(line)
		sourceIP := sourceIPFromLine(line)
		for i, pattern := range patterns {
			if pattern.regex.MatchString(line) {
				report.PatternCounts[i].Count++
				if status != "" {
					report.PatternCounts[i].statusCounts[status]++
				}
				if sourceIP != "" {
					report.PatternCounts[i].ipCounts[sourceIP]++
				}
				matched = true
			}
		}
		if !matched {
			continue
		}
		report.TotalMatchedLines++
		if status != "" {
			statusCounts[status]++
		}
		if sourceIP != "" {
			ipCounts[sourceIP]++
		}
	}
	return scanner.Err()
}

func sourceIPFromLine(line string) string {
	matches := sourceIPPattern.FindStringSubmatch(line)
	if matches == nil {
		return ""
	}
	return matches[1]
}

func httpStatusBucket(line string) string {
	matches := httpStatusPattern.FindStringSubmatch(line)
	if matches == nil {
		return ""
	}
	status := matches[1]
	if strings.HasPrefix(status, "5") {
		return "50x"
	}
	return status
}

func topSourceIPs(counts map[string]int, limit int) []SourceIPCount {
	if len(counts) == 0 || limit <= 0 {
		return nil
	}
	top := make([]SourceIPCount, 0, len(counts))
	for ip, count := range counts {
		top = append(top, SourceIPCount{IP: ip, Count: count})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Count == top[j].Count {
			return top[i].IP < top[j].IP
		}
		return top[i].Count > top[j].Count
	})
	if len(top) > limit {
		top = top[:limit]
	}
	return top
}

func sortedStatusCounts(counts map[string]int) []StatusCount {
	if len(counts) == 0 {
		return nil
	}
	statuses := make([]StatusCount, 0, len(counts))
	for status, count := range counts {
		statuses = append(statuses, StatusCount{Status: status, Count: count})
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statusSortValue(statuses[i].Status) < statusSortValue(statuses[j].Status)
	})
	return statuses
}

func statusSortValue(status string) int {
	if status == "50x" {
		return 500
	}
	n := 900
	if _, err := fmt.Sscanf(status, "%d", &n); err != nil {
		return 900
	}
	return n
}

func findingsFromSanityReport(report sanityReport, reportPath string) []SanityFinding {
	var findings []SanityFinding
	for _, count := range report.PatternCounts {
		if count.Count == 0 {
			continue
		}
		findings = append(findings, SanityFinding{
			Server:           report.Server,
			Backup:           report.Backup,
			Target:           report.Target,
			Pattern:          count.Name,
			Count:            count.Count,
			HTTPStatusCounts: sortedStatusCounts(count.statusCounts),
			TopSourceIPs:     topSourceIPs(count.ipCounts, 5),
			ReportPath:       reportPath,
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Count == findings[j].Count {
			return findings[i].Pattern < findings[j].Pattern
		}
		return findings[i].Count > findings[j].Count
	})
	return findings
}

func summaryFromSanityReport(report sanityReport, reportPath string) SanityReportSummary {
	return SanityReportSummary{
		Server:            report.Server,
		Backup:            report.Backup,
		Target:            report.Target,
		SourcePath:        report.SourcePath,
		Date:              report.Date,
		ArchiveSizeBytes:  report.ArchiveSizeBytes,
		ArchivedFileCount: report.ArchivedFileCount,
		ScannedFileCount:  report.ScannedFileCount,
		SkippedFileCount:  report.SkippedFileCount,
		ScanRotated:       report.ScanRotated,
		TotalMatchedLines: report.TotalMatchedLines,
		HTTPStatusCounts:  report.HTTPStatusCounts,
		TopSourceIPs:      report.TopSourceIPs,
		Findings:          findingsFromSanityReport(report, reportPath),
		ReportPath:        reportPath,
	}
}

func shouldSkipSanityLog(name string) bool {
	baseName := strings.ToLower(path.Base(strings.TrimPrefix(name, "./")))
	for _, extension := range []string{".tar.gz", ".gz", ".zip", ".tar", ".tgz", ".bz2", ".xz"} {
		if strings.HasSuffix(baseName, extension) {
			return true
		}
	}
	return rotatedNumberPattern.MatchString(baseName) || rotatedDatePattern.MatchString(baseName)
}

func renderSanityReport(report sanityReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Server: %s\n", report.Server)
	fmt.Fprintf(&out, "Backup: %s\n", report.Backup)
	fmt.Fprintf(&out, "Target: %s\n", report.Target)
	fmt.Fprintf(&out, "Path: %s\n", report.SourcePath)
	fmt.Fprintf(&out, "Date: %s\n\n", report.Date.UTC().Format("2006-01-02"))
	fmt.Fprintf(&out, "Log files archived: %d\n", report.ArchivedFileCount)
	fmt.Fprintf(&out, "Archive size: %s\n", formatReportBytes(report.ArchiveSizeBytes))
	fmt.Fprintf(&out, "Log files scanned: %d\n", report.ScannedFileCount)
	fmt.Fprintf(&out, "Rotated/compressed logs skipped: %d\n", report.SkippedFileCount)
	fmt.Fprintf(&out, "Scan rotated logs: %t\n\n", report.ScanRotated)
	if len(report.HTTPStatusCounts) > 0 {
		fmt.Fprintln(&out, "HTTP status counts:")
		for _, count := range report.HTTPStatusCounts {
			fmt.Fprintf(&out, "  %s: %d\n", count.Status, count.Count)
		}
		fmt.Fprintln(&out)
	}
	fmt.Fprintln(&out, "Pattern counts:")
	if len(report.PatternCounts) == 0 {
		fmt.Fprintln(&out, "  none configured")
	} else {
		for _, count := range report.PatternCounts {
			fmt.Fprintf(&out, "  %s: %d\n", count.Name, count.Count)
			for _, status := range sortedStatusCounts(count.statusCounts) {
				fmt.Fprintf(&out, "    %s: %d\n", status.Status, status.Count)
			}
		}
	}
	fmt.Fprintf(&out, "\nTotal matched lines: %d\n", report.TotalMatchedLines)
	if len(report.TopSourceIPs) > 0 {
		fmt.Fprintln(&out, "\nTop source IPs:")
		for _, count := range report.TopSourceIPs {
			fmt.Fprintf(&out, "  %s: %d\n", count.IP, count.Count)
		}
	}
	return out.String()
}

func formatReportBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	for _, suffix := range []string{"K", "M", "G", "T"} {
		value /= unit
		if value < unit {
			if value >= 10 {
				return fmt.Sprintf("%.0f%s", value, suffix)
			}
			formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
			return formatted + suffix
		}
	}
	return fmt.Sprintf("%.0fP", value/unit)
}
