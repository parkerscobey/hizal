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

## Session Lifecycle

Start a session at the top of any compaction task — see `winnow-onboard`. End it with `end_session` when done.

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly for all project-scoped MCP calls.

## Workflow

1. Identify related chunks with `search_context(query="<topic>", project_id="<project_id>", limit=20)`.
2. Read the candidates before changing anything.
3. Fetch the selected material with `compact_context(scope="PROJECT", query="<topic>", project_id="<project_id>")` — scope is required. Compact one scope at a time.
4. Produce one clear summary that preserves important facts, decisions, and references.
5. Write the summary with `write_knowledge(project_id="<project_id>")`.
6. Supersede or delete originals only after confirming the new chunk fully covers them.
7. Report what was merged and the new chunk ID.

## Scope-Aware Compacting

Compact within a single scope per session:

| Scope | What to compact | Notes |
|---|---|---|
| PROJECT | Overlapping KNOWLEDGE, RESEARCH, PLAN chunks | Prefer within same chunk_type |
| AGENT | Personal memory that grew noisy | Keep always_inject IDENTITY separate |
| ORG | Org-wide knowledge that drifted | Be conservative — org chunks affect all agents |

Do NOT compact across scopes. Promoting content between scopes is a consolidation decision, not compaction — use end_session review to handle that.

## Chunk Type Awareness

Prefer compacting within the same chunk_type:
- RESEARCH with RESEARCH — these are more disposable
- KNOWLEDGE with KNOWLEDGE — stable facts
- PLAN with PLAN — in-flight work

Do NOT merge DECISION chunks into KNOWLEDGE summaries. DECISION chunks are high-value and long-lived — preserve them individually.

## Always-Inject Awareness

Do not merge always_inject and non-always_inject chunks. They serve different purposes:
- always_inject chunks are foundational — they must remain readable on their own
- non-always_inject chunks are on-demand context

## Always-Inject Guardrails

Never use `write_convention` during compaction. Compaction produces summaries, not foundational rules.

Before using `write_convention`, ask:
1. Will this still be true and relevant in 6 months?
2. Does every agent need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no, use `write_knowledge` or `write_memory` instead.

## Notes

- Never delete unread chunks.
- Preserve traceability by referencing the original chunk IDs.
- Prefer updating stale chunks with a superseded note over deleting them when history matters.
- Use `compact_context(scope=...)` — scope param is required.
- Use typed write tools instead of `write_context`.
