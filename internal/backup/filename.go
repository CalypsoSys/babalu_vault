package backup

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/retention"
)

var managedFilenamePattern = regexp.MustCompile(`^(daily|weekly|monthly)_.+_.+_\d{4}-\d{2}-\d{2}\.gz$`)

func BuildFilename(tier retention.Tier, serverName, databaseName string, ts time.Time) string {
	return fmt.Sprintf("%s_%s_%s_%s.gz", tier, serverName, databaseName, ts.Format("2006-01-02"))
}

func ParseManagedTimestamp(filename, serverName, databaseName string) (time.Time, bool) {
	prefix := string(retention.TierDaily) + "_" + serverName + "_" + databaseName + "_"
	if !strings.HasPrefix(filename, prefix) {
		return time.Time{}, false
	}
	suffix := strings.TrimPrefix(filename, prefix)
	if !managedFilenamePattern.MatchString(filename) {
		return time.Time{}, false
	}
	tsPart := strings.TrimSuffix(suffix, ".gz")
	ts, err := time.Parse("2006-01-02", tsPart)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
