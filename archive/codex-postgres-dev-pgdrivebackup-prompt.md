# Build Local Postgres Dev + Google Drive Backup Utility

Create all files directly under the current working directory. Do not ask follow-up questions; make reasonable choices and implement the project.

You are working in a new project folder.

Goal:

Create a clean local development setup for Postgres plus a Go backup utility that can automatically back up multiple Postgres databases from multiple configured servers, including both native Postgres and Docker-based Postgres, upload the backups to Google Drive, and apply rotating retention rules.

This is for a Windows development machine that also uses WSL and Docker Desktop.

## High-level architecture

Create a repo with two main parts:

```text
postgres-dev/
  docker-compose.yml
  .env.example
  README.md

pgdrivebackup/
  cmd/pgdrivebackup/main.go
  internal/config/
  internal/backup/
  internal/drive/
  internal/retention/
  internal/logging/
  configs/example.yaml
  README.md
  go.mod
  go.sum
```

## Part 1: Local shared development Postgres

Create a `postgres-dev/docker-compose.yml` that starts one shared local Postgres instance for development.

Requirements:

- Use Docker Compose.
- Use the official Postgres image.
- Default to Postgres 16 unless there is a good reason not to.
- Container name: `dev-postgres`.
- Expose Postgres on localhost port `5432`.
- Use a Docker named volume, not a bind mount to the Windows filesystem.
- Include `.env.example`.
- Include clear README instructions.

Example behavior:

Windows apps, WSL apps, and local tools should connect with:

```text
Host: localhost
Port: 5432
User: postgres
Password: devpassword
Database: postgres
```

Dockerized apps on the same Docker network should be able to connect using the Docker service name.

Do not store real secrets in the repo.

## Part 2: Go app — pgdrivebackup

Build a production-quality Go command-line app named `pgdrivebackup`.

The app backs up Postgres databases and uploads compressed backup files to Google Drive.

It must support:

1. Native/TCP Postgres backups
2. Docker Postgres backups using `docker exec`
3. Multiple servers
4. Multiple databases per server
5. Per-database backup file naming
6. Gzip compression
7. Optional encryption before upload
8. Google Drive upload
9. Rotating retention rules
10. Dry-run mode
11. Restore instructions
12. Windows Task Scheduler instructions
13. Linux cron/systemd timer instructions

## Backup methods

Support two backup modes.

### Mode A: TCP/native Postgres

Use local `pg_dump` from the machine running the Go app.

Example:

```bash
pg_dump --format=custom --no-owner --no-acl --host HOST --port PORT --username USER DBNAME
```

Do not put passwords directly on the command line.

Use `PGPASSWORD` environment variable when needed.

### Mode B: Docker Postgres

Use `docker exec` to run `pg_dump` inside the configured Postgres container.

Example:

```bash
docker exec -e PGPASSWORD=... CONTAINER pg_dump --format=custom --no-owner --no-acl --username USER DBNAME
```

The app should stream stdout from `pg_dump`, gzip it, optionally encrypt it, save it locally temporarily, then upload it to Google Drive.

## Backup format

Use Postgres custom dump format:

```bash
pg_dump --format=custom
```

File extension should be:

```text
.dump.gz
```

If encrypted:

```text
.dump.gz.enc
```

Use filenames like:

```text
serverName_databaseName_YYYY-MM-DD_HH-mm-ss.dump.gz
```

Example:

```text
localdev_ommadb_dev_2026-04-18_02-00-00.dump.gz
```

## Google Drive

Use Google Drive API v3.

Auth approach:

- Use OAuth desktop app flow for a personal Google Drive account.
- Store OAuth token locally in a configurable token file.
- Support a first-run auth command.
- Do not commit credentials or tokens.
- Upload files into a configured Google Drive folder.
- If the folder does not exist, create it.
- Create subfolders by server and database.

Desired Drive layout:

```text
PostgresBackups/
  localdev/
    ommadb_dev/
      daily/
      weekly/
      monthly/
    wordpress_dev/
      daily/
      weekly/
      monthly/
  production-vps/
    app1/
      daily/
      weekly/
      monthly/
```

Use official Google API Go libraries.

The Google Drive API quickstart for Go says the quickstart auth approach is suitable for testing, and Google recommends choosing credentials appropriate for production usage; document that clearly in the README. Google Drive uploads should use Drive API file creation/upload behavior. 

Sources to consult if needed:

- https://developers.google.com/workspace/drive/api/quickstart/go
- https://developers.google.com/workspace/drive/api/guides/manage-uploads

## Retention rules

Implement grandparent/father/son style retention.

Retention should be configurable globally and per database.

Default rules:

```yaml
retention:
  daily_keep: 14
  weekly_keep: 8
  monthly_keep: 12
```

Meaning:

- Keep the most recent 14 daily backups.
- Keep one weekly backup per week for the last 8 weeks.
- Keep one monthly backup per month for the last 12 months.

The app should upload each backup to the correct daily/weekly/monthly folder as needed.

Simpler acceptable implementation:

