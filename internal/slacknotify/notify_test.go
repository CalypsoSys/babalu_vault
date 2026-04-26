package slacknotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMessageUsesPayloadValues(t *testing.T) {
	body, err := BuildMessage(`{"type":"review","cwd":"/tmp/work","last-assistant-message":"All done."}`)
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	want := "*Codex:* 'review'\nAll done.\n\n*Dir:* '/tmp/work'"
	if msg.Text != want {
		t.Fatalf("unexpected message text:\nwant %q\ngot  %q", want, msg.Text)
	}
}

func TestBuildMessageFallsBackForInvalidJSON(t *testing.T) {
	body, err := BuildMessage("{")
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if msg.Text != defaultAttentionText {
		t.Fatalf("expected fallback text %q, got %q", defaultAttentionText, msg.Text)
	}
}

func TestBuildMessageTruncatesLongAssistantMessage(t *testing.T) {
	long := strings.Repeat("a", maxLastMessageRunes+5)
	body, err := BuildMessage(`{"last-assistant-message":"` + long + `"}`)
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}

	var msg slackMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if !strings.Contains(msg.Text, strings.Repeat("a", maxLastMessageRunes)+"...") {
		t.Fatalf("expected truncated text, got %q", msg.Text)
	}
}

func TestLoadEnvFileSupportsExportSyntaxAndQuotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "\n# comment\nexport SLACK_CODEX_WEBHOOK_URL=\"https://example.test/hook\"\nPLAIN_VALUE='hello world'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SLACK_CODEX_WEBHOOK_URL", "")
	t.Setenv("PLAIN_VALUE", "")

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}

	if got := os.Getenv("SLACK_CODEX_WEBHOOK_URL"); got != "https://example.test/hook" {
		t.Fatalf("unexpected webhook env %q", got)
	}
	if got := os.Getenv("PLAIN_VALUE"); got != "hello world" {
		t.Fatalf("unexpected plain env %q", got)
	}
}

func TestNotifySkipsWhenWebhookMissing(t *testing.T) {
	t.Setenv(webhookEnvName, "")

	client := Client{
		HTTPClient: http.DefaultClient,
		EnvPath:    filepath.Join(t.TempDir(), "missing"),
	}
	if err := client.Notify(context.Background(), `{"type":"done"}`); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
}

func TestNotifyPostsBuiltMessage(t *testing.T) {
	var gotRequest *http.Request
	var gotBody slackMessage

	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotRequest = req
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	t.Setenv(webhookEnvName, "https://example.test/webhook")

	client := Client{HTTPClient: httpClient, EnvPath: filepath.Join(t.TempDir(), "missing")}
	if err := client.Notify(context.Background(), `{"type":"complete","last-assistant-message":"Ready"}`); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	if gotRequest == nil {
		t.Fatal("expected request to be sent")
	}
	if gotRequest.Method != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotRequest.Method)
	}
	if gotRequest.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected application/json content type, got %q", gotRequest.Header.Get("Content-Type"))
	}
	if gotBody.Text != "*Codex:* 'complete'\nReady" {
		t.Fatalf("unexpected posted text %q", gotBody.Text)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
