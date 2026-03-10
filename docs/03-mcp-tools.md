# Winnow: MCP Tools Specification

## Overview

Winnow exposes MCP tools to agents for context management. Inspired by the original development MCP, but with key differences.

### Comparison to The Original Development MCP

| The Original MCP | Winnow |
|-----------------|-----------|
| `search_docs` | `search_context` (semantic, grouped by query_key, includes recency) |
| `read_file` | `read_context` (structured chunks, with version history) |
| `write_doc` | `write_context` (structured, with versioning) |
| `add_doc_review` | `review_context` (quality tracking) |
| — | `update_context` (edit existing, creates new version) |
| — | `get_context_versions` (view version history) |
| — | `compact_context` (summarize and compress) |

### Key Differences

1. **Structured context chunks** — Not flat markdown; includes gotchas, related keys, source references
2. **Context compaction** — Unique to Winnow; prevents the "dumb zone"
3. **Review system** — Tracks quality, feeds back into improving context
4. **Persistent storage** — Context survives sessions, compounds over time

---

## Tool: write_context

**Purpose:** Agent writes research findings as a context chunk

**When to use:**
- After researching a codebase area
- When discovering patterns, gotchas, or key files
- When onboarding and learning something new

**Input:**
```json
{
  "query_key": "string",       // grouping key, e.g., "auth-system"
  "title": "string",           // short descriptive title
  "content": "string",         // the context (markdown or structured)
  "source_file": "string?",    // optional: relevant file path
  "source_lines": "[int, int]?", // optional: line numbers
  "gotchas": ["string"]?,      // optional: warnings for future agents
  "related": ["string"]?       // optional: related context query_keys
}
```

**Output:**
```json
{
  "id": "ctx_abc123",
  "query_key": "auth-system",
  "title": "Session handling pattern",
  "created_at": "2026-03-08T12:00:00Z"
}
```

**Example Usage:**
```
The agent has been researching how authentication works:

write_context(
  query_key: "auth-system",
  title: "Session-based auth with Warden",
  content: "Auth uses Warden with database strategy. User model has:\n- `session_key` (uuid)\n- `last_session_at` (datetime)\nSession expires after 30 days of inactivity.",
  source_file: "app/models/user.rb",
  source_lines: [120, 180],
  gotchas: [
    "No remember_token support - users must log in each visit",
    "Session cleanup runs daily at 3am UTC"
  ],
  related: ["api-auth", "payment-tokens"]
)
```

---

## Tool: search_context

**Purpose:** Find relevant context chunks via query

**When to use:**
- Starting a new task
- Before writing code in an unfamiliar area
- When unsure if context already exists

**Input:**
```json
{
  "query": "string",    // search query
  "limit": 10,          // optional: max results (default: 10)
  "query_key": "string?" // optional: filter by specific context group
}
```

**Output:**
```json
{
  "results": [
    {
      "id": "ctx_abc123",
      "query_key": "auth-system",
      "title": "Session-based auth with Warden",
      "content": "Auth uses Warden with database strategy...",
      "source_file": "app/models/user.rb",
      "source_lines": [120, 180],
      "score": 0.95,
      "created_at": "2026-03-08T12:00:00Z",
      "updated_at": "2026-03-08T14:30:00Z",
      "version": 2
    },
    ...
  ],
  "total": 3
}
```

**Example Usage:**
```
search_context(
  query: "how does payment processing work",
  limit: 5
)
```

---

## Tool: read_context

**Purpose:** Get a specific context chunk by ID

**When to use:**
- After finding relevant chunks via search
- When you have a context ID and need full details
- When referencing specific context in planning

**Input:**
```json
{
  "id": "ctx_abc123"  // context chunk ID
}
```

**Output:**
```json
{
  "id": "ctx_abc123",
  "query_key": "auth-system",
  "title": "Session-based auth with Warden",
  "content": "Auth uses Warden...",
  "source_file": "app/models/user.rb",
  "source_lines": [120, 180],
  "gotchas": [
    "No remember_token support"
  ],
  "related": ["api-auth", "payment-tokens"],
  "versions": [
    {"id": "ver_xyz", "created_at": "2026-03-08T12:00:00Z"}
  ],
  "created_at": "2026-03-08T12:00:00Z",
  "updated_at": "2026-03-08T14:30:00Z"
}
```

---

## Tool: compact_context

**Purpose:** Fetch all context chunks matching a query in a single call, optimized for agent-side compaction

**How it works:** This tool does NOT perform summarization. It returns the raw matching chunks so the agent can summarize them client-side using its own reasoning. The agent then writes the compacted summary back via `write_context`. This keeps all LLM inference on the client — no server-side AI costs or latency.

**When to use:**
- Before entering the "dumb zone" (after 15-20 min of work)
- When starting a new phase of work
- When onboarding a new agent
- Before ending a session (for future agents to pick up)

**Input:**
```json
{
  "query": "string",        // what to compact
  "limit": 50               // optional: max chunks to return (default: 50)
}
```

