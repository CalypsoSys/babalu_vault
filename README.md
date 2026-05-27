# Postgres Dev + Backup Tools

This repository contains:

- `postgres-dev/`: a shared local PostgreSQL 18 Docker Compose setup intended for Windows, WSL, and Docker Desktop workflows.
- `babalu-vault`: a Go command-line utility that backs up configured PostgreSQL, MySQL, and file-set targets to local storage and applies rotating retention rules.
- `codex-slack-notify`: a Go helper that posts Codex completion or attention alerts to a Slack incoming webhook.

## Backup Client Tools

`babalu-vault` uses local client tools only for local TCP backup sources. PostgreSQL TCP backups need `pg_dump` and `psql`; MySQL TCP backups need `mysqldump`.

Common installation options:

- Windows: install PostgreSQL from the official installer and include the client tools, then ensure the `bin` directory containing `pg_dump.exe` is on `PATH`.
- Debian/Ubuntu: `sudo apt install postgresql-client mysql-client`
- macOS with Homebrew: `brew install libpq` and, if needed, add the Homebrew `libpq/bin` directory to `PATH`

Quick verification:

```bash
pg_dump --version
psql --version
mysqldump --version
```

If you use only Docker or SSH-backed backup targets, local database client tools are not required for those targets. The command runs inside the configured container or on the remote host.

Start with:

1. Read `postgres-dev/README.md`.
2. Read [README-babalu-vault.md](README-babalu-vault.md).
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

`make build` writes binaries to `./bin/babalu-vault`, `./bin/codex-slack-notify`, and `./bin/codex-slack-notify.exe`.

## Secret Scanning

This repo includes a baseline `gitleaks` setup:

- Local scan: `make gitleaks`
- Config: [`.gitleaks.toml`](.gitleaks.toml)
- Pre-commit hook: [`.pre-commit-config.yaml`](.pre-commit-config.yaml)
- CI workflow: [`.github/workflows/gitleaks.yml`](.github/workflows/gitleaks.yml)

The local target expects the `gitleaks` CLI to already be installed and scans git history with redacted output.

To enable the pre-commit hook locally:

```bash
pre-commit install
```

The hook uses the official `gitleaks` pre-commit integration and runs on staged changes before each commit.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
