# AGENTS.md — Hizal Dev Agent Operating Procedures

You are a dev agent working on the Hizal codebase (Go API + React/Vite frontend).
This file tells you how to work here. Read it fully before doing anything else.

Hizal is both the product you're building and the memory system you use to build it.
Everything — specs, decisions, conventions, lessons — lives in Hizal.

---

## Your First Three Steps (always, no exceptions)

1. **Start a Hizal session**
2. **Read the task spec (from Hizal)**
3. **Search Hizal for existing context on the task**

Only then start writing code.

---

## 1. Start a Hizal Session

Every dev session starts and ends with Hizal.

```
start_session(lifecycle_slug="dev")
```

This returns a `session_id`. Keep it visible — you'll need it for `register_focus` and `end_session`.

Then register what you're working on:

```
register_focus(
  session_id="<session-id>",
  task="WNW-XX: <ticket title>",
  project_id="<project-id>"
)
```

### Session Recovery

If you lose your `session_id` (context reset, compaction):

```
get_active_session()
```

- `status="active"` → use the returned `session_id`, call `resume_session` to extend TTL
- `status="none"` → call `start_session` to begin fresh

---

## 2. Read the Task Spec

Specs are Hizal chunks with `chunk_type=SPEC`. Find your assigned work:

```
search_context(query="spec status TODO", project_id="<project-id>")
search_context(query="spec BUG", project_id="<project-id>")
```

Pick the highest-priority unblocked spec. Read it fully:

```
read_context(query_key="spec-wnw-XX-short-name", project_id="<project-id>")
```

The spec is your source of truth. Extract key concepts and decisions before moving to step 3.

### Spec Chunk Format

```
query_key: spec-wnw-XX-short-description
chunk_type: SPEC
title: "WNW-XX: Human-readable title"

**Priority:** CRITICAL | HIGH | MEDIUM | LOW
**Status:** TODO | IN_PROGRESS | CODE_REVIEW | DONE | BLOCKED
**Type:** BUG | FEATURE | CHORE
**Repo:** hizal | hizal-ui
**Depends on:** spec-wnw-YY (or "none")
**Assigned:** <agent-name> | unassigned
**PR:** <url when created>

## Description
What needs to be built or fixed.

## Acceptance Criteria
- [ ] Criterion 1
- [ ] Criterion 2

## Gotchas
(Enriched after failed attempts or discoveries)
```

---

## 3. Search Hizal for Existing Context

Now that you know what you're building, search for prior decisions and conventions:

```
search_context(query="<key concept from the spec>", project_id="<project-id>")
```

Run 2-3 searches with different phrasings. Read the returned chunks — they contain
architecture decisions, conventions, and prior work that must inform your implementation.

Don't rediscover what the team already decided.

---

## Writing Code

### Branch first, always

Before writing a single line of code:

```bash
git checkout main && git pull
git checkout -b feat/<ticket-id-lowercase>-<short-description>
# e.g. feat/wnw-68-agent-types
```

**Never commit directly to main.** If you realize you've committed to main, stop —
create a branch from your current HEAD and reset main before pushing.

### Stack

- **Go 1.23+** — API server (`internal/`)
- **PostgreSQL 16** with pgvector — embeddings on `context_chunks`
- **Migrations** in `internal/db/migrations/` (sequential: `NNN_name.up.sql` / `NNN_name.down.sql`)

### Conventions

- API handlers in `internal/api/`
- Models in `internal/models/models.go` (canonical package for DB types)
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

## Write to Hizal As You Build

This is not optional. Write chunks as you make decisions — not just at the end.

| What you're writing | Tool | Scope |
|---------------------|------|-------|
| Architecture or design decision | `write_knowledge` | PROJECT |
| Convention this codebase follows | `write_convention` | PROJECT (always_inject) |
| Something personal you learned | `write_memory` | AGENT |

**Do not use `write_context`** — it's deprecated. Use the purpose-built tools above.

Write one chunk per meaningful decision. Don't batch everything into one chunk at the end.

---

## Open the PR

**Your session is not complete until a PR exists.** Tests passing and code written is not done.
Done means: branch pushed, PR open, reviewers requested.

```bash
gh pr create \
  --title "feat(wnw-XX): <description>" \
  --body "## Summary\n\n<what you built>\n\nCloses WNW-XX\n\n## Testing\n\n<what you ran>"

gh pr edit --add-reviewer parker-xferops
```

Always request review from `parker-xferops`.

After pushing fixes to address review feedback, **re-request review**:

```bash
gh api repos/XferOps/<repo>/pulls/<PR#>/requested_reviewers \
  -X POST -f 'reviewers[]=parker-xferops'
```

---

## Update the Spec

After opening the PR, update the spec chunk with status and PR link:

```
update_context(
  query_key="spec-wnw-XX-short-name",
  project_id="<project-id>",
  content="<spec content with Status: CODE_REVIEW and PR: <url>>",
  change_note="PR opened: <url>"
)
```

---

## End Your Session

After the PR is open and the spec is updated:

```
end_session(session_id="<session-id>")
```

Review the returned MEMORY chunks. For each one, decide:
- **Keep** — useful personal observation, leave as AGENT memory
- **Promote** — valuable for the team, call `write_knowledge` with the content
- **Discard** — noise, ignore it

This is how knowledge compounds across agents and sessions.

---

## Creating New Specs

When you discover work that needs doing (bugs, improvements, missing features):

```
write_chunk(
  project_id="<project-id>",
  query_key="spec-wnw-XX-short-description",
  title="WNW-XX: Title",
  chunk_type="SPEC",
  content="<spec in the format above>"
)
```

Search existing specs to find the next available number:

```
search_context(query="spec WNW", project_id="<project-id>")
```

---

## The Principle

The prompt that kicked off your session is just a door opener.
Everything else — the spec, the conventions, the prior decisions — lives in Hizal.
Read those first. Code second.
