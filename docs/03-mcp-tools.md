# Winnow: MCP Tools Reference

## Overview

Winnow exposes MCP tools over HTTP+SSE for AI agents to manage context. Tools are organized into four categories: **session lifecycle**, **purpose-built writes**, **reads and search**, and **project management**.

All tools require `Authorization: Bearer <api-key>` configured once in your MCP client.

### Tool Availability by Agent Type

Not all agents see all tools. The `tools/list` response is filtered by the agent's registered type:

| Tool Category | dev | admin | research | orchestrator |
|--------------|-----|-------|----------|--------------|
| Session lifecycle | ✅ | ✅ | ✅ | ✅ |
| Read/search/compact | ✅ | ✅ | ✅ | ✅ |
| Write tools | ✅ | ✅ | ✅ | ✅ |
| Review | ✅ | ✅ | ✅ | ✅ |
| Project management | ❌ | ✅ | ❌ | ✅ |

---

## Session Lifecycle Tools

### start_session

Start a new agent work session. Returns a `session_id` and triggers injection of always-inject chunks (identity, conventions, principles).

**Input:**
```json
{
  "lifecycle_slug": "dev",       // optional: agent type preset (dev, admin, research, orchestrator)
  "project_id": "uuid"           // optional: primary project for this session
}
```

**Output:** `{ "session_id": "uuid" }`

### resume_session

Resume a previously started session (e.g., after agent restart).

**Input:** `{ "session_id": "uuid" }`

### get_active_session

Check if the current agent has an active session. Useful for session recovery after crashes.

**Input:** none

**Output:** `{ "session_id": "uuid", ... }` or empty if no active session.

### register_focus

Tell Winnow what you're working on. Enables future SSE notifications when other agents write context relevant to your focus.

**Input:**
```json
{
  "session_id": "uuid",
  "task": "implementing billing webhooks",
  "project_id": "uuid"
}
```

### end_session

End the current session. Returns MEMORY-typed chunks written during the session for review and promotion.

**Input:** `{ "session_id": "uuid" }`

**Output:** List of MEMORY chunks from this session (consolidation_behavior=SURFACE).

---

## Purpose-Built Write Tools

Six tools replace the generic `write_context`. The tool name communicates intent and automatically routes to the correct scope + injection behavior.

### write_identity

**Scope:** AGENT | **Always Inject:** yes | **Chunk Type:** IDENTITY

Who this agent is — role, values, responsibilities, relationships.

**When to use:** Initial provisioning, or when role/responsibilities meaningfully change.
**Do NOT use:** After every session, for task-specific observations, to record what you just did.

**Input:**
```json
{
  "query_key": "seth-identity",
  "title": "Seth — Junior Developer",
  "content": "I'm the junior developer at XferOps...",
  "agent_id": "uuid"
}
```

### write_memory

**Scope:** AGENT | **Always Inject:** no | **Chunk Type:** MEMORY

Episodic notes — personal observations, learned patterns, past experiences.

**When to use:** At decision points during work, after discovering a gotcha, after completing a task.

**Input:**
```json
{
  "query_key": "auth-middleware-silent-fail",
  "title": "Auth middleware fails silently without tenant resolver",
  "content": "When auth middleware runs before the tenant resolver, it fails silently...",
  "agent_id": "uuid",
  "source_file": "internal/middleware/auth.go",
  "gotchas": ["No error logged — request just returns 401"]
}
```

### write_knowledge

**Scope:** PROJECT | **Always Inject:** no | **Chunk Type:** KNOWLEDGE

Project facts — architecture, patterns, conventions, deployment docs.

**When to use:** Seeding, researching, after learning something worth sharing with the team.

**Input:**
```json
{
  "query_key": "auth-shared-cookie",
  "title": "Auth uses NextAuth v5 with shared cookie at .xferops.dev",
  "content": "All XferOps apps share a single auth cookie...",
  "project_id": "uuid",
  "source_file": "src/auth/config.ts",
  "source_lines": [42, 67],
  "gotchas": ["Cookie domain must match — .xferops.dev, not .xferops.com"],
  "related": ["auth-middleware", "tenant-resolution"]
}
```

### write_convention

**Scope:** PROJECT | **Always Inject:** yes | **Chunk Type:** CONVENTION

Foundational project rules — always in context once a project is activated.

**When to use:** Recording rules every agent must always know, that will remain true for months.
**Do NOT use:** For facts that change, for things only some agents need, for task outcomes.

**Input:**
```json
{
  "query_key": "pr-required",
  "title": "All changes require a PR — never push to main",
  "content": "Every code change must go through a pull request...",
  "project_id": "uuid"
}
```

### write_org_knowledge

**Scope:** ORG | **Always Inject:** no | **Chunk Type:** KNOWLEDGE

Org-wide facts retrieved on demand — team composition, product history, strategic context.

**Input:**
```json
{
  "query_key": "team-composition",
  "title": "XferOps has four AI agents",
  "content": "Adam (orchestrator), Marcus (security), Quinn (QA), Seth (junior dev)...",
  "org_id": "uuid"
}
```

