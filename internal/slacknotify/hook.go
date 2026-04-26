package slacknotify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type permissionRequestHook struct {
	CWD       string `json:"cwd"`
	Reason    string `json:"reason"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	} `json:"tool_input"`
}

func PermissionRequestPayload(rawHook string) (string, error) {
	var hook permissionRequestHook
	if err := json.Unmarshal([]byte(rawHook), &hook); err != nil {
		return "", fmt.Errorf("parse permission request hook payload: %w", err)
	}

	last := strings.TrimSpace(hook.Reason)
	if last == "" {
		last = strings.TrimSpace(hook.ToolInput.Description)
	}
	if last == "" {
		last = "Codex is requesting approval."
	}

	toolName := strings.TrimSpace(hook.ToolName)
	if toolName == "" {
		toolName = "tool"
	}

	if command := strings.TrimSpace(hook.ToolInput.Command); command != "" {
		last += fmt.Sprintf("\n\nTool: %s\nCommand: %s", toolName, command)
	} else {
		last += fmt.Sprintf("\n\nTool: %s", toolName)
	}

	body, err := json.Marshal(payload{
		EventType:            "approval-requested",
		CWD:                  hook.CWD,
		LastAssistantMessage: last,
	})
	if err != nil {
		return "", fmt.Errorf("marshal permission request payload: %w", err)
	}

	return string(body), nil
}

func AppendJSONL(path string, raw string, mode string) error {
	if path == "" {
		return nil
	}

	record := map[string]any{
		"_ts": time.Now().Format(time.RFC3339Nano),
	}
	if mode != "" {
		record["_mode"] = mode
	}

	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		record["_parse_error"] = err.Error()
		record["_raw"] = raw
	} else {
		record["payload"] = parsed
	}

	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal log record: %w", err)
	}
	line = append(line, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write log record: %w", err)
	}

	return nil
}
