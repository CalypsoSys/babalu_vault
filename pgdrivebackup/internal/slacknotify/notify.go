package slacknotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	defaultAttentionText = "Codex needs attention."
	defaultLastMessage   = "Codex turn complete / needs attention."
	maxLastMessageRunes  = 900
	webhookEnvName       = "SLACK_CODEX_WEBHOOK_URL"
)

type payload struct {
	EventType            string `json:"type"`
	CWD                  string `json:"cwd"`
	LastAssistantMessage string `json:"last-assistant-message"`
}

type slackMessage struct {
	Text string `json:"text"`
}

type Client struct {
	HTTPClient *http.Client
	EnvPath    string
}

func (c Client) Notify(ctx context.Context, rawPayload string) error {
	if err := c.loadSecretsEnv(); err != nil {
		return err
	}

	webhookURL := os.Getenv(webhookEnvName)
	if webhookURL == "" {
		return nil
	}

	body, err := BuildMessage(rawPayload)
	if err != nil {
		return err
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post to slack webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook returned %s", resp.Status)
	}

	return nil
}

func BuildMessage(rawPayload string) ([]byte, error) {
	msg := slackMessage{Text: defaultAttentionText}

	var in payload
	if err := json.Unmarshal([]byte(rawPayload), &in); err == nil {
		eventType := in.EventType
		if eventType == "" {
			eventType = "codex"
		}

		last := in.LastAssistantMessage
		if last == "" {
			last = defaultLastMessage
		}
		last = truncateRunes(last, maxLastMessageRunes)

		text := fmt.Sprintf("*Codex:* '%s'\n%s", eventType, last)
		if in.CWD != "" {
			text += fmt.Sprintf("\n\n*Dir:* '%s'", in.CWD)
		}
		msg.Text = text
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal slack message: %w", err)
	}
	return body, nil
}

func (c Client) loadSecretsEnv() error {
	envPath := c.EnvPath
	if envPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory: %w", err)
		}
		envPath = filepath.Join(homeDir, ".config", "secrets", "env")
	}

	if _, err := os.Stat(envPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat secrets env file: %w", err)
	}

	return LoadEnvFile(envPath)
}

func LoadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read env file: %w", err)
	}

	for lineNumber, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "export ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		}

		name, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			return fmt.Errorf("parse env file %s line %d: expected KEY=VALUE", path, lineNumber+1)
		}

		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			return fmt.Errorf("parse env file %s line %d: missing variable name", path, lineNumber+1)
		}

		if len(value) >= 2 {
			if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
				value = value[1 : len(value)-1]
			} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
				value = value[1 : len(value)-1]
			}
		}

		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set env %s: %w", name, err)
		}
	}

	return nil
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}

	var b strings.Builder
	b.Grow(len(value))
	count := 0
	for _, r := range value {
		if count == limit {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteString("...")
	return b.String()
}
