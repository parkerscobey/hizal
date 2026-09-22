# Workstream C — Search + Focus Relevance (needs repro first)

Source: GitHub #129.2 + #129.3.

## Problem
- Semantic search for ticket-scoped terms (`FD-24561`, autobill copy) returns only ORG/AGENT noise. Real chunks surface only via `list_chunks` + subagent summarization.
- `register_focus` with valid tags returns `focus_injected_chunks: 0`. Current `updateFocusInjectSet` (`internal/mcp/sessions.go:667-740`) matches only `rule.FocusTags` overlap and returns a count, not chunks.

## Scope
1. Repro: seed ticket-scoped chunks, run reported queries, capture ranking + `scopeFilter` / `alwaysInjectOnly` / freshness behavior (`internal/mcp/tools.go:725-886`).
2. Decide: embedding vs filter vs ranking fix. Check `scopeFilter`, `ExcludeQueryKeyPrefixes`, freshness decay re-rank window (SQL LIMIT before decay).
3. `register_focus`: return matched chunk summaries (not just count) so agent gets content without extra round-trip.
4. Evaluate `get-identity` proposal vs B summaries mode (identity-summaries-first may subsume it).
5. Tests: regression queries that previously missed; focus match returns content.

## Out of scope
A + B implementation.

## Acceptance
- Reported queries return project chunks in top-N.
- Focus registration with matching tags injects + returns usable chunks.
