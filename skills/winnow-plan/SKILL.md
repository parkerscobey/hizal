---
name: winnow-plan
description: Build a task plan with Winnow by reviewing prior decisions and constraints, drafting an approach, validating it against existing context, and saving the resulting plan.
---

# Winnow Plan

Use this skill when the user wants a concrete implementation or investigation plan grounded in Winnow context.

Use it for requests like:
- "Plan how to implement X"
- "Create a plan for X"
- "How should we approach X?"

## Setup

Expect a Winnow MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly for all project-scoped MCP calls.

## Workflow

1. Discover the target project with `list_projects` if needed.
2. Search for related decisions, constraints, and prior work with `search_context`.
3. Read the relevant chunks in full.
4. Draft a plan with:
   - goal
   - approach
   - concrete steps
   - dependencies
   - risks or open questions
   - success criteria
5. Validate the draft against known conventions or constraints from Winnow.
6. Save the finalized plan with `write_context`.
7. If the plan changes materially, update it with `update_context`.

## Notes

- Plans should reflect known constraints from Winnow, not just a fresh guess.
- Include ticket IDs or other traceable references in the saved plan when available.
- Use `project_id` on MCP tool calls instead of connection-level project headers.
