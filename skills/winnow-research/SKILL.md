# SKILL: winnow-research

## Description
Research workflow using Winnow for AI agents. Searches existing context, explores related chunks, and writes new findings back into the Winnow knowledge base. Prevents re-researching topics that are already documented.

## Setup
**Prerequisites:**
- Winnow MCP server configured with your API key and project ID
- MCP config (`~/.cursor/mcp.json` or OpenClaw MCP config):

```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": { "Authorization": "Bearer dk_live_YOUR_KEY_HERE" }
    }
  }
}
```

**Required env / config:**
- `WINNOW_API_KEY` — your Winnow API key (`dk_live_{org}_{random}`)
- `WINNOW_PROJECT_ID` — the project UUID to read/write context into

## Usage
Invoke this skill when:
- Starting research on a new topic or feature
- Exploring an unfamiliar codebase area
- Gathering information before writing code, docs, or designs

**Trigger phrases:**
- "Research X for me"
- "What do we know about X?"
- "Look into X and save what you find"

## Workflow

### Step 1 — Search existing context
Before fetching anything new, search Winnow to avoid duplication:
```
search_context(query="<topic>", projectId="<project_id>", limit=5)
```
Read the top results. If the answer is already there and recent, return it directly.

### Step 2 — Explore related chunks
For each relevant result, fetch the full chunk to get more depth:
```
read_context(id="<chunk_id>")
```
Follow any referenced IDs or topics to build a complete picture.

### Step 3 — External research (if needed)
If existing context is insufficient, gather new information:
- Web search, documentation, codebase grep, etc.
- Synthesize findings into a clear, factual summary

### Step 4 — Write findings back
Store new/updated knowledge in Winnow:
```
write_context(
  projectId="<project_id>",
  content="<synthesized findings>",
  tags=["research", "<topic>"],
  source="<url or file path>"
)
```

### Step 5 — Return to user
Summarize what was found (existing + new). Include Winnow chunk IDs for traceability.

## Notes
- Always search before writing — avoid duplicate chunks
- Use descriptive tags: `["research", "auth", "v0.1"]`
- Keep chunks focused (one concept per chunk, ~200-500 words)
- Include `source` field whenever possible for auditability
