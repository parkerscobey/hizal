# Workstream B — Start Payload Control + Error DX (do second)

Source: GitHub #127 + #129.1 + #128.

## Problem
`StartSession` (`internal/mcp/sessions.go:440-524`) returns full `injected_chunks` content. Mature stores hit 23 chunks / ~47k chars / ~54-65KB. Harnesses truncate, `session_id` buried. Caller has no prune lever (`ExcludeQueryKeys`/`MaxInjectTokens` server-side only). Missing `project_id` triggers opaque retry loop (#128).

## Scope
1. Opt-in `chunk_detail: full|summary` (default `full` for back-compat) on `start_session` (+ `resume_session` if cheap).
2. Summary fields: `id, query_key, title, scope, chunk_type, size`. Plus total size. Full content pulled via `read_context`.
3. Truncated set returns summaries instead of silent drop (compose with existing `truncated_count`).
4. Error DX: missing/unknown `project_id` error lists how to recover (`list_projects`, valid IDs).
5. Update `skills/hizal-start/SKILL.md` to teach summarize-then-pull.
6. Tests: summary mode shape, back-compat default, truncation summaries, error hint.

## Out of scope
Injection editing (A), relevance ranking (C).

## Acceptance
- Fresh agent can start with summaries, pull 1-2 chunks, never fetch 65KB.
- `session_id` visible without fishing saved files.
