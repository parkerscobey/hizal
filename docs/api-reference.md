# Winnow REST API Reference

Base URL: `https://winnow-api.xferops.dev`

## Authentication

All endpoints (except `/health` and `/v1/keys`) require:

```
Authorization: Bearer ctx_your-org_YOUR_KEY_HERE
```

### Error format

```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "context chunk not found"
  }
}
```

---

## Endpoints

### `GET /health`

Health check. No auth required.

**Response 200:**
```json
{
  "status": "ok",
  "version": "0.2.1"
}
```

---

### `POST /v1/keys`

Create a new API key. No auth required (bootstrap endpoint).

**Request:**
```json
{
  "org_slug": "my-team"
}
```

**Response 201:**
```json
{
  "key": "ctx_my-team_abc123..."
}
```

> ⚠️ The key is only returned once. Store it securely.

---

### `GET /api/v1/agent-onboarding`

Return dynamic onboarding data for an API-key-authenticated agent.

This endpoint is intended for agents and CLI usage. It returns:

- Long-form onboarding guidance in `guide_markdown`
- Links to agent workflow skills in `skills`
- Projects available to the current API key
- Whether the caller still needs to choose a project
- Suggested initial search queries and tool usage guidance

**Auth:**
```http
Authorization: Bearer ctx_your-org_YOUR_KEY_HERE
```

**Headers:**

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ | API key bearer token |
| `X-Project-ID` | — | Optional. If provided, echoed back as `selected_project_id` |

**Response 200:**
```json
{
  "application": "winnow",
  "version": "0.2.1",
  "guide_markdown": "# Winnow Agent Onboarding Guide\n...",
  "key": {
    "id": "key_123",
    "name": "agent key",
    "owner_type": "AGENT",
    "scope_all_projects": false
  },
  "org": {
    "id": "org_123",
    "name": "Acme",
    "slug": "acme"
  },
  "agent": {
    "id": "agent_123",
    "name": "Code Agent",
    "slug": "code-agent"
  },
  "owner": {
    "user_id": "user_123",
    "name": "Agent Owner"
  },
  "default_project_id": "project_123",
  "selected_project_id": null,
  "needs_project_selection": false,
  "skills": [
    {
      "id": "winnow-onboard",
      "title": "Winnow Onboard",
      "description": "Onboard to a project with Winnow by selecting project scope and reading high-signal context first.",
      "purpose": "Fast project orientation before coding.",
      "format": "markdown",
      "url": "/api/v1/skills/winnow-onboard"
    }
  ],
  "available_projects": [
    {
      "id": "project_123",
      "name": "API",
      "slug": "api",
      "selected": false
    }
  ],
  "mcp_endpoint": "/mcp",
  "context_api_base": "/v1/context",
  "recommended_start_queries": [
    "project overview architecture",
    "authentication authorization",
    "data model migrations",
    "deployment environment configuration",
    "recent changes roadmap"
  ],
  "tooling": {
    "implemented_tools": [
      "list_projects",
      "search_context",
      "read_context",
      "write_context",
      "update_context",
      "get_context_versions",
      "compact_context",
      "review_context",
      "delete_context"
    ],
    "required_headers": [
      "Authorization: Bearer <api-key>"
    ],
    "project_selection": "Pass project_id in MCP tool arguments. Context REST requests still accept project_id query param or X-Project-ID header."
  },
  "instructions": [
    "Use Winnow before exploring the codebase directly.",
    "Choose one project from available_projects or call list_projects, then pass project_id on subsequent MCP tool calls."
  ],
  "chunk_shape": {
    "required": ["query_key", "title", "content"],
    "optional": ["source_file", "source_lines", "gotchas", "related"]
  }
}
```

**Notes:**

- Call this endpoint without `X-Project-ID` if the agent first needs to discover available projects.
- If the key can access multiple projects, `needs_project_selection` will be `true` until the agent chooses one.
- Subsequent MCP tool calls should pass `project_id`.
- Subsequent context REST requests can still use `project_id` query param or `X-Project-ID`.

---

### `GET /api/v1/agents/:id/onboarding`

Return the same onboarding payload for a specific agent, but authenticated as a human user via JWT.

