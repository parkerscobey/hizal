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
6. Write back a focused synthesis with `write_context`.
7. Return the answer with the relevant chunk IDs when useful.

## Notes

- Avoid writing duplicate chunks.
- Keep chunks narrow and factual.
- Include a source path or URL when you add new information.
- Use `project_id` on MCP tool calls instead of connection-level project headers.
