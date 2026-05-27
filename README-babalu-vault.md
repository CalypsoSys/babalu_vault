# babalu-vault

`babalu-vault` is a Go terminal application for backing up configured database and file targets to local storage with gzip compression and automatic daily, weekly, and monthly retention.

It supports:

- PostgreSQL backups over local TCP, local Docker, remote SSH TCP, or remote SSH Docker
- MySQL backups over local TCP, local Docker, remote SSH TCP, or remote SSH Docker
- Remote file-set backups over SSH using `tar`
- Live PostgreSQL database discovery using `pg_database`
- Gzipped SQL archives for database targets and gzipped tar archives for file targets
- Global retention policy
- Continuous TUI mode with a daily in-app scheduler
- One-shot `list` and `backup` commands
- Dry-run mode
- Filtering by server, backup job, and target

## Repository Layout

```text
configs/
  example.yaml
cmd/babalu-vault/main.go
internal/config/
internal/backup/
internal/retention/
internal/logging/
```

## Prerequisites

- Go 1.26+
- PostgreSQL client tools if you back up PostgreSQL over local TCP
- MySQL client tools if you back up MySQL over local TCP
- Docker CLI if you back up local Docker containers
- SSH client if you back up remote hosts
- A writable local backup directory

Remote SSH Docker and remote SSH file backups only require the local `ssh` client. The database or `tar` command runs on the remote host.

## Config Shape

See `configs/example.yaml` for a complete sample.

The config is intentionally split into three concepts:

- `settings`: app paths, compression, dry-run, and scheduler settings
- `servers`: reusable access contexts such as TCP, local Docker, or SSH hosts
- `backups`: backup jobs that point at a server and define an engine, source, targets, and optional discovery

Database passwords usually use the `source.password` field. For environment-backed secrets, write them as `${VARNAME}`:

```bash
export LOCALDEV_POSTGRES_PASSWORD=local-dev-password
```

On Windows PowerShell:

```powershell
$env:LOCALDEV_POSTGRES_PASSWORD="local-dev-password"
```

For MySQL Docker sources, `source.password_env` can read the password from an environment variable already present inside the database container. This is useful for WordPress MySQL containers that already have `MYSQL_ROOT_PASSWORD` in their Compose environment.

Example config fragments:

```yaml
settings:
  temp_dir: "./tmp"
  root_dir: "./backups"
  log_path: "./logs/babalu-vault.log"
  gzip_level: 6
  dry_run: false
  time_of_day: "02:00"
  state_path: "./state/babalu-vault-scheduler.json"

retention:
  daily_keep: 4
  weekly_keep: 1
  monthly_keep: 1

servers:
  - name: "local-postgres-host"
    type: "tcp"
    host: "localhost"

  - name: "local-docker"
    type: "docker"

  - name: "homelab-wp-host"
    type: "ssh"
    ssh_target: "backup@homelab-wp-host"
    ssh_port: 22
```

A PostgreSQL TCP backup:

```yaml
backups:
  - name: "local-postgres"
    server: "local-postgres-host"
    engine: "postgres"
    source:
      mode: "tcp"
      port: 5432
      username: "postgres"
      password: "${LOCALDEV_POSTGRES_PASSWORD}"
    targets:
      - name: "app-db"
        database: "app"
```

A remote Docker MySQL backup:

```yaml
backups:
  - name: "dndboomer-mysql"
    server: "homelab-wp-host"
    engine: "mysql"
    source:
      mode: "docker"
      container: "dndboomer_wp_db"
      username: "root"
      password_env: "MYSQL_ROOT_PASSWORD"
    targets:
      - name: "dndboomer-db"
        database: "wordpress"
```

A remote file-set backup:

```yaml
backups:
  - name: "homelab-wp-files"
    server: "homelab-wp-host"
    engine: "files"
    targets:
      - name: "dndboomer-html"
        path: "/srv/stacks/wordpress/sites/dndboomer/html"
        excludes:
          - "wp-content/cache/**"
          - "wp-content/debug.log"
```

