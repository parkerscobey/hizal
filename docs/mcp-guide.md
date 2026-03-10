# Winnow MCP Guide

Winnow exposes all its functionality as an MCP (Model Context Protocol) server over HTTP+SSE. This lets any MCP-compatible client (Claude Desktop, Cursor, OpenClaw, etc.) use Winnow tools natively.

## Connection

**MCP Endpoint:** `https://winnow-api.xferops.dev/mcp`  
**Transport:** HTTP+SSE (streamable HTTP)  
**Auth:** `Authorization: Bearer <your-api-key>` header

---

## Configuration

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer ctx_your-org_YOUR_KEY_HERE"
      }
    }
  }
}
```

### Cursor

Edit `.cursor/mcp.json` (project) or `~/.cursor/mcp.json` (global):

```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer ctx_your-org_YOUR_KEY_HERE"
      }
    }
  }
}
```

### OpenClaw

Edit `~/.mcporter/mcporter.json`:

```json
{
  "servers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer ctx_your-org_YOUR_KEY_HERE"
      }
    }
  }
}
```

---

## Tools Reference

### `write_context`

Store a new knowledge chunk with semantic embedding.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query_key` | string | ✅ | Namespace tag (e.g. `"auth-flow"`, `"db-schema"`) |
| `title` | string | ✅ | Short, descriptive title |
| `content` | string | ✅ | The actual knowledge (can be long) |
| `source_file` | string | — | File this relates to (e.g. `"internal/auth/middleware.go"`) |
| `source_lines` | [int, int] | — | Line range `[start, end]` |
| `gotchas` | string[] | — | Known pitfalls, edge cases, warnings |
| `related` | string[] | — | IDs of related chunks to link |

**Returns:**
```json
{
  "id": "cuid_abc123",
  "query_key": "auth-flow",
  "title": "JWT validation in middleware",
  "created_at": "2026-03-10T00:00:00Z"
}
```

**Example:**
```
write_context(
  query_key="payment-flow",
  title="Stripe webhook signature validation",
  content="All Stripe webhooks are verified using the STRIPE_WEBHOOK_SECRET env var. The verification happens in internal/payments/webhook.go line 34. Failed verification returns 400 immediately.",
  source_file="internal/payments/webhook.go",
  source_lines=[30, 50],
  gotchas=["Secret differs between test and live modes", "Raw body must be used — parsed body will fail sig check"]
)
```

---

### `search_context`

Semantic vector search. Finds chunks by meaning, not just keywords.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | ✅ | Natural language query |
| `limit` | int | — | Max results (default: 10) |
| `query_key` | string | — | Restrict to a specific namespace |

**Returns:**
```json
{
  "results": [
    {
      "id": "cuid_abc123",
      "query_key": "payment-flow",
      "title": "Stripe webhook signature validation",
      "content": "...",
      "score": 0.94,
      "version": 1,
      ...
    }
  ]
}
```

Results sorted by relevance × recency.

**Example:**
```
search_context(query="how are webhooks verified", query_key="payment-flow", limit=5)
```

---

### `read_context`

Retrieve a specific chunk by ID, including full version history.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | ✅ | Chunk ID |

**Returns:** Full chunk object + `versions` array.

---

### `update_context`

Update a chunk. A new version is created; history is preserved.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | ✅ | Chunk ID to update |
| `title` | string | — | New title |
| `content` | string | — | New content |
| `source_file` | string | — | Updated file path |
| `source_lines` | [int, int] | — | Updated line range |
| `gotchas` | string[] | — | Replaces existing gotchas list |
| `related` | string[] | — | Replaces existing related IDs |
| `change_note` | string | ✅ | Reason for update |

**Returns:**
```json
{
  "id": "cuid_abc123",
  "version": 3,
  "updated_at": "2026-03-12T00:00:00Z"
}
```

---

### `get_context_versions`

View the full version history of a chunk.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | ✅ | Chunk ID |
| `limit` | int | — | Max versions to return |

**Returns:**
```json
{
  "versions": [
    { "version": 1, "change_note": "Initial write", "created_at": "..." },
    { "version": 2, "change_note": "Fixed after testing", "created_at": "..." }
  ]
}
```

---

### `compact_context`

Fetch a set of related chunks for client-side compaction (summarization).

Use this when your context window is getting full. Fetch the relevant chunks, summarize them into a single new chunk, then optionally delete the originals.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | ✅ | Topic to compact around |
| `limit` | int | — | Max chunks to return (default: 20) |

**Returns:**
```json
{
  "chunks": [...],
  "total": 15
}
```

**Compaction workflow:**
1. `compact_context(query="authentication system")`
2. Read all returned chunks
3. Summarize into a single coherent chunk
4. `write_context(...)` with the summary
5. Delete source chunks with `delete_context(id)` if desired

---

### `review_context`

Rate the quality of a chunk after using it.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `chunk_id` | string | ✅ | ID of chunk being reviewed |
| `task` | string | — | What you were working on |
| `usefulness` | int 1-5 | ✅ | How useful was it? |
| `usefulness_note` | string | — | Why |
| `correctness` | int 1-5 | ✅ | How accurate/correct? |
| `correctness_note` | string | — | Why |
| `action` | enum | ✅ | `useful` \| `needs_update` \| `outdated` \| `incorrect` |

**Action meanings:**

| Action | When to use |
|--------|------------|
| `useful` | Content was accurate and helpful |
| `needs_update` | Mostly correct but has gaps or minor inaccuracies |
| `outdated` | Was accurate at some point but code has changed |
| `incorrect` | Contains wrong information |

---

### `delete_context`

Permanently remove a chunk.

**Input:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | ✅ | Chunk ID to delete |

**Returns:**
```json
{
  "deleted": true,
  "id": "cuid_abc123"
}
```

---

## Tips

- **Use `query_key` consistently.** It's your namespace. `"auth"`, `"payments"`, `"database"` — whatever makes sense for your codebase.
- **Keep chunks focused.** One concept per chunk. Long chunks are harder to search and use.
- **Review after every task.** Even a quick `review_context` helps the quality signal over time.
- **Compact early.** Don't wait until you're at 80% context. Compact at 40-50% and keep moving.
