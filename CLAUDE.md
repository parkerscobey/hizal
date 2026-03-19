# CLAUDE.md — Hizal Development Guide

## What is This?

Hizal is a behavior-driven memory API for AI agents. Go backend with PostgreSQL + pgvector for semantic search, MCP server over HTTP+SSE for agent tooling.

## Quick Start

```bash
cp .env.example .env   # configure DATABASE_URL + OPENAI_API_KEY
make migrate
make dev               # starts on :8080
make test
```

## Project Structure

```
cmd/server/          — Server entrypoint
internal/
  api/               — HTTP handlers, middleware, CORS, onboarding guide
  auth/              — API key generation, validation, scoping
  db/                — Database connection, migrations
  embeddings/        — OpenAI embedding client (text-embedding-3-small)
  email/             — Email templates and sending
  mcp/               — MCP server (HTTP+SSE transport, all tool handlers)
  models/            — Database-backed types (canonical package)
  seed/              — Auto-seeding from GitHub repos
skills/              — Agent skill definitions (SKILL.md files)
docs/                — Documentation
```

## Key Architecture Decisions

- **No server-side LLM** — All summarization happens client-side
- **pgvector** — Semantic search with text-embedding-3-small embeddings
- **Three scopes** — PROJECT (shared), AGENT (private), ORG (org-wide)
- **always_inject** — Chunks surfaced automatically as ambient context
- **Purpose-built write tools** — Tool name communicates intent, routes to correct scope
- **Session lifecycle** — start_session / register_focus / end_session
- **Agent types** — dev, admin, research, orchestrator (controls tool visibility)
- **Chunk types** — KNOWLEDGE, MEMORY, CONVENTION, IDENTITY, PRINCIPLE, DECISION, RESEARCH, PLAN, SPEC, IMPLEMENTATION, CONSTRAINT, LESSON

## MCP Tools (implemented in internal/mcp/server.go)

### Session lifecycle
`start_session`, `resume_session`, `get_active_session`, `register_focus`, `end_session`

### Purpose-built writes
`write_identity` (AGENT, always_inject), `write_memory` (AGENT), `write_knowledge` (PROJECT), `write_convention` (PROJECT, always_inject), `write_org_knowledge` (ORG), `store_principle` (ORG, always_inject)

### Read/search
`search_context`, `read_context`, `get_context_versions`, `compact_context`

### Other
`update_context`, `delete_context`, `review_context`, `write_context` (deprecated)

### Admin (orchestrator/admin types only)
`list_projects`, `list_agents`, `create_project`, `add_agent_to_project`, `remove_agent_from_project`

## Database Model Contract

- `internal/models` is the canonical package for database-backed types
- Every table/column in migrations must be reflected in `internal/models`
- Scan rows into `internal/models` types first
- Keep transport/join structs local to the serving package

## Coding Conventions

- Standard Go style (`gofmt`, `go vet`)
- Error messages lowercase and descriptive
- All schema changes via `internal/db/migrations/` (sequential numbering, up + down)
- API key format: `ctx_{org}_{random}`
- All context operations require project/agent/org scoping
- Tests use `go test ./...`

## Common Patterns

```go
// Check for existing chunk before writing
results := searchContext(ctx, query, projectID)
if len(results) > 0 && results[0].Score > 0.85 {
    // Update existing instead of creating duplicate
    updateContext(ctx, results[0].ID, newContent)
}
```

## Skills

Agent workflow packages in `skills/`:
- `hizal-seed` — populate empty projects
- `hizal-research` — investigate + write knowledge
- `hizal-plan` — create validated implementation plans
- `hizal-compact` — compress noisy context
- `hizal-review` — rate and improve quality
- `hizal-onboard` — fast project orientation