Backups are stored under:

```text
<root_dir>/<backup>/<target>/
```

Managed files are prefixed by retention tier:

```text
daily_<backup>_<target>_YYYY-MM-DD.sql.gz
weekly_<backup>_<target>_YYYY-MM-DD.sql.gz
monthly_<backup>_<target>_YYYY-MM-DD.sql.gz

daily_<backup>_<target>_YYYY-MM-DD.tar.gz
weekly_<backup>_<target>_YYYY-MM-DD.tar.gz
monthly_<backup>_<target>_YYYY-MM-DD.tar.gz
```

Database targets use `.sql.gz`; file targets use `.tar.gz`.

## Commands

Launch the continuous terminal UI:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml
```

The TUI schedules one automatic backup per local calendar day at `settings.time_of_day`. On startup, it checks `settings.state_path` and runs immediately if it has not already run yet that day.

While the TUI is running, it watches the YAML config file for changes and reloads it automatically. Updated schedules, targets, retention settings, backup root paths, and dry-run settings are applied to future runs without restarting the app.

Launch the TUI in dry-run mode:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml --dry-run
```

TUI controls:

- `/`: open the command palette
- `b`: run backup now
- `p`: pause or resume the scheduler
- `j` / `k`: move through configured targets
- `q`: quit

List backup targets:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml list
```

Back up everything:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml backup
```

Back up a single server:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml backup --server homelab-wp-host
```

Back up one backup job:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml backup --backup dndboomer-mysql
```

Back up one target:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml backup --backup dndboomer-mysql --target dndboomer-db
```

Dry run:

```bash
go run ./cmd/babalu-vault --config configs/example.yaml backup --dry-run
```

Version:

```bash
go run ./cmd/babalu-vault version
```

## Discovery

PostgreSQL backup jobs can discover databases at runtime:

```yaml
backups:
  - name: "local-docker-postgres"
    server: "local-docker"
    engine: "postgres"
    source:
      mode: "docker"
      container: "dev-postgres"
      username: "postgres"
      password: "${LOCALDEV_POSTGRES_PASSWORD}"
    discovery:
      databases: true
      ignore:
        - "postgres"
        - "template0"
        - "template1"
```

When `discovery.databases: true` is enabled:

- `backup` queries `pg_database` at runtime and backs up every discovered database except ignored names
- `list` queries the live server and prints the current discovered targets
- entries under `targets` are optional and can be used to override names or document expected databases

MySQL discovery is intentionally not supported yet. Configure MySQL database targets explicitly.

## Backup Methods

### PostgreSQL TCP

Uses local `pg_dump`:

```bash
PGPASSWORD=... pg_dump --format=plain --no-owner --no-acl --no-password --host HOST --port PORT --username USER DBNAME
```

### PostgreSQL Docker

Uses local or remote `docker exec`:

```bash
docker exec -e PGPASSWORD=... CONTAINER pg_dump --format=plain --no-owner --no-acl --no-password --username USER DBNAME
```

### MySQL TCP

Uses local `mysqldump`:

```bash
MYSQL_PWD=... mysqldump --single-transaction --quick --routines --triggers --events --hex-blob --default-character-set=utf8mb4 --host HOST --port PORT --user USER DBNAME
```

### MySQL Docker

Uses local or remote `docker exec`:

```bash
docker exec -e MYSQL_PWD=... CONTAINER mysqldump --single-transaction --quick --routines --triggers --events --hex-blob --default-character-set=utf8mb4 -uUSER DBNAME
```

When `source.password_env` is set, the command reads the password from inside the container instead:

```bash
docker exec CONTAINER sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump --single-transaction --quick --routines --triggers --events --hex-blob --default-character-set=utf8mb4 -uUSER DBNAME'
```

### File Sets

Uses SSH to run remote `tar` and streams the archive back to this machine:

```bash
ssh backup@example-host tar --numeric-owner --one-file-system -C /path/to/source --exclude PATTERN -cf - .
```

SSH is run in non-interactive mode for scheduler safety. The remote host key must already be trusted in your local `known_hosts` file, or the backup will fail fast instead of prompting inside the TUI.

## Retention

Default global retention:

```yaml
retention:
  daily_keep: 4
  weekly_keep: 1
  monthly_keep: 1
