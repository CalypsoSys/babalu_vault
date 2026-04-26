# codex-slack-notify

`codex-slack-notify` is a small Go executable that posts only the Codex alerts
that need your response to a Slack incoming webhook.

It accepts the Codex JSON payload as its first argument, looks for
`SLACK_CODEX_WEBHOOK_URL` in the environment, and also loads
`~/.config/secrets/env` when present so it still works when Codex starts from a
shell that did not source your profile.

The same binary can also be used as a Codex `PermissionRequest` hook. In that
mode it reads the hook JSON from stdin, converts it into an
`approval-requested` Slack payload, and posts it through the same webhook.

If the webhook variable is missing, it exits successfully without sending
anything. Invalid JSON payloads fall back to a generic `"Codex needs attention."`
Slack message.

## Build

```bash
go build ./cmd/codex-slack-notify
```

Or from the repo root:

```bash
make build-slack-notify
```

To also produce a Windows binary:

```bash
make build-slack-notify-windows
```

## Workflow

This notifier is meant for "Codex is waiting on you" phone alerts. It ignores
routine progress and ordinary completions, and it does not make Slack a two-way
Codex control surface.

```text
Codex in tmux -> codex-slack-notify -> Slack channel -> phone alert -> SSH back in -> tmux attach
```

## Setup

### 1. Store the Slack webhook secret

```bash
mkdir -p ~/.config/secrets
chmod 700 ~/.config/secrets
vi ~/.config/secrets/env
```

Put this in that file:

```bash
export SLACK_CODEX_WEBHOOK_URL='https://hooks.slack.com/services/XXX/YYY/ZZZ'
```

Lock it down:

```bash
chmod 600 ~/.config/secrets/env
```

Security note: treat the webhook URL like a password. Do not paste it into
GitHub, tickets, screenshots, prompts, or tracked config files.

### 2. Optionally load the secret in your shell

`codex-slack-notify` already reads `~/.config/secrets/env` directly, so this is
mostly for manual testing and convenience:

```bash
vi ~/.profile
```

Add:

```bash
# Load private environment secrets
if [ -f "$HOME/.config/secrets/env" ]; then
  . "$HOME/.config/secrets/env"
fi
```

Reload it:

```bash
source ~/.profile
echo "$SLACK_CODEX_WEBHOOK_URL"
```

### 3. Install the notifier somewhere stable

Linux/macOS:

```bash
mkdir -p ~/.local/bin
go build -o ~/.local/bin/codex-slack-notify ./cmd/codex-slack-notify
chmod +x ~/.local/bin/codex-slack-notify
```

Windows:

```powershell
go build -o $HOME\bin\codex-slack-notify.exe .\cmd\codex-slack-notify
```

### 4. Test it manually

```bash
~/.local/bin/codex-slack-notify '{"type":"test","last-assistant-message":"Slack test from codex-slack-notify","cwd":"/tmp"}'
```

Windows PowerShell:

```powershell
& "$HOME\bin\codex-slack-notify.exe" '{"type":"test","last-assistant-message":"Slack test from codex-slack-notify","cwd":"C:\\Temp"}'
```

You should see a message in your Slack channel when the payload looks like
Codex is waiting for your answer. On your phone, set that channel to notify you
for all new messages if you want reliable attention pings.

### 5. Configure Codex

Linux/macOS Codex config path:

```bash
mkdir -p ~/.codex
vi ~/.codex/config.toml
```

Windows Codex config path:

```text
C:\Users\YOURNAME\.codex\config.toml
```

Add or adjust:

```toml
notify = ["/home/YOURNAME/.local/bin/codex-slack-notify", "--log-path", "/home/YOURNAME/.codex/notify-invocations.jsonl"]

[tui]
notifications = ["approval-requested", "agent-turn-complete"]
notification_method = "auto"
notification_condition = "always"
```

Windows example:

