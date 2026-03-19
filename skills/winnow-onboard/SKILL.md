---
name: winnow-onboard
description: Onboard to a project with Winnow by listing projects, selecting the right project_id, searching for architecture and status context, and summarizing the current mental model.
---

# Winnow Onboard

Use this skill when the user wants a fast project orientation before coding.

Use it for requests like:
- "Onboard me to this project"
- "Get me up to speed"
- "What is the current state of this system?"

## Session Lifecycle

Every session starts with `start_session` and ends with `end_session`. These are non-negotiable — they drive injection and consolidation behavior.

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

## Session Start

1. Call `start_session(project_id="<project_id>", lifecycle_slug="dev")`. This injects all `always_inject` chunks (CONVENTION, IDENTITY, PRINCIPLE) for the session. No separate activation step needed.
2. Call `register_focus(session_id="<session_id>", task="<ticket ID>: <task title>")` to record what you are working on.
3. Discover the target project if not explicit — call `list_projects` and use the returned `project_id`.

## Workflow

1. Search across all accessible scopes for high-level context.
   - `search_context(query="project overview architecture", project_id="<project_id>", limit=5)`
   - `search_context(query="design decisions conventions", project_id="<project_id>", limit=5)`
   - `search_context(query="current status roadmap", project_id="<project_id>", limit=5)`
2. Read the most relevant chunks in full with `read_context`.
3. Expand into the major feature or domain areas you discover.
4. Check `get_context_versions` for foundational chunks if freshness matters.
5. Review any SURFACE chunks returned from previous `end_session` calls that have not been promoted or discarded.
6. Return a concise mental model covering:
   - project purpose and current state
   - major architecture and data flow
   - conventions and constraints
   - open questions or gaps
   - where to start for the user's task
7. If you create a useful synthesis that does not already exist, write it back with the right tool:
   - `write_knowledge` for shared project facts
   - `write_memory` for personal observations specific to this task

## Session End

After the primary task is complete and a PR is open:

1. Call `end_session(session_id="<session_id>")` — it returns all SURFACE chunks written during the session.
2. For each SURFACE chunk, decide: keep (leave as-is), promote (write a new KNOWLEDGE chunk with the content), or discard.
3. Compact if context retrieval feels noisy — see `winnow-compact`.

## Notes

- Prefer existing Winnow context before reading large portions of the repo.
- `start_session` replaces `activate_project` — no such tool exists.
- `search_context` returns all accessible scopes by default (PROJECT, AGENT, ORG) when no scope filter is set.
- Use typed write tools instead of `write_context`: `write_knowledge`, `write_memory`, `write_convention`, etc.
- If Winnow context is sparse, fall back to repo docs, README files, and targeted code search.