```

Each successful run writes one new `daily_...` backup into the target directory.

After that, the same backup cycle manages snapshot tiers:

- when a new ISO week begins, the newest daily backup from a prior week is copied into a weekly snapshot if that week does not already have one
- when a new calendar month begins, the newest daily backup from a prior month is copied into a monthly snapshot if that month does not already have one
- when the newest weekly snapshot is at least 7 days old, the newest daily backup is copied into a refreshed weekly snapshot
- when the newest monthly snapshot is at least 1 calendar month old, the newest weekly backup is copied into a refreshed monthly snapshot

Cleanup is intentionally conservative:

- Only files whose names match this app's managed filename pattern are eligible for deletion.
- Only files inside the configured local backup folders are considered.
- In dry-run mode, deletions are logged but not executed.

## Restore

PostgreSQL:

```bash
gunzip -c daily_local-postgres_app-db_2026-05-27.sql.gz | psql --dbname app
```

PostgreSQL Docker:

```bash
gunzip -c daily_local-postgres_app-db_2026-05-27.sql.gz | docker exec -i dev-postgres psql --username postgres --dbname app
```

MySQL:

```bash
gunzip -c daily_dndboomer-mysql_dndboomer-db_2026-05-27.sql.gz | mysql --user root --password wordpress
```

File set:

```bash
tar xzf daily_homelab-wp-files_dndboomer-html_2026-05-27.tar.gz -C /restore/path
```

## Scheduling

### In-App Scheduler

The default UI mode already runs continuously and executes one scheduled backup per local calendar day at `settings.time_of_day`.

It persists the last completed scheduled run in `settings.state_path`, so after a reboot or restart it immediately catches up if no run has happened yet on the current local date.

The scheduler polls the config file once per second while the TUI is active. If the file changes and the new YAML validates, the app updates the in-memory config and future runs use the new values immediately.

Reload behavior is intentionally conservative:

- An in-progress backup continues with the config it started with.
- Future manual and scheduled backups use the reloaded config.
- Existing target status rows are preserved for unchanged server/backup/target triples.
- Newly added targets appear immediately with `pending` status.
- Removed targets disappear from the target list on the next successful reload.
- If a config edit is invalid, the app logs a reload warning and keeps using the last valid config.

Current limitation:

- Changing `settings.log_path` does not move the already-open TUI log writer until the process is restarted.

Use an external service manager only if you want the TUI process itself to start automatically after reboot or login.

### Windows Task Scheduler

For a Windows development machine, prefer Windows Task Scheduler over WSL cron because WSL does not always run continuously.

Example action:

```powershell
C:\path\to\babalu-vault.exe --config C:\path\to\configs\local.yaml
```

### Linux Cron

```cron
@reboot /usr/local/bin/babalu-vault --config /etc/babalu-vault/config.yaml
```

### systemd Service

```ini
[Unit]
Description=babalu-vault TUI scheduler

[Service]
Type=simple
WorkingDirectory=/opt/babalu-vault
ExecStart=/usr/local/bin/babalu-vault --config /etc/babalu-vault/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Security Notes

- Do not commit real config files with machine-specific backup paths unless they are intentionally shared defaults.
- Keep database passwords in environment variables, not YAML.
- Backup files can contain sensitive application data and PII. Store `settings.root_dir` on secured storage with appropriate filesystem permissions and backup retention policies.
- Application logs can be written to `settings.log_path`. Treat those logs as potentially sensitive because backup names, target names, errors, and operational details may appear there.

## Development

Run tests:

```bash
go test ./...
```

Build:

```bash
go build ./cmd/babalu-vault
```
