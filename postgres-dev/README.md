# postgres-dev

Shared local PostgreSQL for development on a Windows machine that also uses WSL and Docker Desktop.

## Defaults

- PostgreSQL version: `18`
- Container name: `dev-postgres`
- Host access: `localhost:5432`
- Docker service name on the Compose network: `postgres`
- Data storage: Docker named volume `postgres_dev_data`
- File logs: host-mounted from `${POSTGRES_LOG_DIR}` and defaulting to `/srv/logs/postgres`

For PostgreSQL 18 and later, the official Docker image expects the named volume to be mounted at `/var/lib/postgresql`, not `/var/lib/postgresql/data`.

Default connection settings:

```text
Host: localhost
Port: 5432
User: postgres
Password: local-dev-password
Database: postgres
```

## Quick start

1. Copy `.env.example` to `.env`.
2. Adjust values if needed.
3. Start PostgreSQL:

```bash
docker compose up -d
```

4. Check readiness:

```bash
docker compose ps
docker compose logs -f postgres
```

PostgreSQL also writes logs to the host directory configured by `POSTGRES_LOG_DIR`.

5. Connect from Windows, WSL, or local tools:

```bash
psql "host=localhost port=5432 user=postgres password=local-dev-password dbname=postgres"
```

## Networking notes

- Windows applications can use `localhost:5432`.
- WSL applications can use `localhost:5432`.
- Dockerized applications attached to the same Compose network can use host `postgres` and port `5432`.

## Lifecycle commands

Start:

```bash
docker compose up -d
```

Stop:

```bash
docker compose down
```

Stop and remove the named volume:

```bash
docker compose down -v
```

## Log files

- PostgreSQL writes to `${POSTGRES_LOG_DIR}/postgresql.log` via a host bind mount.
- PostgreSQL 18 writes file logs as container user/group `999:999` in this stack.
- Log rotation is intentionally left to external host-side tooling such as `logrotate`.
- A sample Linux `logrotate` config is provided at `postgres-dev/logrotate-postgres-dev.conf`.
- Docker still captures container stdout/stderr, so `docker compose logs -f postgres` remains useful for quick inspection.

Example host-side ownership fix for a Linux log directory:

```bash
sudo mkdir -p /srv/logs/postgres
sudo chown 999:999 /srv/logs/postgres
```

Preferred installation on a Linux host:

```bash
sudo cp /path/to/calypso_pgvault/postgres-dev/logrotate-postgres-dev.conf /etc/logrotate.d/postgres-dev
sudo chown root:root /etc/logrotate.d/postgres-dev
sudo chmod 644 /etc/logrotate.d/postgres-dev
sudo logrotate -f /etc/logrotate.d/postgres-dev
```

The sample config sends `USR1` to the container after rotation so PostgreSQL reopens the log file cleanly. `logrotate` expects root-owned config when run with `sudo`, so installing the sample under `/etc/logrotate.d/` is the preferred approach. Adjust the `create` ownership and log path if you override `POSTGRES_LOG_DIR`.

## Security notes

- `.env.example` is safe to commit.
- Do not commit a real `.env` with non-development credentials.
- This setup intentionally uses a Docker named volume instead of a Windows bind mount to avoid filesystem permission and performance issues common with Windows-hosted database files.
