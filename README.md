# Winnow

**Context management for AI coding agents.** Winnow keeps your agents in the "smart zone" — efficient, focused context instead of an overwhelming wall of text.

- 🔗 **API:** https://winnow-api.xferops.dev
- 🖥️ **UI:** https://winnow.xferops.dev
- 📖 **Docs:** [`docs/`](./docs/)

---

## The Problem

AI coding agents struggle in large codebases not because models are dumb — it's because context is poorly managed. Winnow fixes this:

| Problem | Winnow Solution |
|---------|----------------|
| Agents forget what they learned | Write findings as searchable context chunks |
| Context window fills up | Compaction resets state without losing knowledge |
| Knowledge goes stale | Versioned updates with full history |
| Low-quality context accumulates | Review system flags and rates chunks |

---

## Quickstart (5 minutes)

### 1. Get an API key

```bash
curl -X POST https://winnow-api.xferops.dev/v1/keys \
  -H "Content-Type: application/json" \
  -d '{"org_slug": "your-org"}'
```

Returns: `{ "key": "ctx_your-org_..." }` — save this, it's only shown once.

### 2. Configure your MCP client

**Claude Desktop / Cursor / OpenClaw** — add to your MCP config:

```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer ctx_your-org_YOUR_KEY_HERE"
      }
    }
  }
}
```

### 3. Use it in your agent

```
search_context("how does authentication work")
→ returns relevant chunks ranked by semantic similarity

write_context(
  query_key="auth-flow",
  title="JWT verification in middleware",
  content="The auth middleware validates JWTs by...",
  source_file="internal/auth/middleware.go",
  source_lines=[42, 67]
)
→ stores with embedding for future retrieval
```

That's it. Your agent now has persistent, searchable memory.

---

**Researching / Designing** — Defining the product before implementation

All tools require `Authorization: Bearer <key>` header (configured once in your MCP client).

| Tool | What it does |
|------|-------------|
| `write_context` | Store a research finding or knowledge chunk |
| `search_context` | Semantic search across all chunks |
| `read_context` | Retrieve a specific chunk with version history |
| `update_context` | Versioned update to an existing chunk |
| `get_context_versions` | View full version history |
| `compact_context` | Fetch chunks for agent-side compaction |
| `review_context` | Rate chunk quality (usefulness + correctness) |
| `delete_context` | Remove a chunk |

→ Full tool reference: [`docs/mcp-guide.md`](./docs/mcp-guide.md)

---

## REST API

Same auth, same data — REST alternative to MCP.

```
POST   /v1/context            # write
GET    /v1/context/search     # search
GET    /v1/context/compact    # compact
GET    /v1/context/:id        # read
PATCH  /v1/context/:id        # update
DELETE /v1/context/:id        # delete
GET    /v1/context/:id/versions  # version history
POST   /v1/context/:id/review    # review
GET    /health                # health check
```

→ Full API reference: [`docs/api-reference.md`](./docs/api-reference.md)

---

## How We Use It (and How You Might)

Winnow handles memory and context. It doesn't replace your task tracker or your orchestrator — it works alongside them.

**Our setup at XferOps:**

1. A long-running orchestrator agent (OpenClaw) receives a task from a human via Telegram
2. The orchestrator looks up the spec in Forge (our project management tool) and passes the ticket ID to a dev agent (OpenCode)
3. The dev agent starts a Winnow session, reads the Forge ticket, searches Winnow for relevant context, then implements and opens a PR
4. At the end of the session, the dev agent calls `end_session` — Winnow returns the MEMORY chunks written during the session for review and promotion

**The specification layer is deliberately generic.** Winnow doesn't care where your task spec comes from:

| Source | How it works |
|--------|-------------|
| **Forge / Linear / Jira** | Orchestrator or human passes a ticket ID; agent reads spec via MCP or API |
| **Orchestrator prompt** | Orchestrator writes a detailed prompt directly; agent extracts task and search hints from it |
| **Winnow chunk** | Orchestrator writes a KNOWLEDGE or DECISION chunk as the spec; agent searches for it by query key |
| **Plain file** | A spec.md or similar; works fine, though it trades Winnow's persistence for simplicity |

The pattern is always the same: **Winnow provides memory and context; something else provides the task definition.** See [`AGENTS.md`](./AGENTS.md) for how we structure the dev agent workflow.

---

## Core Concepts

**Context Chunk** — A small, composable unit of knowledge. Has a title, content, source location, gotchas, and related chunk IDs.

**Query Key** — A namespace tag on chunks (e.g. `"auth-flow"`, `"database-schema"`). Used to scope searches.

**Compaction** — Fetch a set of related chunks and have your agent summarize them into a single new chunk. Resets working context without losing knowledge.

**Smart Zone** — Keeping your context window under ~40% full. Compaction is the main tool to stay there.

**RPI Workflow** — Research → Plan → Implement. Use Winnow during the Research phase to accumulate knowledge, then compact before implementation.

---

## Documentation

| Doc | Contents |
|-----|---------|
| [`docs/api-reference.md`](./docs/api-reference.md) | Full REST API with request/response examples |
| [`docs/mcp-guide.md`](./docs/mcp-guide.md) | MCP connection guide + all tool schemas |
| [`docs/06-agent-onboarding.md`](./docs/06-agent-onboarding.md) | How to onboard a Winnow-connected agent to this application |
| [`docs/workflows.md`](./docs/workflows.md) | Example agent workflows |

---

## Development

**v0.1** — Production API live at `winnow-api.xferops.dev`. Self-service key management UI coming in v0.2.

### Database Model Contract

- `internal/models` is the canonical package for database-backed types.
- Every table and column introduced by `internal/db/migrations/` must be reflected in `internal/models`.
- When API handlers or other packages scan rows from persisted tables, prefer scanning into `internal/models` types first.
- Keep join, aggregate, and transport-only response structs local to the package that serves them; do not add those shapes to `internal/models`.