**Output:**
```json
{
  "chunks": [
    {
      "id": "ctx_abc123",
      "query_key": "auth-system",
      "title": "Session-based auth with Warden",
      "content": "Auth uses Warden with database strategy...",
      "source_file": "app/models/user.rb",
      "source_lines": [120, 180],
      "gotchas": ["No remember_token support - users must log in each visit"],
      "related": ["api-auth", "payment-tokens"],
      "version": 2,
      "created_at": "2026-03-08T12:00:00Z",
      "updated_at": "2026-03-08T14:30:00Z"
    },
    {
      "id": "ctx_def456",
      "query_key": "auth-system",
      "title": "Warden strategy configuration",
      "content": "Database strategy configured in...",
      "source_file": "config/initializers/warden.rb",
      "source_lines": [10, 30],
      "gotchas": ["Session cleanup runs daily at 3am UTC"],
      "related": ["auth-system"],
      "version": 1,
      "created_at": "2026-03-08T13:00:00Z",
      "updated_at": "2026-03-08T13:00:00Z"
    }
  ],
  "total": 2
}
```

**Expected agent workflow after calling this tool:**
1. Call `compact_context` to fetch all related chunks
2. Agent summarizes the chunks in its own context (what, files, gotchas, gaps, related)
3. Agent writes the compacted summary back via `write_context` with a descriptive `query_key`

**Example Usage:**
```
compact_context(
  query: "auth system",
  limit: 20
)
```

---

## Tool: update_context

**Purpose:** Update an existing context chunk (creates new version, preserves history)

**When to use:**
- Context is outdated or incomplete
- Adding new findings to existing context
- Fixing incorrect information

**Input:**
```json
{
  "id": "ctx_abc123",           // chunk to update
  "title": "string?",           // optional new title
  "content": "string?",         // optional new content
  "source_file": "string?",     // optional new source
  "source_lines": "[int, int]?", // optional new lines
  "gotchas": ["string"]?,       // optional new gotchas
  "related": ["string"]?,       // optional new related keys
  "change_note": "string"       // brief note about what changed
}
```

**Output:**
```json
{
  "id": "ctx_abc123",
  "version": 3,
  "updated_at": "2026-03-08T16:00:00Z"
}
```

**Example Usage:**
```
update_context(
  id: "ctx_abc123",
  content: "Auth uses Warden with database strategy. User model has:\n
    session_key (uuid)\n
    last_session_at (datetime)\n
    Added: reset_password_token for password resets",
  gotchas: [
    "No remember_token support - users must log in each visit",
    "Session cleanup runs daily at 3am UTC",
    "Password reset token expires after 6 hours (NEW)"
  ],
  change_note: "Added password reset info from recent work"
)
```

---

## Tool: get_context_versions

**Purpose:** View version history of a context chunk

**When to use:**
- Understanding how context has evolved
- Recovering older (correct) information
- Reviewing what changed between versions

**Input:**
```json
{
  "id": "ctx_abc123",    // context chunk ID
  "limit": 10            // optional: max versions to return
}
```

**Output:**
```json
{
  "versions": [
    {
      "version": 3,
      "change_note": "Added password reset info",
      "created_at": "2026-03-08T16:00:00Z"
    },
    {
      "version": 2,
      "change_note": "Added session cleanup gotcha",
      "created_at": "2026-03-08T14:30:00Z"
    },
    {
      "version": 1,
      "change_note": "Initial context",
      "created_at": "2026-03-08T12:00:00Z"
    }
  ]
}
```

---

## Tool: review_context

**Purpose:** Add a quality review to a context chunk (inspired by the original development MCP's `add_doc_review`)

**When to use:**
- After using context that helped (or didn't help) with a task
- When user provides feedback on agent generation (context may be partially to blame)
- Periodic quality audits

**Input:**
```json
{
  "chunk_id": "ctx_abc123",   // context chunk being reviewed
  "task": "string",           // what the agent was working on
  "usefulness": 1-5,          // usefulness rating
  "usefulness_note": "string?", // optional note
  "correctness": 1-5,         // accuracy rating
  "correctness_note": "string?", // optional note
  "action": "string"          // 'useful', 'needs_update', 'outdated', 'incorrect'
}
```

**Output:**
```json
{
  "id": "rev_xyz789",
  "chunk_id": "ctx_abc123",
  "created_at": "2026-03-08T15:00:00Z"
}
```

**Example Usage:**
```
The agent just completed a task using context about auth. Now reviewing:

review_context(
  chunk_id: "ctx_abc123",
  task: "Added password reset functionality",
  usefulness: 4,
  usefulness_note: "Gotchas about token expiry were very helpful",
  correctness: 5,
  action: "useful"
)
```

---

## Tool: delete_context

**Purpose:** Remove a context chunk

**When to use:**
- Context is outdated or incorrect
- Cleaning up temporary research

**Input:**
```json
{
  "id": "ctx_abc123"
}
```

**Output:**
```json
{
  "deleted": true,
  "id": "ctx_abc123"
}
```

---

## Error Responses

All tools return errors in standard format:

```json
{
  "error": {
    "code": "AUTH_INVALID",
    "message": "API key is invalid or expired"
  }
}
```

| Error Code | Description |
|------------|-------------|
| AUTH_INVALID | API key is invalid or expired |
| AUTH_FORBIDDEN | Key lacks required permission |
| NOT_FOUND | Context chunk not found |
| VALIDATION_ERROR | Invalid input parameters |
| RATE_LIMITED | Too many requests |

---

## Related Docs

- [Problem & Sources](./01-problem-sources.md)
- [Architecture](./02-architecture.md)
- [Skills](./04-skills.md)
- [Workflows](./05-workflows.md)

---

*Last updated: 2026-03-08*
*Status: Draft / Iterating*