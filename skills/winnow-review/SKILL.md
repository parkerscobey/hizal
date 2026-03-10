# SKILL: winnow-review

## Description
Review workflow for Winnow context chunks. Assesses the quality, accuracy, and usefulness of stored chunks, rates them, and updates or removes chunks that are stale, incorrect, or low-value. Keeps the knowledge base healthy and trustworthy.

## Setup
Same as `winnow-research` — Winnow MCP server must be configured with a valid API key and project ID.

## Usage
Invoke this skill when:
- Running a periodic knowledge base health check
- A chunk was referenced and found to be inaccurate
- After a major refactor or decision change
- Before a milestone to ensure context reflects current reality

**Trigger phrases:**
- "Review and rate the Winnow chunks for X"
- "Audit the context for X"
- "Clean up stale context about X"

## Workflow

### Step 1 — Identify chunks to review
```
search_context(query="<topic or area>", projectId="<project_id>", limit=10)
```

### Step 2 — Assess each chunk
```
read_context(id="<chunk_id>")
```
Evaluate: accuracy, relevance, clarity, completeness, age.

### Step 3 — Rate the chunk
```
review_context(
  id="<chunk_id>",
  projectId="<project_id>",
  rating=<1-5>,
  comment="<assessment notes>"
)
```
**Rating scale:** 5=excellent, 4=good, 3=needs update, 2=mostly outdated, 1=incorrect/remove

### Step 4 — Update stale chunks (rating 2-3)
```
update_context(id="<chunk_id>", content="<corrected content>", projectId="<project_id>")
```

### Step 5 — Remove low-quality chunks (rating 1)
```
delete_context(id="<chunk_id>", projectId="<project_id>")
```
Or update with a correction note pointing to a new chunk.

### Step 6 — Report
Summarize: chunks reviewed, rating distribution, updates/deletions made.

## Notes
- Never delete a chunk without reading it fully first
- When in doubt, update rather than delete — preserve history
- Schedule periodic reviews (monthly or per sprint)
- Use `get_context_versions` to understand how a chunk evolved before rating it
