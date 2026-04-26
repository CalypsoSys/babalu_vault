package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CalypsoSys/babalu_vault/internal/slacknotify"
)

func main() {
	var (
		logPath               string
		permissionRequestHook bool
	)

	flag.StringVar(&logPath, "log-path", "", "append every inbound payload to this JSONL file before notifying")
	flag.BoolVar(&permissionRequestHook, "permission-request-hook", false, "read a PermissionRequest hook payload from stdin and send it as an approval Slack alert")
	flag.Parse()

	mode := "notify"
	if permissionRequestHook || strings.Contains(filepath.Base(os.Args[0]), "permission-request") {
		mode = "permission-request-hook"
	}
	if logPath == "" && mode == "permission-request-hook" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "codex-slack-notify: resolve home directory: %v\n", err)
			os.Exit(1)
		}
		logPath = filepath.Join(homeDir, ".codex", "permission-request-hooks.jsonl")
	}

	payload := "{}"
	switch mode {
	case "permission-request-hook":
		rawHook, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codex-slack-notify: read permission hook payload: %v\n", err)
			os.Exit(1)
		}
		if err := slacknotify.AppendJSONL(logPath, string(rawHook), mode); err != nil {
			fmt.Fprintf(os.Stderr, "codex-slack-notify: %v\n", err)
			os.Exit(1)
		}
		payload, err = slacknotify.PermissionRequestPayload(string(rawHook))
		if err != nil {
			fmt.Fprintf(os.Stderr, "codex-slack-notify: %v\n", err)
			os.Exit(1)
		}
	default:
		if flag.NArg() > 0 {
			payload = flag.Arg(0)
		}
		if err := slacknotify.AppendJSONL(logPath, payload, mode); err != nil {
			fmt.Fprintf(os.Stderr, "codex-slack-notify: %v\n", err)
			os.Exit(1)
		}
	}

	client := slacknotify.Client{}
	if err := client.Notify(context.Background(), payload); err != nil {
		fmt.Fprintf(os.Stderr, "codex-slack-notify: %v\n", err)
		os.Exit(1)
	}
}
