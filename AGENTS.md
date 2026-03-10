# Repository Guidelines

## Project Structure & Module Organization
- `cmd/server/` contains the API entrypoint (`main.go`).
- `internal/` holds application packages: `api/` (HTTP handlers/middleware), `mcp/` (tool logic), `db/` (migrations/seeds/connection), `embeddings/`, `auth/`, and `models/`.
- `internal/db/migrations/` stores schema migrations; `internal/db/seeds/` stores local seed data.
- `docs/` contains architecture and workflow documentation.
- `.github/workflows/` defines CI (`ci.yml`) and deployment (`deploy.yml`).

## Build, Test, and Development Commands
- `make build`: compile server to `./bin/server`.
- `make run`: build and run the API locally.
- `make test`: run all Go tests with race detector (`go test ./... -v -race -timeout 60s`).
- `make migrate-up` / `make migrate-down`: apply or roll back DB migrations.
- `make seed`: load local seed data.
- `docker compose up -d`: start local PostgreSQL + pgvector.

Typical local flow:
```bash
cp .env.example .env
docker compose up -d
make migrate-up
make seed
make run
```

## Coding Style & Naming Conventions
- Use standard Go formatting and idioms; run `gofmt` before committing.
- Keep package boundaries clear: reusable domain logic in `internal/*`, process startup only in `cmd/server`.
- Use descriptive, lowercase package names and `CamelCase` exported identifiers.
- Prefer explicit error wrapping (`fmt.Errorf("...: %w", err)`) and early returns.

## Testing Guidelines
- Framework: Go standard `testing` package.
- Place tests in `*_test.go` files next to implementation.
- Prefer table-driven tests for handlers/tool behavior.
- For DB-dependent tests, use `DATABASE_URL` (CI uses Postgres `winnow_test`).
- Ensure `make test` and `go vet ./...` pass before opening a PR.

## Commit & Pull Request Guidelines
- Follow Conventional Commit style seen in history: `feat:`, `fix:`, `chore:`, `docs:`.
- Include scope/context when helpful (example: `fix: handle NULL source_file in search scan`).
- Reference ticket IDs when available (for example, `(CTX-8)`).
- PRs should include: concise summary, testing performed, migration/seed impact, and any config changes.

## Security & Configuration Tips
- Never commit secrets; keep real credentials only in local `.env`.
- Rotate exposed API keys immediately.
- Validate `.env` values match `docker-compose.yml` (port/database/user) to avoid migration issues.
