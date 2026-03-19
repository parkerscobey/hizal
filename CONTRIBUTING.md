# Contributing to Hizal

Thanks for your interest in contributing to Hizal! This guide will help you get started.

## Getting Started

### Prerequisites

- **Go 1.23+**
- **PostgreSQL 16** with [pgvector](https://github.com/pgvector/pgvector) extension
- **OpenAI API key** (for embeddings — `text-embedding-3-small`)

### Local Development

```bash
# Clone the repo
git clone https://github.com/XferOps/winnow.git
cd hizal

# Copy env template
cp .env.example .env
# Edit .env with your database URL and OpenAI API key

# Run migrations
make migrate

# Start the server
make dev

# Run tests
make test
```

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | ✅ | PostgreSQL connection string |
| `OPENAI_API_KEY` | ✅ | For text-embedding-3-small |
| `PORT` | No | Server port (default: 8080) |
| `APP_BASE_URL` | No | Frontend URL for CORS |

## How to Contribute

### Reporting Issues

- Check existing issues first to avoid duplicates
- Include steps to reproduce, expected behavior, and actual behavior
- Include Go version, OS, and database version if relevant

### Pull Requests

1. **Fork and branch** — Create a feature branch from `main`
2. **One thing per PR** — Keep changes focused and reviewable
3. **Write tests** — All new features and bug fixes need tests
4. **Run CI locally** — `make test` and `go build ./...` must pass
5. **Commit messages** — Use conventional commits: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`

### PR Title Format

```
feat: add chunk_type filtering to search_context
fix: resolve org-scoped writes using wrong scope
docs: update MCP tools reference for v0.2
```

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `internal/models` for database-backed types
- Keep handler logic thin — business logic belongs in service packages
- Error messages should be lowercase and descriptive

### Database Changes

- All schema changes go through `internal/db/migrations/`
- Use sequential migration numbering
- Every new table/column must be reflected in `internal/models`
- Include both up and down migrations

### MCP Tool Changes

- Tool schemas are defined in `internal/mcp/server.go`
- Update `docs/03-mcp-tools.md` when adding or changing tools
- Update `internal/api/agent_onboarding_guide.go` if the change affects onboarding
- Update relevant skills in `skills/` if workflow guidance changes

## Architecture

```
cmd/server/          — Server entrypoint
internal/
  api/               — HTTP handlers, middleware, CORS
  auth/              — API key generation and validation
  db/                — Database connection and migrations
  embeddings/        — OpenAI embedding client
  email/             — Email templates and sending
  mcp/               — MCP server (HTTP+SSE transport)
  models/            — Database-backed types (canonical)
  seed/              — Auto-seeding from GitHub repos
skills/              — Agent skill definitions (SKILL.md files)
docs/                — Documentation
```

### Key Design Decisions

- **No server-side LLM** — All summarization happens client-side (agent does the thinking)
- **pgvector for search** — Semantic search using `text-embedding-3-small` embeddings
- **Three scopes** — PROJECT (shared), AGENT (private), ORG (org-wide)
- **always_inject** — Chunks that are surfaced automatically as ambient context
- **Purpose-built write tools** — Tool name communicates intent and routes to correct scope

## Community

- **Issues:** [github.com/XferOps/winnow/issues](https://github.com/XferOps/winnow/issues)
- **Discussions:** [github.com/XferOps/winnow/discussions](https://github.com/XferOps/winnow/discussions)

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](./LICENSE).
