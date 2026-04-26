# pgdrivebackup

`pgdrivebackup` is a Go terminal application for backing up PostgreSQL databases to a local backup directory with gzip compression and automatic daily, weekly, and monthly promotion.

It supports:

- Native PostgreSQL/TCP backups using local `pg_dump`
- Docker PostgreSQL backups using `docker exec`
- SSH-backed PostgreSQL backups for remote native or remote Docker `pg_dump`
- Slack notifications for Codex turn-complete webhooks via `codex-slack-notify`
- Gzipped plain SQL backups for easier cross-version restores
- Multiple servers and multiple databases per server
- Per-database retention overrides
- Gzip compression
- Continuous TUI mode built with Bubble Tea and Lip Gloss
- Local filesystem storage under a configured backup root
- Daily, weekly, and monthly lifecycle folders
- Dry-run mode
- Config-based filtering by server and database

## Repository layout

```text
pgdrivebackup/
  cmd/codex-slack-notify/main.go
  cmd/pgdrivebackup/main.go
  internal/config/
  internal/backup/
  internal/retention/
  internal/logging/
  internal/slacknotify/
  configs/example.yaml
```

## Prerequisites

- Go 1.26+
- PostgreSQL client tools if you use `type: tcp`
- Docker CLI if you use `type: docker`
- SSH client if you use `type: ssh`
- A writable local backup directory

## Example config

See `configs/example.yaml`.

Database passwords use the `password` field. For environment-backed secrets, write them as `${VARNAME}`:

```bash
export LOCALDEV_POSTGRES_PASSWORD=local-dev-password
```

On Windows PowerShell:

```powershell
$env:LOCALDEV_POSTGRES_PASSWORD="local-dev-password"
```

Example server password field:

```yaml
password: "${LOCALDEV_POSTGRES_PASSWORD}"
```

Key config fields:

```yaml
backup:
  temp_dir: "./tmp"
  root_dir: "./backups"
  log_path: "./logs/pgdrivebackup.log"
  gzip_level: 6
  time_of_day: "02:00"
  state_path: "./state/pgdrivebackup-scheduler.json"
```

Backups are stored under:

```text
<root_dir>/<server>/<database>/
```

Managed files are prefixed by retention tier:

```text
daily_<server>_<database>_YYYY-MM-DD.gz
weekly_<server>_<database>_YYYY-MM-DD.gz
monthly_<server>_<database>_YYYY-MM-DD.gz
```

With the default settings, each database keeps at most 6 managed backups:

- 4 recent daily backups
- 1 weekly backup promoted from an older daily
- 1 monthly backup promoted from an older weekly

## Commands

Launch the continuous terminal UI:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml
```

The TUI keeps running and schedules one automatic backup per local calendar day at `backup.time_of_day`. On startup, it checks `backup.state_path` and runs immediately if it has not already run yet that day.

While the TUI is running, it also watches the YAML config file for changes and reloads it automatically. Updated schedules, targets, retention settings, backup root paths, and dry-run settings are applied to future runs without restarting the app.

Launch the TUI in dry-run mode:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml --dry-run
```

TUI controls:

- `/`: open the command palette
- `b`: run backup now
- `p`: pause or resume the scheduler
- `j` / `k`: move through configured databases
- `q`: quit

One-shot commands are still available.

List config:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml list
```

Back up everything:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml backup
```

Back up a single server:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml backup --server localdev
```

Back up one database:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml backup --server localdev --database app1_dev
```

Dry run:

```bash
go run ./cmd/pgdrivebackup --config configs/example.yaml backup --dry-run
```

Version:

```bash
go run ./cmd/pgdrivebackup version
```

## Codex Slack notifier

Build the notifier:

```bash
go build ./cmd/codex-slack-notify
```

Or from the repo root:

```bash
make build-slack-notify
```

The executable accepts the Codex JSON payload as its first argument, looks for
`SLACK_CODEX_WEBHOOK_URL` in the current environment, and also loads
`~/.config/secrets/env` when present to mirror shell startup behavior.

