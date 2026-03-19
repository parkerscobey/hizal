---
name: winnow-research
description: Research a topic with Winnow by checking existing context first, reading the relevant chunks, filling gaps from the repo or web, and writing back a focused summary.
---

# Winnow Research

Use this skill when the user wants research, discovery, or background gathering tied to Winnow.

Use it for requests like:
- "Research X"
- "What do we know about X?"
- "Look into X and save the findings"

## Session Lifecycle

Start a session at the top of any research task — see `winnow-onboard`. End it with `end_session` when done.

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Choose the target `project_id` explicitly. If the project is unclear, call `list_projects` first.

## Workflow

1. Resolve the project with `list_projects` when needed.
2. Search Winnow before doing new work.
   - `search_context(query="<topic>", project_id="<project_id>", limit=5)`
3. Read the top matches with `read_context`.
4. If the answer is already present and recent, use it directly.
5. If context is incomplete, gather the missing facts from the codebase, docs, or the web.
6. Write back findings with the right tool:
   - `write_knowledge` for factual findings worth sharing with the team (PROJECT scope)
   - `write_memory` for personal observations, failed approaches, and interpretive notes (AGENT scope)
   - `write_chunk(type="RESEARCH")` for raw research notes that should be reviewed at end_session
7. Return the answer with the relevant chunk IDs when useful.

## Writing Tool Guidance

| What you found | Tool | Why |
|---|---|---|
| Architecture, patterns, factual discoveries | `write_knowledge` | Shared team context |
| A failed approach and why it failed | `write_memory` | Personal lesson, not team knowledge |
| Raw notes you want to review later | `write_chunk(type="RESEARCH")` | Ephemeral, surfaced at end_session |
| A decision with rationale | `write_chunk(type="DECISION")` | Long-lived, preserve carefully |

## Always-Inject Guardrails

Never use `write_convention`, `write_identity`, or `store_principle` during research. Research findings are on-demand context — they should not flood every agent's context window.

Before using `write_convention`, ask:
1. Will this still be true and relevant in 6 months?
2. Does every agent need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no, use `write_knowledge` or `write_memory` instead.

## Notes

- Avoid writing duplicate chunks — search first.
- Keep chunks narrow and factual.
- Include a source path or URL when you add new information.
- Use typed write tools instead of `write_context`.
