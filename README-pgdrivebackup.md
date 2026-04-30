# pgdrivebackup

`pgdrivebackup` is a Go terminal application for backing up PostgreSQL databases to a local backup directory with gzip compression and automatic daily, weekly, and monthly promotion.

It supports:

- Native PostgreSQL/TCP backups using local `pg_dump`
- Docker PostgreSQL backups using `docker exec`
- SSH-backed PostgreSQL backups for remote native or remote Docker `pg_dump`
- Gzipped plain SQL backups for easier cross-version restores
- Multiple servers and multiple databases per server
- Optional database discovery per server using `pg_database`
- Global retention policy
- Gzip compression
- Continuous TUI mode built with Bubble Tea and Lip Gloss
- Local filesystem storage under a configured backup root
- Daily, weekly, and monthly lifecycle folders
- Dry-run mode
- Config-based filtering by server and database

## Repository layout

```text
configs/
  example.yaml
cmd/pgdrivebackup/main.go
internal/config/
internal/backup/
internal/retention/
internal/logging/
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

List backup targets:

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

## Database discovery mode

If a server should back up every database it can see, enable discovery on that server:

```yaml
- name: "dockerdev"
  type: "docker"
  container: "dev-postgres"
  username: "postgres"
  password: "${LOCALDEV_POSTGRES_PASSWORD}"
  discover_databases: true
  ignore_databases:
    - "postgres"
    - "template0"
    - "template1"
```

When `discover_databases: true` is enabled:

- `backup` queries `pg_database` at runtime and backs up every discovered database except ignored names
- `list` queries the live server and prints the current discovered targets
- entries under `databases:` become optional and can be used to document expected databases

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
- weekly snapshots are only created from backups in a completed prior ISO week
- monthly snapshots are only created from backups in a completed prior calendar month
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
