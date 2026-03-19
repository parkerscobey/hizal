---
name: hizal-plan
description: Build a task plan with Hizal by reviewing prior decisions and constraints, drafting an approach, validating it against existing context, and saving the resulting plan.
---

# Hizal Plan

Use this skill when the user wants a concrete implementation or investigation plan grounded in Hizal context.

Use it for requests like:
- "Plan how to implement X"
- "Create a plan for X"
- "How should we approach X?"

## Session Lifecycle

Start a session at the top of any planning task — see `hizal-onboard`. End it with `end_session` when done.

## Setup

Expect a Hizal MCP server to be configured with:
- `Authorization: Bearer <api-key>`

Resolve the `project_id` explicitly for all project-scoped MCP calls.

## Workflow

1. Discover the target project with `list_projects` if needed.
2. Search agent memory for personal prior experience with the problem area — `search_context(query="<topic>", scope="AGENT", limit=5)`.
3. Search project knowledge for existing conventions and constraints that the plan must respect:
   - `search_context(query="<topic> conventions", project_id="<project_id>", limit=5)`
   - `search_context(query="<topic> constraints", project_id="<project_id>", limit=5)`
4. Read the relevant chunks in full with `read_context`.
5. Draft a plan with:
   - goal
   - approach
   - concrete steps
   - dependencies
   - risks or open questions
   - success criteria
6. Validate the draft against known conventions or constraints from Hizal.
7. Save the plan with `write_chunk(type="PLAN", project_id="<project_id>")`. Plans are SURFACE — they are reviewed at end_session and promoted to KNOWLEDGE if still valuable.
8. If the plan changes materially, update it with `update_context`.

## Writing Tool Guidance

| Plan state | Tool / type |
|---|---|
| Draft being iterated | `write_chunk(type="PLAN")` |
| Finalized, worth sharing | promote via `write_knowledge` |
| Key architectural decision made | `write_chunk(type="DECISION")` |

## Always-Inject Guardrails

Never use `write_convention` during planning. Plan outcomes belong in KNOWLEDGE or PLAN, not always_inject context.

Before using `write_convention`, ask:
1. Will this still be true and relevant in 6 months?
2. Does every agent need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no, use `write_knowledge` or `write_memory` instead.

## Notes

- Plans should reflect known constraints from Hizal, not just a fresh guess.
- Include ticket IDs or other traceable references in the saved plan when available.
- Use typed write tools instead of `write_context`.
