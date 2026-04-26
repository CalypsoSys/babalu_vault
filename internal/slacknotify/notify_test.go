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

func TestShouldNotifyForApprovalRequestedEvent(t *testing.T) {
	shouldNotify, parsed := ShouldNotify(`{"type":"approval-requested","last-assistant-message":"Do you want me to commit this?"}`)
	if !parsed {
		t.Fatal("expected payload to parse")
	}
	if !shouldNotify {
		t.Fatal("expected approval-requested payload to notify")
	}
}

func TestShouldNotifySkipsTurnCompleteEvent(t *testing.T) {
	shouldNotify, parsed := ShouldNotify(`{"type":"agent-turn-complete","last-assistant-message":"Done."}`)
	if !parsed {
		t.Fatal("expected payload to parse")
	}
	if shouldNotify {
		t.Fatal("expected turn-complete payload to be skipped")
	}
}

func TestShouldNotifyForTurnCompleteQuestion(t *testing.T) {
	shouldNotify, parsed := ShouldNotify(`{"type":"agent-turn-complete","last-assistant-message":"Do you want me to run the refinement sweep around the new 8/21 momentum leader?"}`)
	if !parsed {
		t.Fatal("expected payload to parse")
	}
	if !shouldNotify {
		t.Fatal("expected turn-complete question payload to notify")
	}
}

func TestShouldNotifyForQuestionMessage(t *testing.T) {
	shouldNotify, parsed := ShouldNotify(`{"type":"agent_message","last-assistant-message":"How should I handle the migration?"}`)
	if !parsed {
		t.Fatal("expected payload to parse")
	}
	if !shouldNotify {
		t.Fatal("expected question payload to notify")
	}
}

func TestShouldNotifySkipsNonQuestionAgentMessage(t *testing.T) {
	shouldNotify, parsed := ShouldNotify(`{"type":"agent_message","last-assistant-message":"I am updating the docs now."}`)
	if !parsed {
		t.Fatal("expected payload to parse")
	}
	if shouldNotify {
		t.Fatal("expected non-question agent message to be skipped")
	}
}

func TestShouldNotifySkipsInvalidJSON(t *testing.T) {
	shouldNotify, parsed := ShouldNotify("{")
	if parsed {
		t.Fatal("expected invalid payload to fail parsing")
	}
	if shouldNotify {
		t.Fatal("expected invalid payload to be skipped")
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

func TestNotifyPostsQuestionMessage(t *testing.T) {
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
	if err := client.Notify(context.Background(), `{"type":"agent_message","last-assistant-message":"How should I proceed?"}`); err != nil {
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
	if gotBody.Text != "*Codex:* 'agent_message'\nHow should I proceed?" {
		t.Fatalf("unexpected posted text %q", gotBody.Text)
	}
}

func TestNotifySkipsNonBlockingMessage(t *testing.T) {
	called := false
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
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
	if err := client.Notify(context.Background(), `{"type":"agent-turn-complete","last-assistant-message":"Done."}`); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if called {
		t.Fatal("expected no webhook request for non-blocking message")
	}
}

func TestNotifyPostsTurnCompleteQuestionMessage(t *testing.T) {
	var gotBody slackMessage
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
	err := client.Notify(context.Background(), `{"type":"agent-turn-complete","last-assistant-message":"Do you want me to create the commit for the shared backend error-log and config cleanup changes?"}`)
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	if gotBody.Text != "*Codex:* 'agent-turn-complete'\nDo you want me to create the commit for the shared backend error-log and config cleanup changes?" {
		t.Fatalf("unexpected posted text %q", gotBody.Text)
	}
}

func TestNotifyPostsApprovalRequestedMessage(t *testing.T) {
	var gotBody slackMessage
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
	err := client.Notify(context.Background(), `{"type":"approval-requested","last-assistant-message":"Do you want me to commit this?"}`)
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	if gotBody.Text != "*Codex:* 'approval-requested'\nDo you want me to commit this?" {
		t.Fatalf("unexpected posted text %q", gotBody.Text)
	}
}

func TestPermissionRequestPayloadUsesReasonAndCommand(t *testing.T) {
	got, err := PermissionRequestPayload(`{"cwd":"/tmp/work","reason":"Codex needs approval.","tool_name":"Bash","tool_input":{"command":"git commit -m \"hi\"","description":"ignored"}}`)
	if err != nil {
		t.Fatalf("PermissionRequestPayload() error = %v", err)
	}

	var payload payload
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if payload.EventType != "approval-requested" {
		t.Fatalf("unexpected event type %q", payload.EventType)
	}
	if payload.CWD != "/tmp/work" {
		t.Fatalf("unexpected cwd %q", payload.CWD)
	}
	if !strings.Contains(payload.LastAssistantMessage, "Codex needs approval.") {
		t.Fatalf("unexpected last message %q", payload.LastAssistantMessage)
	}
	if !strings.Contains(payload.LastAssistantMessage, "Tool: Bash") {
		t.Fatalf("unexpected last message %q", payload.LastAssistantMessage)
	}
	if !strings.Contains(payload.LastAssistantMessage, "Command: git commit -m \"hi\"") {
		t.Fatalf("unexpected last message %q", payload.LastAssistantMessage)
	}
}

func TestPermissionRequestPayloadFallsBackToDescription(t *testing.T) {
	got, err := PermissionRequestPayload(`{"cwd":"/tmp/work","tool_name":"Shell","tool_input":{"description":"Do you want me to continue?"}}`)
	if err != nil {
		t.Fatalf("PermissionRequestPayload() error = %v", err)
	}

	var payload payload
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if !strings.Contains(payload.LastAssistantMessage, "Do you want me to continue?") {
		t.Fatalf("unexpected last message %q", payload.LastAssistantMessage)
	}
	if !strings.Contains(payload.LastAssistantMessage, "Tool: Shell") {
		t.Fatalf("unexpected last message %q", payload.LastAssistantMessage)
	}
}

func TestAppendJSONLWritesModeAndPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.jsonl")
	if err := AppendJSONL(path, `{"type":"agent-turn-complete","last-assistant-message":"Done?"}`, "notify"); err != nil {
		t.Fatalf("AppendJSONL() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if got["_mode"] != "notify" {
		t.Fatalf("unexpected mode %v", got["_mode"])
	}

	payloadAny, ok := got["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload object, got %T", got["payload"])
	}
	if payloadAny["type"] != "agent-turn-complete" {
		t.Fatalf("unexpected payload type %v", payloadAny["type"])
	}
	if _, ok := got["_ts"]; !ok {
		t.Fatal("expected timestamp field")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
