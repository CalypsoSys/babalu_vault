package backup

import (
	"testing"
	"time"
)

func TestBuildAndParseManagedFilename(t *testing.T) {
	ts := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	name := BuildFilename("localdev", "ommadb_dev", ts)
	if name != "localdev_ommadb_dev_2026-04-18_02-00-00.sql.gz" {
		t.Fatalf("unexpected filename %q", name)
	}

	parsed, ok := ParseManagedTimestamp(name, "localdev", "ommadb_dev")
	if !ok {
		t.Fatal("expected filename to parse")
	}
	if !parsed.Equal(ts) {
		t.Fatalf("unexpected parsed time %v", parsed)
	}
}
