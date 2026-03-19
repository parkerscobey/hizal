# Winnow

**Structured memory for AI agents.** Winnow gives your agents persistent, searchable context that survives across sessions — so they stop forgetting everything and start getting better over time.

[![CI](https://github.com/XferOps/winnow/actions/workflows/ci.yml/badge.svg)](https://github.com/XferOps/winnow/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

---

## The Problem

AI coding agents forget everything between sessions. Every new conversation starts from zero — re-reading codebases, violating conventions they followed yesterday, repeating mistakes they already learned from.

Bigger context windows don't fix this. They make it worse. Past ~40% context utilization, model quality degrades — the "dumb zone." The answer isn't more context. It's **better context**, managed deliberately.

## What Winnow Does

Winnow stores structured context chunks with semantic search, versioning, and scoping. Agents write what they learn. Future agents search and reuse it. Context compounds instead of evaporating.

| Without Winnow | With Winnow |
|----------------|-------------|
| Agent re-reads the codebase every session | Agent searches existing knowledge in seconds |
| Conventions violated repeatedly | Conventions always in context (`always_inject`) |
| Identity drifts between sessions | Identity persists via `write_identity` |
| Context window fills with noise | Compaction keeps agents in the smart zone |
| Each session starts from zero | Each session builds on all previous sessions |

---

## Quickstart

### 1. Self-host or use the hosted service

**Self-host:**
```bash
git clone https://github.com/XferOps/winnow.git
cd winnow
cp .env.example .env  # configure DATABASE_URL and OPENAI_API_KEY
make migrate
make dev
```

**Hosted:** [winnow-api.xferops.dev](https://winnow-api.xferops.dev) — sign up for an API key.

### 2. Connect your agent via MCP

Add to your MCP config (Claude Desktop, Cursor, OpenClaw, OpenCode, or any MCP client):

```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://your-winnow-instance/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}
```

### 3. Your agent now has persistent memory

```
# Search existing knowledge
search_context(query="how does auth work", project_id="...")

# Write what you learned
write_knowledge(
  query_key="auth-flow",
  title="JWT verification in middleware",
  content="The auth middleware validates JWTs by...",
  project_id="..."
)

# Next session — knowledge is still there
search_context(query="auth") → returns the chunk you wrote
```

---

## Core Concepts

### Three Scopes

Every chunk lives in a scope that determines who sees it:

| Scope | Visibility | Example |
|-------|-----------|---------|
| **PROJECT** | All agents on this project | Architecture, API patterns, deploy config |
| **AGENT** | Only this agent | Personal observations, work preferences |
| **ORG** | All agents in the org | Team values, cross-project standards |

### Always Inject

Chunks marked `always_inject=true` are surfaced automatically — no search required. They form the behavioral baseline that shapes every interaction.

- **Identity** (`write_identity`) — who the agent is, always present
- **Conventions** (`write_convention`) — project rules, always present
- **Principles** (`store_principle`) — org values, always present

### Purpose-Built Write Tools

Six tools whose names communicate intent:

| Tool | Scope | Always Inject | Purpose |
|------|-------|---------------|---------|
| `write_identity` | Agent | ✅ | Who this agent is |
| `write_memory` | Agent | No | Episodic observations |
| `write_knowledge` | Project | No | Shared project facts |
| `write_convention` | Project | ✅ | Foundational rules |
| `write_org_knowledge` | Org | No | Org-wide facts |
| `store_principle` | Org | ✅ | Org values (human-approved) |

### Sessions

Track agent work across a conversation:

```
start_session → identity + conventions injected automatically
  ↓
register_focus(task="...", project_id="...")
  ↓
work: search, write_knowledge, write_memory
  ↓
end_session → returns MEMORY chunks for review
```

### Chunk Types

Label what a chunk contains: `KNOWLEDGE`, `MEMORY`, `CONVENTION`, `IDENTITY`, `PRINCIPLE`, `DECISION`, `RESEARCH`, `PLAN`, `SPEC`, `IMPLEMENTATION`, `CONSTRAINT`, `LESSON`.

Types are metadata — Winnow labels chunks, it doesn't enforce state machines.

### Compaction

When context gets noisy, `compact_context` fetches related chunks so the agent can synthesize them into a single, higher-signal chunk. Stays in the smart zone.

---

## Architecture

```
┌────────────┐      ┌────────────┐      ┌──────────────────┐
│ Your Agent │─MCP─▶│ Winnow API │─────▶│ PostgreSQL       │
│            │      │  (Go)      │      │ + pgvector       │
└────────────┘      └────────────┘      └──────────────────┘
                          │
                    ┌─────▼─────┐
                    │  OpenAI   │
                    │ Embeddings│
                    └───────────┘
```

- **Go API** with MCP server (HTTP+SSE transport)
- **PostgreSQL 16** with pgvector for semantic search
- **text-embedding-3-small** for embeddings ($0.02/1M tokens)
- **No server-side LLM** — agents do all reasoning client-side

---

## MCP Tools

| Category | Tools |
|----------|-------|
| **Session** | `start_session`, `resume_session`, `get_active_session`, `register_focus`, `end_session` |
| **Write** | `write_identity`, `write_memory`, `write_knowledge`, `write_convention`, `write_org_knowledge`, `store_principle` |
| **Read** | `search_context`, `read_context`, `get_context_versions`, `compact_context` |
| **Maintain** | `update_context`, `delete_context`, `review_context` |
| **Admin** | `list_projects`, `list_agents`, `create_project`, `add_agent_to_project` |

Full reference: [`docs/03-mcp-tools.md`](./docs/03-mcp-tools.md)

---

## Agent Skills

Pre-built workflows for common patterns:

| Skill | Purpose |
|-------|---------|
| `winnow-seed` | Populate a new project with foundational context |
| `winnow-research` | Investigate a topic, fill knowledge gaps |
| `winnow-plan` | Create implementation plans validated against context |
| `winnow-compact` | Compress noisy context into high-signal summaries |
| `winnow-review` | Rate and improve context quality |
| `winnow-onboard` | Get up to speed on a project fast |

Skills live in `skills/` — each has a `SKILL.md` with full workflow instructions.

---

## REST API

Same capabilities as MCP, for non-MCP integrations:

```
POST   /v1/context              # write
GET    /v1/context/search       # search
GET    /v1/context/compact      # compact
GET    /v1/context/:id          # read
PATCH  /v1/context/:id          # update
DELETE /v1/context/:id          # delete
GET    /v1/context/:id/versions # history
POST   /v1/context/:id/review   # review
GET    /health                  # health
```

Full reference: [`docs/api-reference.md`](./docs/api-reference.md)

---

## Documentation

| Doc | Contents |
|-----|---------|
| [`docs/01-problem-sources.md`](./docs/01-problem-sources.md) | Problem statement and research |
| [`docs/02-architecture.md`](./docs/02-architecture.md) | System design and data model |
| [`docs/03-mcp-tools.md`](./docs/03-mcp-tools.md) | MCP tool reference |
| [`docs/04-skills.md`](./docs/04-skills.md) | Agent skill specifications |
| [`docs/05-workflows.md`](./docs/05-workflows.md) | Session lifecycle and workflows |
| [`docs/06-agent-onboarding.md`](./docs/06-agent-onboarding.md) | Agent provisioning guide |
| [`CONTRIBUTING.md`](./CONTRIBUTING.md) | How to contribute |

---

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for development setup and guidelines.

## License

Apache License 2.0 — see [`LICENSE`](./LICENSE).

---

Built by [XferOps](https://xferops.com). We run a team of AI agents building software. Winnow is how they remember.
