package backup

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSanitizeSensitiveStringMasksPasswordForms(t *testing.T) {
	input := "PGPASSWORD=supersecret password=hunter2 password: swordfish token=abc123 secret=xyz"
	got := sanitizeSensitiveString(input)

	if got == input {
		t.Fatal("expected sensitive values to be masked")
	}
	if containsAny(got, []string{"supersecret", "hunter2", "swordfish", "abc123", "xyz"}) {
		t.Fatalf("expected secrets to be removed, got %q", got)
	}
}

func TestFormatOperationMessageMasksErrorAndCommandValues(t *testing.T) {
	msg := formatOperationMessage(
		"starting backup command",
		slog.String("command", "docker exec -e PGPASSWORD=supersecret db pg_dump app"),
		slog.Any("error", errors.New("connection failed password=hunter2")),
	)

	if containsAny(msg, []string{"supersecret", "hunter2"}) {
		t.Fatalf("expected formatted operation message to redact secrets, got %q", msg)
	}
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
