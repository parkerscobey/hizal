---
name: winnow-compact
description: Compact overlapping Winnow context by gathering related chunks, producing a higher-signal summary, writing it back, and superseding or deleting redundant chunks carefully.
---

# Winnow Compact

Use this skill when Winnow has too many overlapping or low-signal chunks on the same topic.

Use it for requests like:
- "Compact the context for X"
- "Merge the research on X"
- "Clean up noisy Winnow chunks"

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly for all project-scoped MCP calls.

## Workflow

1. Identify related chunks with `search_context(query="<topic>", project_id="<project_id>", limit=20)`.
2. Read the candidates before changing anything.
3. Fetch the selected material with `compact_context(ids=[...], project_id="<project_id>")`.
4. Produce one clear summary that preserves important facts, decisions, and references.
5. Write the summary with `write_context`.
6. Supersede or delete originals only after confirming the new chunk fully covers them.
7. Report what was merged and the new chunk ID.

## Notes

- Never delete unread chunks.
- Preserve traceability by referencing the original chunk IDs.
- Prefer updating stale chunks with a superseded note over deleting them when history matters.
