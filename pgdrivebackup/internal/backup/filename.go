package backup

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var managedFilenamePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.dump\.gz$`)

func BuildFilename(serverName, databaseName string, ts time.Time) string {
	return fmt.Sprintf("%s_%s_%s.dump.gz", serverName, databaseName, ts.Format("2006-01-02_15-04-05"))
}

func ParseManagedTimestamp(filename, serverName, databaseName string) (time.Time, bool) {
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
