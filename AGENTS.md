# AGENTS.md — Winnow Dev Agent Operating Procedures

You are a dev agent working on the Winnow codebase (Go API + React/Vite frontend).
This file tells you how to work here. Read it fully before doing anything else.

---

## Your First Three Steps (always, no exceptions)

1. **Start a Winnow session**
2. **Read the task spec (from Forge)**
3. **Search Winnow for existing context on the task**

Only then start writing code.

---

## 1. Start a Winnow Session

Winnow is the shared memory layer for this team. Every dev session starts and ends with it.

```
winnow_start_session(
  agent_id="opencode-<your-session-slug>",
  project_id="1651f741-6127-4653-9486-149d16028277",
  lifecycle_slug="dev"
)
```

Save the returned `session_id` — you'll need it throughout.

Then immediately register your focus:

```
winnow_register_focus(
  session_id="<session_id>",
  focus_task="<ticket ID>: <ticket title>"
)
```

---

## 2. Read the Task Spec

**In our setup**, specs come from Forge (our project management tool) via the forge MCP:

```
forge_get_task(taskId="<ticket-id>")
```

The ticket description is the spec. Read it fully. Extract the key concepts and decisions
before moving to step 3.

---

## 3. Search Winnow for Existing Context

Now that you know what you're building, search for prior decisions and conventions:

```
winnow_search_context(
  query="<key concept from the spec>",
  project_id="1651f741-6127-4653-9486-149d16028277"
)
```

Run searches. Read the returned chunks — they contain architecture decisions, conventions,
and prior design work that must inform your implementation.
Don't rediscover what the team already decided.

---

## Writing Code

### Branch naming
```
feat/<ticket-id-lowercase>-<short-description>
# e.g. feat/wnw-68-agent-types
```

Always branch from `main`. Pull latest before branching.

### Stack
- **Go 1.23** — API server (`internal/`)
- **PostgreSQL** — migrations in `internal/db/migrations/` (sequential: `NNN_name.up.sql` / `NNN_name.down.sql`)
- **React/Vite/TypeScript** — frontend (`winnow-ui/` repo, separate)
- **pgvector** — embeddings on `context_chunks`

### Conventions
- All API handlers in `internal/api/`
- Models in `internal/models/models.go`
- MCP tools in `internal/mcp/`
- New routes wired in `internal/api/router.go` under the appropriate auth group
- Write at least one test for every new handler or MCP tool
- `go build ./...` and `go test ./...` must be green before opening a PR

### Build check
```bash
go build ./...
go vet ./...
go test ./... -race -timeout 60s
```

---

## Write to Winnow As You Build

This is not optional. Write chunks as you make decisions — not just at the end.

**Use the right tool for the right content:**

| What you're writing | Tool |
|---------------------|------|
| Architecture or design decision made during this work | `winnow_write_knowledge` |
| A convention this codebase follows (discovered or established) | `winnow_write_convention` |
| Something personal you learned that will help you next time | `winnow_write_memory` |

**Do not use `winnow_write_context`** — it's deprecated. Use the typed tools above.

Example — after deciding how to handle global presets:
```
winnow_write_knowledge(
  project_id="1651f741-6127-4653-9486-149d16028277",
  query_key="agent-types-global-preset-pattern",
  title="Agent Types: Global Presets Are Immutable",
  content="Global presets (dev, admin, research, orchestrator) have org_id=NULL.
  The API enforces immutability at the handler level — PATCH and DELETE return 403
  for any type with org_id=NULL. Org-specific types are fully CRUD-able."
)
```

Write one chunk per meaningful decision. Don't batch everything into one chunk at the end.

---

## Open the PR

```bash
gh pr create \
  --title "feat(<ticket-id-lowercase>): <description>" \
  --body "## Summary\n\n<what you built>\n\n## Testing\n\n<what you ran>\n\n## Migration Impact\n\n<if any>"

gh pr edit --add-reviewer parker-xferops,adam-xferops-ai
```

Always request review from `parker-xferops`.

Then update the Forge ticket with the PR link and move it to Code Review.

---

## End Your Session

When the PR is open:

```
winnow_end_session(session_id="<session_id>")
```

Review the returned MEMORY chunks. For each one, decide:
- **Keep** — useful, leave it as-is
- **Promote** — elevate to PROJECT KNOWLEDGE (call `winnow_write_knowledge` with the content)
- **Discard** — noise, ignore it

This is how institutional knowledge compounds across agents and sessions.

---

## Key IDs

| Thing | ID |
|-------|----|
| Winnow product project | `1651f741-6127-4653-9486-149d16028277` |
| Forge project (Winnow board) | `cmmhg1y1f0001le01gkx2a3sk` |
| Lifecycle to use | `dev` |

---

## The Principle

The prompt that kicked off your session is just a door opener.
Everything else — the spec, the conventions, the prior decisions — lives in the task tracker and Winnow.
Read those first. Code second.
