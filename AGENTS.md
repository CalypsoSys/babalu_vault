# Repository Guidelines

## Project Structure & Module Organization

This repository has two working areas:

- repo-root Go module: backup and notifier executables live under `cmd/`, with supporting packages under `internal/`.
- `postgres-dev/`: local PostgreSQL 18 Docker Compose environment for development. Use `docker-compose.yml` as the baseline.

Keep new Go code inside `internal/...` unless it is part of the CLI surface. Keep sample or local-only config under the repo-root `configs/` directory.

## Build, Test, and Development Commands

- `go test ./...`: run all Go unit tests.
- `go build ./cmd/babalu-vault`: build the backup CLI locally.
- `go run ./cmd/babalu-vault list --config configs/example.yaml`: verify config parsing and command wiring.
- `cd postgres-dev && docker compose up -d`: start the local PostgreSQL container.
- `cd postgres-dev && docker compose down`: stop the local database.

Use `go run ./cmd/babalu-vault backup --config configs/example.yaml --dry-run` before testing real uploads or retention behavior.

## Coding Style & Naming Conventions

Go code should stay `gofmt`-formatted and follow standard Go naming: exported identifiers in `CamelCase`, internal helpers in `camelCase`, and concise package names. Prefer small focused packages over cross-package utility files. Test files should sit beside the code they cover and use names like `config_test.go` or `retention_test.go`.

YAML config keys should remain lowercase with underscores, matching `configs/example.yaml`.

## Testing Guidelines

Use Go's built-in `testing` package. Cover config loading, filename handling, filesystem retention behavior, and backup edge cases with table-driven tests where practical. Run `go test ./...` before opening a PR. For Docker-backed workflows, validate against `postgres-dev/` separately; do not make unit tests depend on a running container.

## Commit & Pull Request Guidelines

This branch has no commit history yet, so use a simple imperative commit style such as `Add dry-run retention test` or `Document Docker setup`. Keep commits focused. PRs should include a short summary, test evidence (`go test ./...`, relevant `docker compose` checks), and screenshots only when a UI is introduced.

## Security & Configuration Tips

Do not commit real `.env` files, OAuth credentials, refresh tokens, or database passwords. Keep secrets in environment variables such as `LOCALDEV_POSTGRES_PASSWORD` and `BABALU_VAULT_ENCRYPTION_PASSWORD`.
Do not commit PII, secrets, passwords, tokens, or machine-specific settings.
Make sure code follows security best practices for handling sensitive data, including avoiding unsafe storage or logging of PII, passwords, secrets, and related credentials.

## Agent-Specific Instructions

In Codex CLI workflows, adopt the convention that any user command prefixed with `>>>` is meant to add or update instructions, not to be executed as a shell command. Record and apply the text that follows as new working guidance for the current task or session, and update `AGENTS.md` when the instruction is intended to persist for future work in this repository.
For Post-Change Deliverables, always ask whether the user is ready for commit, PR, and ticket outputs before producing them, and only provide whichever deliverables they affirmatively want. Post-Change Deliverables should include commit message format, PR description template, and Jira ticket description template.