### store_principle

**Scope:** ORG | **Always Inject:** yes | **Chunk Type:** PRINCIPLE

Org-wide values and norms — always in context for all agents in the org.

**⚠️ Requires `promoted_by_user_id`.** Agents should SUGGEST principles via `write_org_knowledge`, not write them unilaterally. A human must explicitly promote.

**Input:**
```json
{
  "query_key": "simplicity-over-cleverness",
  "title": "We prefer simplicity over cleverness",
  "content": "When choosing between a clever solution and a simple one, choose simple...",
  "org_id": "uuid",
  "promoted_by_user_id": "uuid"
}
```

### write_context (deprecated)

Legacy tool. Defaults to `scope=PROJECT`, `always_inject=false` (equivalent to `write_knowledge`). Use purpose-built tools instead.

**Input:**
```json
{
  "query_key": "string",
  "title": "string",
  "content": "string",
  "project_id": "uuid",
  "source_file": "string?",
  "source_lines": "[int, int]?",
  "gotchas": ["string"]?,
  "related": ["string"]?,
  "scope": "PROJECT|AGENT|ORG",
  "always_inject": false,
  "chunk_type": "KNOWLEDGE"
}
```

---

## Read and Search Tools

### search_context

Semantic search across accessible chunks. Supports filtering by scope, chunk type, agent, project, org, and always_inject status.

**Input:**
```json
{
  "query": "how does authentication work",
  "project_id": "uuid",        // filter to PROJECT scope
  "agent_id": "uuid",          // filter to AGENT scope
  "org_id": "uuid",            // filter to ORG scope
  "scope": "PROJECT",          // filter to specific scope
  "chunk_type": "KNOWLEDGE",   // filter by chunk type
  "always_inject_only": true,  // only always_inject chunks
  "query_key": "auth-flow",    // filter by exact query_key
  "limit": 10                  // max results (default 10)
}
```

**Output:**
```json
{
  "results": [
    {
      "id": "uuid",
      "query_key": "auth-shared-cookie",
      "title": "Auth uses NextAuth v5 with shared cookie",
      "content": "...",
      "scope": "PROJECT",
      "chunk_type": "KNOWLEDGE",
      "always_inject": false,
      "score": 0.95,
      "source_file": "src/auth/config.ts",
      "created_at": "2026-03-19T12:00:00Z",
      "updated_at": "2026-03-19T14:30:00Z",
      "version": 2
    }
  ],
  "total": 3
}
```

### read_context

Retrieve a specific chunk by ID or query_key.

**Input:**
```json
{
  "id": "uuid",               // by chunk ID
  "query_key": "auth-flow",   // OR by query_key (requires project_id)
  "project_id": "uuid"        // required when reading by query_key
}
```

### get_context_versions

View version history of a chunk.

**Input:** `{ "id": "uuid", "limit": 10 }`

### compact_context

Fetch chunks matching a query for agent-side compaction. **The server does NOT summarize** — it returns raw chunks for the agent to synthesize client-side.

**Input:**
```json
{
  "query": "auth system",
  "project_id": "uuid",
  "scope": "PROJECT",
  "chunk_type": "RESEARCH",
  "limit": 50
}
```

**Expected workflow after calling:**
1. Agent reviews returned chunks
2. Agent summarizes into a new, higher-signal chunk
3. Agent writes the summary back via `write_knowledge` (or appropriate tool)
4. Agent optionally deletes or supersedes the original chunks

---

## Update and Delete Tools

### update_context

Versioned update to an existing chunk. Creates a new version while preserving history.

**Input:**
```json
{
  "id": "uuid",
  "title": "string?",
  "content": "string?",
  "source_file": "string?",
  "source_lines": "[int, int]?",
  "gotchas": ["string"]?,
  "related": ["string"]?,
  "change_note": "Added password reset info"
}
```

### delete_context

Remove a chunk.

**Input:** `{ "id": "uuid" }`

---

## Review Tool

### review_context

Rate chunk quality after using it. Follows the `add_doc_review` pattern from the original development MCP.

**Input:**
```json
{
  "chunk_id": "uuid",
  "task": "Added password reset functionality",
  "usefulness": 4,
  "usefulness_note": "Gotchas about token expiry were helpful",
  "correctness": 5,
  "correctness_note": "All info was accurate",
  "action": "useful"
}
```

**Actions:** `useful`, `needs_update`, `outdated`, `incorrect`

---

## Project Management Tools

Available only to `admin` and `orchestrator` agent types.

### list_projects

List all projects the agent has access to.

### list_agents

List all agents in the org.

### create_project

Create a new project.

**Input:**
```json
{
  "name": "My Project",
  "slug": "my-project"
}
```

### add_agent_to_project / remove_agent_from_project

Manage agent-project assignments.

**Input:** `{ "agent_id": "uuid", "project_id": "uuid" }`

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

*Last updated: 2026-03-19*
