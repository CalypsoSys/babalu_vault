package main

import (
	"context"
	"fmt"
	"os"

	"github.com/CalypsoSys/babalu_vault/internal/slacknotify"
)

func main() {
	payload := "{}"
	if len(os.Args) > 1 {
		payload = os.Args[1]
	}

	client := slacknotify.Client{}
	if err := client.Notify(context.Background(), payload); err != nil {
		fmt.Fprintf(os.Stderr, "codex-slack-notify: %v\n", err)
		os.Exit(1)
	}
}
