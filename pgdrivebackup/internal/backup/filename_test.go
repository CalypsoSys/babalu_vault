package backup

import (
	"testing"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/retention"
)

func TestBuildAndParseManagedFilename(t *testing.T) {
	ts := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	name := BuildFilename(retention.TierDaily, "localdev", "ommadb_dev", ts)
	if name != "daily_localdev_ommadb_dev_2026-04-18.gz" {
		t.Fatalf("unexpected filename %q", name)
	}

	parsed, ok := ParseManagedTimestamp(name, "localdev", "ommadb_dev")
	if !ok {
		t.Fatal("expected filename to parse")
	}
	if parsed.Format("2006-01-02") != ts.Format("2006-01-02") {
		t.Fatalf("unexpected parsed time %v", parsed)
	}
}
