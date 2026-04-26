# codex-slack-notify

`codex-slack-notify` is a small Go executable that posts Codex completion or
attention alerts to a Slack incoming webhook.

It accepts the Codex JSON payload as its first argument, looks for
`SLACK_CODEX_WEBHOOK_URL` in the environment, and also loads
`~/.config/secrets/env` when present so it still works when Codex starts from a
shell that did not source your profile.

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

This notifier is meant for "Codex finished" or "Codex needs attention" phone
alerts. It does not make Slack a two-way Codex control surface.

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

You should see a message in your Slack channel. On your phone, set that channel
to notify you for all new messages if you want reliable attention pings.

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
notify = ["~/.local/bin/codex-slack-notify"]
```

Windows example:

```toml
notify = ["C:\\Users\\YOURNAME\\bin\\codex-slack-notify.exe"]
```

What these settings do:

- `notify = ["~/.local/bin/codex-slack-notify"]` is the external hook.
  Codex runs that command and passes it a JSON payload, which is what sends the
  Slack message.
- `notify = ["C:\\Users\\YOURNAME\\bin\\codex-slack-notify.exe"]` does the
  same thing on Windows without any PowerShell wrapper.

## Limitations

- Incoming Slack webhooks are one-way. You cannot reply in Slack and have Codex
  continue from that reply by webhook alone.
- For true Slack interaction you would need a real Slack bot, interactivity or
  slash commands, request signature verification, and a safe bridge back to the
  machine running Codex.
