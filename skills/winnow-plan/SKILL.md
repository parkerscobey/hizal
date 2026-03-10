# SKILL: winnow-plan

## Description
Planning workflow using Winnow. Reviews existing context and decisions, structures a plan for the current task, validates it against known constraints and conventions, then writes the finalized plan back to Winnow for team visibility and future reference.

## Setup
Same as `winnow-research` — Winnow MCP server must be configured with a valid API key and project ID.

## Usage
Invoke this skill when:
- Starting a non-trivial task that needs a plan
- Designing a new feature or system change
- Breaking down a complex ticket into subtasks
- Preparing for a sprint or milestone

**Trigger phrases:**
- "Plan out how to implement X"
- "Create a plan for X and save it"
- "How should we approach X?"
- "Write up a plan for X"

## Workflow

### Step 1 — Review existing context
Search for prior decisions, constraints, and related work:
```
search_context(query="<task topic>", projectId="<project_id>", limit=5)
search_context(query="<related feature or system>", projectId="<project_id>", limit=5)
```
Note any constraints, patterns, or prior attempts.

### Step 2 — Structure the plan
Draft the plan with clear sections:
- **Goal** — What are we trying to achieve?
- **Approach** — High-level strategy
- **Steps** — Ordered list of concrete actions
- **Dependencies** — What needs to exist first?
- **Risks / open questions** — What might block this?
- **Success criteria** — How do we know it's done?

### Step 3 — Validate against context
Cross-check the plan against known constraints:
```
search_context(query="<constraint or convention area>", projectId="<project_id>", limit=3)
```
Revise the plan if it conflicts with established decisions.

### Step 4 — Write the plan to Winnow
```
write_context(
  projectId="<project_id>",
  content="<finalized plan>",
  tags=["plan", "<feature>", "<sprint or milestone>"],
  source="<ticket ID or task reference>"
)
```

### Step 5 — Return to user
Present the plan. Include the Winnow chunk ID so it can be referenced later.

### Step 6 — Update if plan changes
When the plan evolves during execution:
```
update_context(
  id="<plan_chunk_id>",
  content="<updated plan>",
  projectId="<project_id>"
)
```

## Notes
- Plans should be living documents — update them as work progresses
- Tag with the sprint/milestone (e.g., `"v0.1"`) for time-scoped retrieval
- A plan chunk should answer: "Why did we do it this way?" for future readers
- Reference ticket IDs in the `source` field for traceability
