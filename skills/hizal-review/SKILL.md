---
name: hizal-review
description: Review Hizal context quality by finding relevant chunks, rating their accuracy and usefulness, updating stale content, and removing low-value entries when justified.
---

# Hizal Review

Use this skill when the user wants a quality audit of stored Hizal knowledge.

Use it for requests like:
- "Review the Hizal chunks for X"
- "Audit the context for this area"
- "Clean up stale knowledge"

## Session Lifecycle

Start a session at the top of any review task — see `hizal-onboard`. End it with `end_session` when done.

## Setup

Expect a Hizal MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly for all project-scoped MCP calls.

## Workflow

1. Find the target chunks with `search_context(query="<topic>", project_id="<project_id>")`.
2. Read each chunk fully with `read_context`.
3. Check freshness with `get_context_versions` when needed.
4. Rate usefulness and correctness with `review_context`.
5. Update stale chunks with `update_context` when a correction is clear.
6. Delete only when a chunk is clearly wrong, redundant, or replaced.
7. Return the review summary, including what changed.

## Scope-Aware Review

You can review chunks across all scopes. Approach each differently:

| Scope | When updating | When discarding |
|---|---|---|
| AGENT | Use `write_memory` with corrected content | Just delete or update in-place |
| PROJECT | Check if source file has changed (staleness detection) | Verify no other agent relies on it |
| ORG | Coordinate with org admin — org knowledge affects all agents | Be conservative — promote to KNOWLEDGE instead of deleting |

## Chunk Type in Reviews

chunk_type affects how to handle reviews:

| chunk_type | Expected lifespan | Review bar |
|---|---|---|
| KNOWLEDGE | Evergreen | High bar for deletion — it is the baseline |
| RESEARCH | Ephemeral | Low bar — discard if direction is chosen |
| PLAN | Task-scoped | Archive or discard once the work ships |
| DECISION | Long-lived | Very high bar — only incorrect DECISIONs should be removed |

SURFACE chunks (chunks with consolidation_behavior=SURFACE) are expected to be ephemeral. KEEP chunks should be evergreen — rate them accordingly.

## After Reviewing as Outdated

- **AGENT-scoped chunk**: update via `write_memory` or flag for removal
- **PROJECT-scoped chunk**: check if the source file has changed since the chunk was written — if the file was updated, the chunk is likely stale
- **ORG-scoped chunk**: promote to `write_knowledge` or flag for org admin review

## Always-Inject Guardrails

Never use `write_convention` during review. Review produces corrections, not foundational rules.

Before using `write_convention`, ask:
1. Will this still be true and relevant in 6 months?
2. Does every agent need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no, use `write_knowledge` or `write_memory` instead.

## Notes

- Prefer correcting over deleting when the history is still useful.
- Be explicit about why a chunk is stale or low value.
- Use `project_id` on MCP tool calls instead of connection-level project headers.
- Use typed write tools instead of `write_context` for corrections.
