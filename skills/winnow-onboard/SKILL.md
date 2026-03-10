# SKILL: winnow-onboard

## Description
Onboarding workflow for new agents or team members joining a project. Searches the Winnow knowledge base to build a mental model of the project — architecture, decisions, conventions, and current status — without needing to read every source file.

## Setup
Same as `winnow-research` — Winnow MCP server must be configured with a valid API key and project ID.

## Usage
Invoke this skill when:
- A new agent session starts on an unfamiliar project
- A new team member needs to get up to speed
- Resuming a project after a long break
- Doing a context reset / fresh perspective

**Trigger phrases:**
- "Onboard me to project X"
- "What's the current state of X?"
- "Get me up to speed on X"
- "Load context for X"

## Workflow

### Step 1 — Search for overview chunks
Start with high-level queries to find architectural and decision context:
```
search_context(query="project overview architecture", projectId="<project_id>", limit=5)
search_context(query="design decisions conventions", projectId="<project_id>", limit=5)
search_context(query="current status roadmap", projectId="<project_id>", limit=5)
```

### Step 2 — Read key chunks in full
For each high-relevance result, read the full chunk:
```
read_context(id="<chunk_id>")
```
Prioritize chunks tagged: `overview`, `architecture`, `decision`, `convention`, `status`

### Step 3 — Explore domain areas
Search for the major functional areas of the project:
```
search_context(query="<feature area>", projectId="<project_id>", limit=3)
```
Repeat for each key area identified in step 1.

### Step 4 — Check version history on critical chunks
For any chunk that seems foundational, check if it's been updated:
```
get_context_versions(id="<chunk_id>")
```
Use the most recent version.

### Step 5 — Build mental model
Synthesize into a structured summary covering:
- **What the project is** — purpose, scope, current state
- **Architecture** — key components, data flow, tech stack
- **Conventions** — naming, patterns, style decisions
- **Open questions / known gaps** — what's still in flux
- **Where to start** — most relevant areas for the current task

### Step 6 — Write the mental model back (optional)
If this onboarding session produced new synthesis, save it:
```
write_context(
  projectId="<project_id>",
  content="<mental model summary>",
  tags=["onboarding", "overview", "<date>"]
)
```

## Notes
- This skill is read-heavy — writing is optional and only if synthesis adds new value
- If Winnow has sparse context, fall back to reading README, docs/, and git log
- For recurring sessions: search for chunks tagged with the current sprint or milestone
