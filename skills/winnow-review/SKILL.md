---
name: winnow-review
description: Review Winnow context quality by finding relevant chunks, rating their accuracy and usefulness, updating stale content, and removing low-value entries when justified.
---

# Winnow Review

Use this skill when the user wants a quality audit of stored Winnow knowledge.

Use it for requests like:
- "Review the Winnow chunks for X"
- "Audit the context for this area"
- "Clean up stale knowledge"

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly for all project-scoped MCP calls.

## Workflow

1. Find the target chunks with `search_context`.
2. Read each chunk fully with `read_context`.
3. Check freshness with `get_context_versions` when needed.
4. Rate usefulness and correctness with `review_context`.
5. Update stale chunks with `update_context` when a correction is clear.
6. Delete only when a chunk is clearly wrong, redundant, or replaced.
7. Return the review summary, including what changed.

## Notes

- Prefer correcting over deleting when the history is still useful.
- Be explicit about why a chunk is stale or low value.
- Use `project_id` on MCP tool calls instead of connection-level project headers.
