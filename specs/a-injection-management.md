# Workstream A — Injection Management (do first)

Source: GitHub #126 + bloat root cause from #127/#129.

## Problem
`UpdateContext` backend accepts `inject_audience` (`internal/mcp/tools.go:296-309`, applied at 1095-1099, inject_set invalidated at 1127-1134) but MCP `update_context` schema (`internal/mcp/server.go:329-345`) does not expose it. Agents cannot demote `all:true` chunks to search-only without delete + recreate. That path loses IDs, splits versions, stales links.

## Scope
1. Expose `inject_audience` (object, nullable) on MCP `update_context` InputSchema.
2. Confirm REST `PATCH /v1/context/:id` (`internal/api/handlers.go:203-217`) already passes through same struct — document it, no fork.
3. Validate shape on update: reject malformed `rules`, accept `null` / omitted as "no change" vs explicit clear. Define clear-tombstone semantics (searchable, never auto-injected).
4. Invalidate / recompute affected sessions' `inject_set` (already done in tools.go — verify + test).
5. Docs: `docs/03-mcp-tools.md` update_context section + `docs/api-reference.md` PATCH section.
6. Tests: update inject_audience set → focus_tags spec; clear to null; malformed rejected; version bump preserved; ID stable.

## Out of scope
Summaries mode (B), search/focus ranking (C).

## Acceptance
- `update_context(id, inject_audience={...}, change_note)` updates targeting, preserves ID, bumps version.
- Clearing injection keeps chunk searchable, excludes from `start_session`/`resume_session` injection.
- `go build ./...`, `go vet ./...`, `go test ./... -race` green.