```toml
notify = ["C:\\Users\\YOURNAME\\bin\\codex-slack-notify.exe", "--log-path", "C:\\Users\\YOURNAME\\.codex\\notify-invocations.jsonl"]

[tui]
notifications = ["approval-requested", "agent-turn-complete"]
notification_method = "auto"
notification_condition = "always"
```

What these settings do:

- `notify = ["~/.local/bin/codex-slack-notify", ...]` is the external hook.
- Use full absolute paths in `notify` and hook commands. Do not rely on `~`
  expansion inside Codex config arrays.
- `notify = ["/home/YOURNAME/.local/bin/codex-slack-notify", ...]` is the
  external hook.
  Codex runs that command, passes it a JSON payload, and the optional
  `--log-path` records every inbound payload before Slack delivery.
- `notify = ["C:\\Users\\YOURNAME\\bin\\codex-slack-notify.exe", ...]` does
  the same thing on Windows without any PowerShell wrapper.
- `notifications = ["approval-requested", "agent-turn-complete"]` configures
  the built-in TUI notification pipeline. It is separate from the external
  `notify` command.
- `notification_method = "auto"` lets Codex use its normal local notification
  mechanism for TUI alerts.
- `notification_condition = "always"` makes TUI notifications fire even when
  the terminal still has focus.

With current Codex behavior, these paths do different jobs:

- `notify` is the external program hook. Current Codex docs describe it as
  receiving supported notifications, currently `agent-turn-complete`.
- `[tui].notifications` controls local terminal notifications such as
  `approval-requested` and `agent-turn-complete`.

That means the Slack notifier should usually watch for "waiting on you"
messages inside `agent-turn-complete`, for example:

- approval-style prompts such as "Do you want me to run this command?"
- clarification or decision questions such as "How should I handle this?"
- explicit waiting text such as "Waiting for your answer"

## Optional: Log Every Notify Payload

If you want to prove exactly what Codex is invoking, keep the `--log-path`
argument in the `notify` array. The binary will append one JSON object per line
to that file, including a timestamp, mode, and the raw parsed payload Codex
handed it.

Then watch:

```bash
tail -f ~/.codex/notify-invocations.jsonl
```

## Optional: Slack Alerts For Real Permission Requests

For true permission prompts, current Codex docs point to Hooks rather than the
legacy `notify` setting. This same Go binary can be installed under a second
name, `codex-slack-permission-request`, and invoked directly from the
`PermissionRequest` hook. When the executable name contains
`permission-request`, it reads stdin, logs the hook payload, converts it into a
Slack-friendly `approval-requested` message, and posts that through the same
webhook.

Add to `~/.codex/config.toml`:

```toml
[features]
codex_hooks = true

[[hooks.PermissionRequest]]

[[hooks.PermissionRequest.hooks]]
type = "command"
command = "/home/joe/.local/bin/codex-slack-permission-request"
timeout = 10
statusMessage = "Sending approval request to Slack"
```

Install the same compiled binary under both names:

```bash
mkdir -p ~/.local/bin
go build -o ~/.local/bin/codex-slack-notify ./cmd/codex-slack-notify
cp ~/.local/bin/codex-slack-notify ~/.local/bin/codex-slack-permission-request
chmod +x ~/.local/bin/codex-slack-notify ~/.local/bin/codex-slack-permission-request
```

Then watch:

```bash
tail -f ~/.codex/permission-request-hooks.jsonl
```

When invoked as `codex-slack-permission-request`, the binary automatically logs
raw hook payloads to `~/.codex/permission-request-hooks.jsonl` even if you do
not pass `--log-path`.

Restart Codex after changing `~/.codex/config.toml` or the Windows
`C:\Users\YOURNAME\.codex\config.toml` file so the updated notification
settings take effect.

## Limitations

- Incoming Slack webhooks are one-way. You cannot reply in Slack and have Codex
  continue from that reply by webhook alone.
- For true Slack interaction you would need a real Slack bot, interactivity or
  slash commands, request signature verification, and a safe bridge back to the
  machine running Codex.
