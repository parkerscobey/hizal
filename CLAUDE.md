# CLAUDE.md — Winnow (Contextor) Codebase Guide

## What is This?
Winnow is a context management API for AI agents. It stores semantic context chunks with vector embeddings, enabling agents to search, retrieve, version, and maintain a persistent knowledge base across sessions.

**Live:** https://winnow-api.xferops.dev (API) | https://winnow.xferops.dev (UI)

## Quick Start

```bash
# Run locally
make dev

# Run tests
make test

# Build
make build
```

## Project Structure
```
cmd/         — entrypoints (server, etc.)
internal/    — core business logic
  context/   — context CRUD, embeddings, versioning
  search/    — vector search implementation
  auth/      — API key validation
server/      — HTTP handlers, MCP server
docs/        — API documentation
```

## MCP Server Setup
Winnow exposes an MCP server for agent tooling. To configure:
```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": { "Authorization": "Bearer dk_live_YOUR_KEY_HERE" }
    }
  }
}
```

## Available MCP Tools

| Tool | Description |
|------|-------------|
| `write_context` | Store a new context chunk with embedding |
| `search_context` | Semantic vector search over chunks |
| `read_context` | Fetch a chunk by ID |
| `update_context` | Update chunk content (creates new version) |
| `get_context_versions` | View full version history of a chunk |
| `compact_context` | Bulk-fetch chunks for summarization |
| `review_context` | Rate a chunk's usefulness/correctness (1–5) |
| `delete_context` | Remove a chunk permanently |

## Agent Workflow Patterns

### Research Workflow (`skills/winnow-research/`)
Search before you write. Never create duplicate chunks.
```
search_context → read top results → gather new info → write_context
```

### Compaction Workflow (`skills/winnow-compact/`)
Merge related/redundant chunks into one high-quality summary.
```
search_context → compact_context → summarize → write_context → delete/update originals
```

### Onboarding Workflow (`skills/winnow-onboard/`)
Build a mental model from Winnow before touching code.
```
search overview → read key chunks → explore domains → synthesize mental model
```

### Planning Workflow (`skills/winnow-plan/`)
Review context, write a plan, save it, update as work evolves.
```
search constraints/decisions → draft plan → validate → write_context → update as needed
```

### Review Workflow (`skills/winnow-review/`)
Audit chunk quality and remove stale/incorrect context.
```
search topic → read chunks → review_context(rating) → update/delete low-quality chunks
```

## Coding Conventions

### API Keys
- Format: `dk_live_{org}_{random}` (prod) or `dk_test_{org}_{random}` (test)
- Never hardcode — use `WINNOW_API_KEY` env var
- Project scoping: all operations require `projectId`

### Context Chunks
- **Size:** 100–600 words per chunk
- **Tags:** Always include at minimum `[type, topic]` — e.g., `["research", "auth"]`
- **Source:** Include URL or file path for traceability
- **Focus:** One concept per chunk

### Tag Conventions
```
research      — gathered information, findings
plan          — task or feature plans
decision      — architectural or design decisions
convention    — coding standards, patterns, style
onboarding    — project overview and mental model docs
compacted     — merged/summarized from multiple chunks
```

### Go Patterns
```go
// Use context.Context for all API calls
result, err := client.SearchContext(ctx, &SearchRequest{
    ProjectID: cfg.ProjectID,
    Query:     query,
    Limit:     10,
})
if err != nil {
    return fmt.Errorf("search_context: %w", err)
}

// Check for existing chunk before writing
if len(result.Chunks) > 0 && result.Chunks[0].Score > 0.85 {
    // Update existing chunk instead
    return client.UpdateContext(ctx, result.Chunks[0].ID, newContent)
}
```

## Testing
- Use `dk_test_*` keys for all tests
- Test project IDs start with `test_`
- Run against local server or staging — never prod

## Common Mistakes to Avoid
1. **Writing without searching first** — always check for existing chunks
2. **Hardcoding project IDs** — use config/env vars
3. **Giant chunks** — keep them focused and under 600 words
4. **No tags** — untagged chunks are hard to find later
5. **Never reviewing** — stale context degrades agent performance

## Skills (OpenClaw)
Agent skill packages live in `skills/`. Each has a `SKILL.md` with full workflow instructions:
- `skills/winnow-research/` — research + write
- `skills/winnow-compact/` — merge + clean up
- `skills/winnow-onboard/` — project onboarding
- `skills/winnow-plan/` — task planning
- `skills/winnow-review/` — quality review

## Cursor Rules
Cursor-specific rules in `.cursor/rules/winnow.mdc`.