- Every run creates a daily backup.
- On the first successful backup of a week, also copy/upload it as weekly.
- On the first successful backup of a month, also copy/upload it as monthly.
- Then apply retention cleanup in each folder.

Retention cleanup should be safe:

- Never delete files that do not match the app’s filename pattern.
- Only delete files in the configured Drive backup folders.
- In dry-run mode, print what would be deleted but do not delete.

## Config file

Use YAML config.

Create `configs/example.yaml`.

Example:

```yaml
google_drive:
  root_folder_name: "PostgresBackups"
  credentials_file: "./secrets/google_credentials.json"
  token_file: "./secrets/google_token.json"

backup:
  temp_dir: "./tmp"
  gzip_level: 6
  dry_run: false
  optional_encryption:
    enabled: false
    passphrase_env: "PGDRIVEBACKUP_ENCRYPTION_PASSWORD"

retention:
  daily_keep: 14
  weekly_keep: 8
  monthly_keep: 12

servers:
  - name: "localdev"
    type: "tcp"
    host: "localhost"
    port: 5432
    username: "postgres"
    password_env: "LOCALDEV_POSTGRES_PASSWORD"
    databases:
      - name: "ommadb_dev"
      - name: "wordpress_dev"

  - name: "dockerdev"
    type: "docker"
    container: "dev-postgres"
    username: "postgres"
    password_env: "LOCALDEV_POSTGRES_PASSWORD"
    databases:
      - name: "app1_dev"
      - name: "app2_dev"
```

Allow per-database retention override:

```yaml
databases:
  - name: "ommadb_dev"
    retention:
      daily_keep: 30
      weekly_keep: 12
      monthly_keep: 24
```

## CLI commands

Implement these commands:

```bash
pgdrivebackup auth --config configs/example.yaml
pgdrivebackup backup --config configs/example.yaml
pgdrivebackup backup --config configs/example.yaml --server localdev
pgdrivebackup backup --config configs/example.yaml --server localdev --database ommadb_dev
pgdrivebackup backup --config configs/example.yaml --dry-run
pgdrivebackup list --config configs/example.yaml
pgdrivebackup retention --config configs/example.yaml --dry-run
pgdrivebackup version
```

Use a Go CLI library if useful, but keep dependencies reasonable.

## Logging

Use structured, readable logging.

Log:

- Backup start/end
- Server name
- Database name
- Backup method
- Local temp filename
- Upload destination
- File size
- Duration
- Retention deletions
- Errors

Do not log passwords or tokens.

## Error handling

Requirements:

- A failure backing up one database should not prevent other configured databases from being attempted.
- Return non-zero exit code if any backup fails.
- Print a final summary table.
- Include clear error messages for missing `pg_dump`, missing `docker`, failed auth, failed upload, and bad config.

## Encryption

Implement optional encryption.

Preferred:

- AES-256-GCM using a key derived from a passphrase with a standard KDF.
- Passphrase comes from environment variable configured in YAML.
- Never write plaintext after encryption except temporary files needed during pipeline.
- Make cleanup robust.

If encryption is disabled, upload `.dump.gz`.

If encryption is enabled, upload `.dump.gz.enc`.

Document restore process for both encrypted and unencrypted backups.

## Restore docs

In README, include examples:

Unencrypted restore:

```bash
gunzip backup.dump.gz
pg_restore --clean --if-exists --no-owner --no-acl --dbname target_db backup.dump
```

Docker restore:

```bash
gunzip -c backup.dump.gz | docker exec -i dev-postgres pg_restore --clean --if-exists --no-owner --no-acl --username postgres --dbname target_db
```

Encrypted restore should show decrypt first, then restore.

## Scheduling

Document Windows Task Scheduler setup.

Example command:

```powershell
C:\path\to\pgdrivebackup.exe backup --config C:\path\to\configs\local.yaml
```

Also document Linux cron example:

```cron
0 2 * * * /usr/local/bin/pgdrivebackup backup --config /etc/pgdrivebackup/config.yaml
```

And a systemd service/timer example.

Note: WSL does not always run continuously like a normal Linux server, so for a Windows development machine prefer Windows Task Scheduler unless the app is running on a real Linux server.

## Security

Document:

- Do not commit `.env`, Google credentials, tokens, or backup files.
- Prefer Drive folder access limited to the backup app.
- Use environment variables for DB passwords.
- Consider encryption before uploading database backups to Google Drive.
- Test restore periodically.

## Tests

Add unit tests for:

- Config loading
- Filename parsing
- Retention selection
- Dry-run deletion behavior
- Server/database filtering

Where Google Drive is involved, design interfaces so tests can use a fake Drive client.

## Quality bar

The result should be something I can actually use.

Please produce:

1. Working source code
2. README files
3. Example config
4. Example `.env`
5. Docker Compose file
6. Restore examples
7. Scheduling examples
8. Clear TODOs only where external setup is unavoidable, such as Google OAuth credentials

Avoid huge abstractions. Keep it practical, readable, and maintainable.