If the webhook variable is missing, it exits successfully without sending
anything. Invalid JSON payloads fall back to a generic `"Codex needs attention."`
Slack message.

## Codex Slack alerts setup

This notifier is meant for "Codex finished" or "Codex needs attention" phone
alerts. It does not make Slack a two-way Codex control surface. The practical
workflow is:

```text
Codex in tmux -> codex-slack-notify -> Slack channel -> phone alert -> SSH back in -> tmux attach
```

### 1. Store the Slack webhook secret

Keep the webhook URL out of the repo and load it from a private env file:

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

For a user-local install path:

```bash
mkdir -p ~/.local/bin
cd /path/to/pgdrivebackup
go build -o ~/.local/bin/codex-slack-notify ./cmd/codex-slack-notify
chmod +x ~/.local/bin/codex-slack-notify
```

You can also keep using a repo-local build during development, but a stable path
is easier for Codex config.

### 4. Test the notifier manually

```bash
~/.local/bin/codex-slack-notify '{"type":"test","last-assistant-message":"Slack test from codex-slack-notify","cwd":"/tmp"}'
```

You should see a message in your Slack channel. On your phone, set that channel
to notify you for all new messages if you want reliable attention pings.

### 5. Configure Codex to call the notifier

Edit your Codex config:

```bash
mkdir -p ~/.codex
vi ~/.codex/config.toml
```

Add or adjust:

```toml
notify = ["/home/joe/.local/bin/codex-slack-notify"]

[tui]
notifications = ["agent-turn-complete", "approval-requested"]
notification_method = "auto"
```

If your username is not `joe`, update the path accordingly.

### 6. Run Codex inside tmux

This is the recommended companion setup because Slack alerts are best used as
"come back and check Codex" notifications:

```bash
sudo apt install tmux
tmux new -s codex
codex
```

Detach before walking away with `Ctrl+b`, then `d`. When Slack pings your phone,
SSH back in and reattach:

```bash
tmux attach -t codex
```

### Limitations

- Incoming Slack webhooks are one-way. You cannot reply in Slack and have Codex
  continue from that reply by webhook alone.
- For true Slack interaction you would need a real Slack bot, interactivity or
  slash commands, request signature verification, and a safe bridge back to the
  machine running Codex.
- The simplest and safest pattern is still: Slack alert, SSH back in, reattach
  `tmux`, continue in the terminal.

## Backup methods

### TCP/native mode

Uses the local `pg_dump` binary:

```bash
pg_dump --format=plain --no-owner --no-acl --no-password --host HOST --port PORT --username USER DBNAME
```

Passwords are passed through `PGPASSWORD`, not on the command line. The `password` config value may be a literal or a `${VARNAME}` environment reference.

### Docker mode

Uses `docker exec` to run `pg_dump` inside the configured container:

```bash
docker exec -e PGPASSWORD=... CONTAINER pg_dump --format=plain --no-owner --no-acl --no-password --username USER DBNAME
```

### SSH mode

Uses the local `ssh` client to run a constrained remote `pg_dump` command and stream the dump back to this machine for local gzip storage.

SSH is run in non-interactive mode for scheduler safety. The remote host key must already be trusted in your local `known_hosts` file, or the backup will fail fast instead of prompting inside the TUI.

Two remote shapes are supported:

- `ssh_remote_type: tcp`: run remote native `pg_dump`
- `ssh_remote_type: docker`: run remote `docker exec ... pg_dump`

Example YAML for remote Docker:

```yaml
- name: "sshdocker"
  type: "ssh"
  ssh_target: "backup@example-host"
  ssh_remote_type: "docker"
  container: "ommadb-postgres"
  username: "postgres"
  password: "${REMOTE_POSTGRES_PASSWORD}"
  databases:
    - name: "mma_data_web"
```

That produces a remote command shape like:

```bash
ssh backup@example-host 'docker exec -e PGPASSWORD=${REMOTE_POSTGRES_PASSWORD} ommadb-postgres pg_dump --format=plain --no-owner --no-acl --no-password --username postgres mma_data_web'
```

