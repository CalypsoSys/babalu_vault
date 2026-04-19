# Postgres Dev + pgdrivebackup

This repository contains:

- `postgres-dev/`: a shared local PostgreSQL 18 Docker Compose setup intended for Windows, WSL, and Docker Desktop workflows.
- `pgdrivebackup/`: a Go command-line utility that backs up one or more PostgreSQL databases to local storage and applies rotating retention rules.

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
2. Read `pgdrivebackup/README.md`.

## Make Targets

Common development commands are available at the repo root:

```bash
make build
make test
make run
make dry-run
```

`make build` writes the binary to `./bin/pgdrivebackup`.
