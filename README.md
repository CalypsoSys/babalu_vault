# Postgres Dev + Backup Tools

This repository contains:

- `postgres-dev/`: a shared local PostgreSQL 18 Docker Compose setup intended for Windows, WSL, and Docker Desktop workflows.
- `pgdrivebackup`: a Go command-line utility that backs up one or more PostgreSQL databases to local storage and applies rotating retention rules.
- `codex-slack-notify`: a Go helper that posts Codex completion or attention alerts to a Slack incoming webhook.

## PostgreSQL Client Tools

`pgdrivebackup` uses the local PostgreSQL client tools when you configure a server with `type: tcp`. In practice, that means `pg_dump` must be installed and available on `PATH`.

Common installation options:

- Windows: install PostgreSQL from the official installer and include the client tools, then ensure the `bin` directory containing `pg_dump.exe` is on `PATH`.
- Debian/Ubuntu: `sudo apt install postgresql-client`
- macOS with Homebrew: `brew install libpq` and, if needed, add the Homebrew `libpq/bin` directory to `PATH`

Quick verification:

```bash
pg_dump --version
```

If you use only `type: docker` or `type: ssh`, local PostgreSQL client tools are not required for those backup targets.

Start with:

1. Read `postgres-dev/README.md`.
2. Read [README-pgdrivebackup.md](README-pgdrivebackup.md).
3. Read [README-codex-slack-notify.md](README-codex-slack-notify.md) if you want Slack alerts for Codex.

## Make Targets

Common development commands are available at the repo root:

```bash
make build
make test
make run
make dry-run
make gitleaks
```

`make build` writes binaries to `./bin/pgdrivebackup`, `./bin/codex-slack-notify`, and `./bin/codex-slack-notify.exe`.

## Secret Scanning

This repo includes a baseline `gitleaks` setup:

- Local scan: `make gitleaks`
- Config: [`.gitleaks.toml`](/home/joe/code/calypso_pgvault/.gitleaks.toml)
- Pre-commit hook: [`.pre-commit-config.yaml`](/home/joe/code/calypso_pgvault/.pre-commit-config.yaml)
- CI workflow: [`.github/workflows/gitleaks.yml`](/home/joe/code/calypso_pgvault/.github/workflows/gitleaks.yml)

The local target expects the `gitleaks` CLI to already be installed and scans git history with redacted output.

To enable the pre-commit hook locally:

```bash
pre-commit install
```

The hook uses the official `gitleaks` pre-commit integration and runs on staged changes before each commit.
