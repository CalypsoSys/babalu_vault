package backup

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/config"
)

func TestBuildSanityReportFromArchiveSkipsRotatedLogsByDefault(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "logs.tar.gz")
	writeTestTarGzip(t, archivePath, map[string]string{
		"./access.log": strings.Join([]string{
			`203.0.113.10 - - [03/Jun/2026:02:00:00 +0000] "GET /.env HTTP/1.1" 404 11`,
			`203.0.113.10 - - [03/Jun/2026:02:00:01 +0000] "GET /api/.env HTTP/1.1" 200 11`,
			`198.51.100.5 - - [03/Jun/2026:02:00:02 +0000] "GET /health HTTP/1.1" 500 11`,
			"panic: permission denied",
			"",
		}, "\n"),
		"./access.log.1": `192.0.2.8 - - [03/Jun/2026:01:00:00 +0000] "GET /.env HTTP/1.1" 500 11`,
		"./debug.log.gz": "fatal: compressed old log should be skipped",
	})
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	item := selectedLogFileSSH()
	report, err := buildSanityReportFromArchive(item, archivePath, info.Size(), time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildSanityReportFromArchive() error = %v", err)
	}

	if report.ArchivedFileCount != 3 {
		t.Fatalf("expected 3 archived files, got %d", report.ArchivedFileCount)
	}
	if report.ScannedFileCount != 1 || report.SkippedFileCount != 2 {
		t.Fatalf("expected 1 scanned and 2 skipped files, got scanned=%d skipped=%d", report.ScannedFileCount, report.SkippedFileCount)
	}
	if got := reportPatternCount(report, "/.env"); got != 2 {
		t.Fatalf("expected /.env count 2, got %d", got)
	}
	if got := reportPatternStatusCount(report, "/.env", "200"); got != 1 {
		t.Fatalf("expected /.env 200 count 1, got %d", got)
	}
	if got := reportPatternStatusCount(report, "/.env", "404"); got != 1 {
		t.Fatalf("expected /.env 404 count 1, got %d", got)
	}
	if got := reportPatternCount(report, "/api/.env"); got != 1 {
		t.Fatalf("expected /api/.env count 1, got %d", got)
	}
	if got := reportPatternStatusCount(report, "/api/.env", "200"); got != 1 {
		t.Fatalf("expected /api/.env 200 count 1, got %d", got)
	}
	if got := reportPatternCount(report, "500"); got != 1 {
		t.Fatalf("expected 500 count 1, got %d", got)
	}
	if got := reportPatternStatusCount(report, "500", "50x"); got != 1 {
		t.Fatalf("expected 500 50x count 1, got %d", got)
	}
	if got := reportPatternCount(report, "panic"); got != 1 {
		t.Fatalf("expected panic count 1, got %d", got)
	}
	if got := reportPatternCount(report, "permission denied"); got != 1 {
		t.Fatalf("expected permission denied count 1, got %d", got)
	}
	if report.TotalMatchedLines != 4 {
		t.Fatalf("expected 4 total matched lines, got %d", report.TotalMatchedLines)
	}
	if len(report.TopSourceIPs) != 2 || report.TopSourceIPs[0].IP != "203.0.113.10" || report.TopSourceIPs[0].Count != 2 {
		t.Fatalf("unexpected top source IPs %+v", report.TopSourceIPs)
	}
	if got := reportStatusCount(report, "200"); got != 1 {
		t.Fatalf("expected 200 count 1, got %d", got)
	}
	if got := reportStatusCount(report, "404"); got != 1 {
		t.Fatalf("expected 404 count 1, got %d", got)
	}
	if got := reportStatusCount(report, "50x"); got != 1 {
		t.Fatalf("expected 50x count 1, got %d", got)
	}

	findings := findingsFromSanityReport(report, "/tmp/report.txt")
	if len(findings) != 5 {
		t.Fatalf("expected 5 findings, got %+v", findings)
	}
	if findings[0].Pattern != "/.env" || findings[0].Count != 2 || findings[0].ReportPath != "/tmp/report.txt" {
		t.Fatalf("unexpected first finding %+v", findings[0])
	}
	if len(findings[0].TopSourceIPs) != 1 || findings[0].TopSourceIPs[0].IP != "203.0.113.10" || findings[0].TopSourceIPs[0].Count != 2 {
		t.Fatalf("unexpected first finding top IPs %+v", findings[0].TopSourceIPs)
	}

	rendered := renderSanityReport(report)
	for _, expected := range []string{
		"Server: hasimojoe",
		"Backup: hasimojoe-logs",
		"Target: srv-logs",
		"Path: /srv/logs",
		"Date: 2026-06-03",
		"Log files archived: 3",
		"Log files scanned: 1",
		"Rotated/compressed logs skipped: 2",
		"HTTP status counts:",
		"  200: 1",
		"  404: 1",
		"  50x: 1",
		"  /.env: 2",
		"    200: 1",
		"    404: 1",
		"Total matched lines: 4",
		"Top source IPs:",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected rendered report to contain %q, got:\n%s", expected, rendered)
		}
	}
}

func TestShouldSkipSanityLogRotatedArtifacts(t *testing.T) {
	tests := []struct {
		name string
		skip bool
	}{
		{name: "./access.log", skip: false},
		{name: "./access.log.1", skip: true},
		{name: "./access.log.12", skip: true},
		{name: "./access.log.2026-06-03", skip: true},
		{name: "./access-2026-06-03", skip: true},
		{name: "./access.log.gz", skip: true},
		{name: "./access.log.tar.gz", skip: true},
		{name: "./access.log.zip", skip: true},
		{name: "./access.log.tgz", skip: true},
		{name: "./access.log.bz2", skip: true},
		{name: "./access.log.xz", skip: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipSanityLog(tt.name); got != tt.skip {
				t.Fatalf("shouldSkipSanityLog(%q) = %t, want %t", tt.name, got, tt.skip)
			}
		})
	}
}

func selectedLogFileSSH() config.SelectedTarget {
	return config.SelectedTarget{
		Server: config.ServerConfig{Name: "hasimojoe", Type: "ssh", SSHTarget: "backup@hasimojoe"},
		Backup: config.BackupConfig{Name: "hasimojoe-logs", Server: "hasimojoe", Engine: "files"},
		Target: config.TargetConfig{
			Name: "srv-logs",
			Path: "/srv/logs",
			SanityChecks: config.SanityChecksConfig{
				Enabled: true,
				Patterns: []config.SanityPatternConfig{
					{Name: "/.env", Match: `/\.env`},
					{Name: "/api/.env", Match: `/api/\.env`},
					{Name: "500", Match: ` 500 `},
					{Name: "panic", Match: `panic`},
					{Name: "permission denied", Match: `permission denied`},
				},
			},
		},
	}
}

func reportPatternCount(report sanityReport, name string) int {
	for _, count := range report.PatternCounts {
		if count.Name == name {
			return count.Count
		}
	}
	return 0
}

func reportPatternStatusCount(report sanityReport, name, status string) int {
	for _, count := range report.PatternCounts {
		if count.Name == name {
			return count.statusCounts[status]
		}
	}
	return 0
}

func reportStatusCount(report sanityReport, status string) int {
	for _, count := range report.HTTPStatusCounts {
		if count.Status == status {
			return count.Count
		}
	}
	return 0
}

func writeTestTarGzip(t *testing.T, archivePath string, files map[string]string) {
	t.Helper()

	out, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(out)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		data := []byte(content)
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(data)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