Example YAML for remote TCP:

```yaml
- name: "sshtcp"
  type: "ssh"
  ssh_target: "backup@db-host"
  ssh_remote_type: "tcp"
  host: "127.0.0.1"
  port: 5432
  username: "postgres"
  password: "${REMOTE_POSTGRES_PASSWORD}"
  databases:
    - name: "reporting"
```

## Backup file naming

```text
daily_serverName_databaseName_YYYY-MM-DD.gz
```

Example:

```text
daily_localdev_app1_dev_2026-04-18.gz
```

## Backup lifecycle

Default global retention:

```yaml
retention:
  daily_keep: 4
  weekly_keep: 1
  monthly_keep: 1
```

Each successful run writes one new `daily_...gz` backup into the database directory.

After that, the same backup cycle automatically manages snapshot tiers:

- when a new ISO week begins, the newest daily backup from a prior week is copied into a `weekly_...gz` snapshot if that week does not already have one
- when a new calendar month begins, the newest daily backup from a prior month is copied into a `monthly_...gz` snapshot if that month does not already have one
- if a weekly or monthly tier is empty, the next backup run bootstraps that tier from the oldest available daily backup so snapshot history starts immediately
- only 1 weekly and 1 monthly snapshot are kept with the default policy

Cleanup is intentionally conservative:

- Only files whose names match this app's managed filename pattern are eligible for deletion.
- Only files inside the configured local backup folders are considered.
- In dry-run mode, deletions are logged but not executed.

## Restore

```bash
gunzip -c backup.gz | psql --dbname target_db
```

Docker restore:

```bash
gunzip -c backup.gz | docker exec -i dev-postgres psql --username postgres --dbname target_db
```

## Scheduling

### In-app scheduler

The default UI mode already runs continuously and executes one scheduled backup per local calendar day at `backup.time_of_day`.

It also persists the last completed scheduled run in `backup.state_path`, so after a reboot or restart it will immediately catch up if no run has happened yet on the current local date.

The scheduler polls the config file once per second while the TUI is active. If the file changes and the new YAML validates, the app updates the in-memory config and future runs use the new values immediately.

Reload behavior is intentionally conservative:

- An in-progress backup continues with the config it started with.
- Future manual and scheduled backups use the reloaded config.
- Existing target status rows are preserved for unchanged server/database pairs.
- Newly added targets appear immediately with `pending` status.
- Removed targets disappear from the target list on the next successful reload.
- If a config edit is invalid, the app logs a reload warning and keeps using the last valid config.

Current limitation:

- Changing `backup.log_path` does not move the already-open TUI log writer until the process is restarted.

Use an external service manager only if you want the TUI process itself to start automatically after reboot or login.

### Windows Task Scheduler

For a Windows development machine, prefer Windows Task Scheduler over WSL cron because WSL does not always run continuously.

Example action:

```powershell
C:\path\to\pgdrivebackup.exe --config C:\path\to\configs\local.yaml
```

### Linux cron

```cron
@reboot /usr/local/bin/pgdrivebackup --config /etc/pgdrivebackup/config.yaml
```

### systemd service

Service example:

```ini
[Unit]
Description=pgdrivebackup TUI scheduler

[Service]
Type=simple
WorkingDirectory=/opt/pgdrivebackup
ExecStart=/usr/local/bin/pgdrivebackup --config /etc/pgdrivebackup/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Security notes

- Do not commit real config files with machine-specific backup paths unless they are intentionally shared defaults.
- Keep database passwords in environment variables, not YAML.
- Backup files can contain sensitive application data and PII. Store `backup.root_dir` on secured storage with appropriate filesystem permissions and backup retention policies.
- Application logs can be written to `backup.log_path`. Treat those logs as potentially sensitive because backup names, database names, errors, and operational details may appear there.

## Development

Run tests:

```bash
go test ./...
```

Build:

```bash
go build ./cmd/pgdrivebackup
```