This endpoint is intended for the UI application and other user-authenticated tooling.

**Auth:**
```http
Authorization: Bearer <jwt>
```

**Path params:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | ✅ | Agent ID |

**Headers / query params:**

| Name | Required | Description |
|------|----------|-------------|
| `Authorization` | ✅ | JWT bearer token |
| `X-Project-ID` | — | Optional selected project |
| `project_id` | — | Optional selected project as query param |

**Response 200:**

Returns the same payload shape as `GET /api/v1/agent-onboarding`.

Skill links in this JWT-authenticated response use agent-scoped URLs like `/api/v1/agents/:id/skills/winnow-onboard`.

**Notes:**

- Access is limited to users who can access the target agent.
- This is the recommended endpoint for rendering a human-readable onboarding page in the UI.

### `GET /api/v1/skills/:id`

Return a served skill document for API-key-authenticated agents.

**Auth:**
```http
Authorization: Bearer ctx_your-org_YOUR_KEY_HERE
```

**Response 200:**
```json
{
  "id": "winnow-onboard",
  "title": "Winnow Onboard",
  "description": "Onboard to a project with Winnow by selecting project scope and reading high-signal context first.",
  "purpose": "Fast project orientation before coding.",
  "format": "markdown",
  "markdown": "---\nname: winnow-onboard\n..."
}
```

### `GET /api/v1/agents/:id/skills/:skillId`

Return the same skill document for a specific agent, but authenticated as a human user via JWT.

---

### `POST /v1/context`

Write a new context chunk.

