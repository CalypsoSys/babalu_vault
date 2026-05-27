package backup

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/retention"
)

var managedFilenamePattern = regexp.MustCompile(`^(daily|weekly|monthly)_.+_.+_\d{4}-\d{2}-\d{2}(\.sql|\.tar)?\.gz$`)

func BuildFilename(tier retention.Tier, groupName, targetName string, ts time.Time, extensions ...string) string {
	extension := ".gz"
	if len(extensions) > 0 && extensions[0] != "" {
		extension = extensions[0]
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", tier, groupName, targetName, ts.Format("2006-01-02"), extension)
}

func ParseManagedTimestamp(filename, groupName, targetName string) (time.Time, bool) {
	prefix := string(retention.TierDaily) + "_" + groupName + "_" + targetName + "_"
	if !strings.HasPrefix(filename, prefix) {
		return time.Time{}, false
	}
	suffix := strings.TrimPrefix(filename, prefix)
	if !managedFilenamePattern.MatchString(filename) {
		return time.Time{}, false
	}
	tsPart := trimManagedExtension(suffix)
	ts, err := time.Parse("2006-01-02", tsPart)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func trimManagedExtension(value string) string {
	for _, extension := range []string{".sql.gz", ".tar.gz", ".gz"} {
		if strings.HasSuffix(value, extension) {
			return strings.TrimSuffix(value, extension)
		}
	}
	return value
}
