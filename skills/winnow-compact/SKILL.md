# SKILL: winnow-compact

## Description
Compaction workflow for Winnow. Fetches multiple related context chunks, summarizes them into a single high-quality chunk, writes the summary back, and optionally removes or supersedes the originals. Keeps the knowledge base clean and token-efficient.

## Setup
Same as `winnow-research` — Winnow MCP server must be configured with a valid API key and project ID.

## Usage
Invoke this skill when:
- A topic has many small or outdated chunks that should be merged
- Context retrieval is returning noisy/redundant results
- Preparing a project snapshot or milestone summary
- Token budget is a concern and you need denser context

**Trigger phrases:**
- "Compact the Winnow context for X"
- "Summarize and clean up chunks about X"
- "Merge the research chunks on X"

## Workflow

### Step 1 — Identify chunks to compact
Search for all chunks related to the topic:
```
search_context(query="<topic>", projectId="<project_id>", limit=20)
```
Review results. Select chunks that are overlapping, redundant, or outdated.

### Step 2 — Fetch full content
Retrieve the complete content of each selected chunk:
```
compact_context(ids=["<id1>", "<id2>", ...], projectId="<project_id>")
```
This returns all chunks in one call, optimized for summarization.

### Step 3 — Summarize
Synthesize all fetched content into a single, comprehensive summary:
- Preserve all important facts, decisions, and references
- Remove repetition, outdated info, and noise
- Structure clearly with headers if the content is long

### Step 4 — Write the summary chunk
```
write_context(
  projectId="<project_id>",
  content="<summary>",
  tags=["compacted", "<topic>"],
  source="compacted from: <list of original IDs>"
)
```

### Step 5 — Version or delete originals
For each original chunk, either:
- **Update** with a deprecation note pointing to the new chunk
- **Delete** if fully captured in the summary:
  ```
  delete_context(id="<old_id>", projectId="<project_id>")
  ```

### Step 6 — Report
Return a summary of what was compacted, how many chunks were merged, and the new chunk ID.

## Notes
- Never delete chunks without reading them first
- Keep the `source` field traceable — list original IDs
- Run compaction periodically (e.g., after a sprint) to prevent context bloat
- Tag compacted chunks with `"compacted"` for easy identification