**Request:**
```json
{
  "query_key": "auth-flow",
  "title": "JWT middleware validates tokens at every request",
  "content": "The auth middleware in internal/auth/middleware.go validates JWTs on every protected route. It checks expiry, signature, and extracts claims...",
  "source_file": "internal/auth/middleware.go",
  "source_lines": [42, 67],
  "gotchas": ["Token expiry is checked server-side, not just decoded"],
  "related": ["chunk-id-for-login-handler"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `query_key` | string | ✅ | Namespace tag for this chunk |
| `title` | string | ✅ | Short descriptor |
| `content` | string | ✅ | The actual knowledge content |
| `source_file` | string | — | File path this chunk relates to |
| `source_lines` | [int, int] | — | Line range `[start, end]` |
| `gotchas` | string[] | — | Known pitfalls or warnings |
| `related` | string[] | — | IDs of related chunks |

**Response 201:**
```json
{
  "id": "cuid_abc123",
  "query_key": "auth-flow",
  "title": "JWT middleware validates tokens at every request",
  "created_at": "2026-03-10T00:00:00Z"
}
```

---

### `GET /v1/context/search`

Semantic vector search across chunks.

**Query params:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | ✅ | Natural language search query |
| `limit` | int | — | Max results (default: 10) |
| `query_key` | string | — | Restrict to this namespace |

**Example:**
```
GET /v1/context/search?query=how+does+authentication+work&limit=5&query_key=auth-flow
```

**Response 200:**
```json
{
  "results": [
    {
      "id": "cuid_abc123",
      "query_key": "auth-flow",
      "title": "JWT middleware validates tokens at every request",
      "content": "...",
      "source_file": "internal/auth/middleware.go",
      "source_lines": [42, 67],
      "gotchas": ["Token expiry is checked server-side"],
      "related": [],
      "score": 0.92,
      "version": 2,
      "created_at": "2026-03-10T00:00:00Z",
      "updated_at": "2026-03-11T00:00:00Z"
    }
  ]
}
```

Results are sorted by relevance score (cosine similarity via pgvector) × recency.

---

### `GET /v1/context/compact`

Fetch chunks for client-side compaction. Returns the most relevant chunks for summarization.

**Query params:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | ✅ | Topic to compact around |
| `limit` | int | — | Max chunks to return (default: 20) |

**Example:**
```
GET /v1/context/compact?query=authentication+system&limit=15
```

**Response 200:**
```json
{
  "chunks": [
    {
      "id": "cuid_abc123",
      "query_key": "auth-flow",
      "title": "...",
      "content": "...",
      ...
    }
  ],
  "total": 15
}
```

> After receiving chunks, your agent should summarize them into a single new chunk via `POST /v1/context`, then delete the source chunks if desired.

---

### `GET /v1/context/:id`

Retrieve a specific context chunk with its full version history.

**Response 200:**
```json
{
  "id": "cuid_abc123",
  "query_key": "auth-flow",
  "title": "JWT middleware validates tokens at every request",
  "content": "...",
  "source_file": "internal/auth/middleware.go",
  "source_lines": [42, 67],
  "gotchas": ["Token expiry is checked server-side"],
  "related": [],
  "score": 0,
  "version": 2,
  "created_at": "2026-03-10T00:00:00Z",
  "updated_at": "2026-03-11T00:00:00Z",
  "versions": [
    {
      "version": 1,
      "change_note": "Initial write",
      "created_at": "2026-03-10T00:00:00Z"
    },
    {
      "version": 2,
      "change_note": "Updated after discovering expiry bug",
      "created_at": "2026-03-11T00:00:00Z"
    }
  ]
}
```

**Response 404:**
```json
{
  "error": { "code": "NOT_FOUND", "message": "..." }
}
```

---

### `PATCH /v1/context/:id`

Update a context chunk. Creates a new version, preserving history.

**Request** (all fields optional except `change_note`):
```json
{
  "title": "Updated title",
  "content": "Updated content reflecting new understanding...",
  "source_file": "internal/auth/middleware.go",
  "source_lines": [42, 90],
  "gotchas": ["Token expiry checked server-side", "New gotcha discovered"],
  "related": ["other-chunk-id"],
  "change_note": "Updated after code review revealed edge case"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | — | New title |
| `content` | string | — | New content |
| `source_file` | string | — | Updated file path |
| `source_lines` | [int, int] | — | Updated line range |
| `gotchas` | string[] | — | Replaces existing gotchas |
| `related` | string[] | — | Replaces existing related IDs |
| `change_note` | string | ✅ | Why this was updated |

**Response 200:**
```json
{
  "id": "cuid_abc123",
  "version": 3,
  "updated_at": "2026-03-12T00:00:00Z"
}
```

---

### `DELETE /v1/context/:id`

Permanently delete a context chunk.

**Response 200:**
```json
{
  "deleted": true,
  "id": "cuid_abc123"
}
```

---

### `GET /v1/context/:id/versions`

Retrieve the version history of a chunk.

**Query params:**

| Param | Type | Description |
|-------|------|-------------|
| `limit` | int | Max versions to return (default: all) |

**Response 200:**
```json
{
  "versions": [
    {
      "version": 1,
      "change_note": "Initial write",
      "created_at": "2026-03-10T00:00:00Z"
    },
    {
      "version": 2,
      "change_note": "Updated after discovering expiry bug",
      "created_at": "2026-03-11T00:00:00Z"
    }
  ]
}
```

---

### `POST /v1/context/:id/review`

Submit a quality review for a chunk.

**Request:**
```json
{
  "chunk_id": "cuid_abc123",
  "task": "Implementing OAuth login flow",
  "usefulness": 4,
  "usefulness_note": "Helped me understand the token validation flow",
  "correctness": 5,
  "correctness_note": "Verified against the actual source",
  "action": "useful"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `chunk_id` | string | ✅ | ID of chunk being reviewed |
| `task` | string | — | What task you were doing |
| `usefulness` | int 1-5 | ✅ | How useful was this chunk? |
| `usefulness_note` | string | — | Why that score |
| `correctness` | int 1-5 | ✅ | How accurate/correct? |
| `correctness_note` | string | — | Why that score |
| `action` | enum | ✅ | `useful` \| `needs_update` \| `outdated` \| `incorrect` |

**Response 201:**
```json
{
  "id": "review_xyz789",
  "chunk_id": "cuid_abc123",
  "created_at": "2026-03-12T00:00:00Z"
}
```

---

## HTTP Status Codes

| Code | Meaning |
|------|---------|
| 200 | Success |
| 201 | Created |
| 400 | Bad request / invalid body |
| 401 | Missing or invalid API key |
| 404 | Chunk not found |
| 503 | Database unavailable |
