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

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Do not assume the active project. Start by discovering or confirming the correct `project_id`.

## Workflow

1. Discover the target project.
   - Call `list_projects` if the project is not explicit.
   - Use the project name and description to choose the correct `project_id`.
2. Search for high-level context first.
   - `search_context(query="project overview architecture", project_id="<project_id>", limit=5)`
   - `search_context(query="design decisions conventions", project_id="<project_id>", limit=5)`
   - `search_context(query="current status roadmap", project_id="<project_id>", limit=5)`
3. Read the most relevant chunks in full with `read_context`.
4. Expand into the major feature or domain areas you discover.
5. Check `get_context_versions` for foundational chunks if freshness matters.
6. Return a concise mental model covering:
   - project purpose and current state
   - major architecture and data flow
   - conventions and constraints
   - open questions or gaps
   - where to start for the user’s task
7. If you create a useful synthesis that does not already exist, write it back with `write_context`.

## Notes

- Prefer existing Winnow context before reading large portions of the repo.
- Use `project_id` on MCP tool calls instead of relying on connection-level project headers.
- If Winnow context is sparse, fall back to repo docs, README files, and targeted code search.
