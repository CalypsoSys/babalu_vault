# pgdrivebackup

`pgdrivebackup` is a Go terminal application for backing up PostgreSQL databases to a local backup directory with gzip compression and automatic daily, weekly, and monthly promotion.

It supports:

- Native PostgreSQL/TCP backups using local `pg_dump`
- Docker PostgreSQL backups using `docker exec`
- SSH-backed PostgreSQL backups for remote native or remote Docker `pg_dump`
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
  cmd/pgdrivebackup/main.go
  internal/config/
  internal/backup/
  internal/retention/
  internal/logging/
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

Database passwords are not stored in YAML. Use environment variables instead:

```bash
export LOCALDEV_POSTGRES_PASSWORD=local-dev-password
```

On Windows PowerShell:

```powershell
$env:LOCALDEV_POSTGRES_PASSWORD="local-dev-password"
```

Key config fields:

```yaml
backup:
  temp_dir: "./tmp"
  root_dir: "./backups"
  log_path: "./logs/pgdrivebackup.log"
  gzip_level: 6
  run_interval: "1h"
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

The TUI starts a backup immediately, keeps running, and schedules the next automatic backup using `backup.run_interval`.

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

## Backup methods

### TCP/native mode

Uses the local `pg_dump` binary:

```bash
pg_dump --format=plain --no-owner --no-acl --no-password --host HOST --port PORT --username USER DBNAME
```

Passwords are passed through `PGPASSWORD`, not on the command line.

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
  password_env: "REMOTE_POSTGRES_PASSWORD"
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
  password_env: "REMOTE_POSTGRES_PASSWORD"
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

After that, the same backup cycle automatically manages promotion:

- when there are more than 4 daily backups, the oldest overflow daily can be promoted into a `weekly_...gz` file once it is at least 7 days old
- when there is more than 1 weekly backup, the oldest overflow weekly can be promoted into a `monthly_...gz` file once it is at least 30 days old
- only 1 weekly and 1 monthly backup are kept with the default policy

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

The default UI mode already runs continuously and executes scheduled backups using `backup.run_interval`. Use an external service manager only if you want the TUI process itself to start automatically after reboot or login.

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
