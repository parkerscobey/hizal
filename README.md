# Hizal

**Structured memory for AI agents.** Hizal gives your agents persistent, searchable context that survives across sessions — so they stop forgetting everything and start getting better over time.

[![CI](https://github.com/XferOps/winnow/actions/workflows/ci.yml/badge.svg)](https://github.com/XferOps/winnow/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

---

## The Problem

AI coding agents forget everything between sessions. Every new conversation starts from zero — re-reading codebases, violating conventions they followed yesterday, repeating mistakes they should have learned from.

Bigger context windows don't fix this. More room to forget isn't memory. The answer isn't more context — it's **structured, persistent knowledge** that agents build and maintain over time.

## What Hizal Does

Hizal stores structured context chunks with semantic search, versioning, and three-scope ownership. Agents write what they learn. Future agents search and reuse it. Context is deterministically injected instead of evaporating.

| Without Hizal | With Hizal |
|----------------|-------------|
| Agent re-reads the codebase every session | Agent searches existing knowledge in seconds |
| Conventions violated repeatedly | Conventions deterministically injected every session |
| Identity drifts between sessions | Identity persists and loads automatically |
| Knowledge scattered across sessions | Structured chunks with semantic search |
| Org norms exist only in human heads | Principles injected across all agents, all sessions |
| Each session starts from zero | Each session builds on all previous sessions |

---

## What Makes Hizal Different

### Three Scopes of Ownership

Every chunk in Hizal has a scope. This isn't a filter — it's an ownership model that determines who can see, write, and benefit from each piece of knowledge.

| Scope | Owner | Visibility | Example |
|-------|-------|-----------|---------|
| **PROJECT** | The project | All agents on this project | Architecture, API patterns, deploy config |
| **AGENT** | The individual agent | Only this agent | Personal observations, learned patterns, identity |
| **ORG** | The organization | All agents in the org | Team values, cross-project standards, org structure |

This matters because not all knowledge is the same. An agent's personal observation about a tricky API ("this endpoint silently 401s without the tenant resolver") is AGENT-scoped memory. The architectural decision behind that behavior is PROJECT-scoped knowledge. The org's principle that "we prefer explicit errors over silent failures" is ORG-scoped.

### Deterministic Context Injection

Most context systems are retrieval-only — agents search and hope the right context surfaces. Hizal has a second mode: **always_inject**.

Chunks marked `always_inject=true` are deterministically loaded into every session. No search required. No chance of missing them. They form a behavioral baseline:

- **Identity** (`write_identity`) — who the agent is, always present, AGENT scope
- **Conventions** (`write_convention`) — project rules every agent must follow, PROJECT scope
- **Principles** (`store_principle`) — org values across all agents, ORG scope

This is the difference between "the agent can look up the coding standards" and "the agent always knows the coding standards." Retrieval is probabilistic. Injection is deterministic. Both matter — Hizal gives you both.

### Purpose-Built Primitives

Instead of a generic `write(scope, inject, type)` API, Hizal provides six named tools whose names communicate intent and automatically set the right scope and injection behavior:

| Tool | Scope | Injected | Purpose |
|------|-------|----------|---------|
| `write_identity` | Agent | ✅ always | Who this agent is — role, values, working style |
| `write_memory` | Agent | on demand | Episodic observations — personal lessons, gotchas |
| `write_knowledge` | Project | on demand | Shared facts — architecture, patterns, decisions |
| `write_convention` | Project | ✅ always | Foundational rules — PRs required, naming conventions |
| `write_org_knowledge` | Org | on demand | Org-wide facts — team composition, product history |
| `store_principle` | Org | ✅ always | Org values — requires human approval, never agent-unilateral |

The tool name IS the instruction. An agent calling `write_convention` doesn't need to think about scopes or injection flags — the primitive handles it. This reduces cognitive load for agents and eliminates miscategorization.

### Agent Types and Lifecycle Presets

Not every agent should see every tool. A junior dev agent shouldn't have `create_project`. A research agent doesn't need `store_principle`.

Hizal ships four global agent types — **dev**, **admin**, **research**, **orchestrator** — each with a curated tool surface. Orgs can define custom types that inherit from these or start fresh.

Session lifecycles work the same way. The `dev` lifecycle gives a 12-hour session with standard inject scopes. The `orchestrator` lifecycle gives 24 hours. Orgs can define custom lifecycles with their own TTLs, required steps, and consolidation thresholds.

Both are primitives: agent types control *what tools are visible*, lifecycles control *how sessions behave*. Compose them to match your workflow.

### Typed Chunks with Consolidation Behavior

Every chunk has a `chunk_type` that describes its content and determines how it's handled at session end:

| Type | Consolidation | Examples |
|------|--------------|---------|
| IDENTITY | keep | Agent role, values, working style |
| CONVENTION | keep | PR rules, naming standards |
| PRINCIPLE | keep | Org values, team norms |
| KNOWLEDGE | keep | Architecture, patterns, decisions |
| DECISION | keep | Architectural choices with rationale |
| CONSTRAINT | keep | Hard boundaries, security rules |
| MEMORY | surface at session end | Personal observations, learned patterns |
| RESEARCH | surface at session end | Investigations, explorations |
| PLAN | surface at session end | Implementation plans |
| SPEC | surface at session end | Feature specifications |
| LESSON | surface at session end | Distilled learnings |

**"Keep"** types persist silently. **"Surface"** types are returned by `end_session` for the agent (or orchestrator) to review — promote to knowledge, keep as memory, or discard. This is how ephemeral observations get curated into durable institutional knowledge.

**NOTE:** Orgs can register custom chunk types with their own consolidation behavior.

### Sessions

Sessions are first-class objects in Hizal, not an afterthought. They track the full arc of an agent's work:

```
start_session(lifecycle_slug="dev")
  │
  ├─ Identity, conventions, and principles injected automatically
  ├─ register_focus(task="WNW-42: billing webhooks", project_id="...")
  │
  ├─ During work:
  │   ├─ search_context → find existing knowledge
  │   ├─ write_knowledge → share project facts
  │   └─ write_memory → record personal observations
  │
  └─ end_session(session_id="...")
       └─ Returns MEMORY/RESEARCH/PLAN chunks for review and promotion
```

`start_session` does the heavy lifting: it looks up the agent's type, selects the right lifecycle preset, and deterministically injects all relevant always_inject chunks (identity, conventions, principles) before the agent writes a single line of code. `end_session` surfaces ephemeral chunks so they can be curated into durable knowledge.

Sessions also track metrics — chunks read, chunks written, resume count — so you can see how agents actually use context over time.

**See [`AGENTS.md`](./AGENTS.md) for a complete example** of how a dev agent session works end-to-end: start session → read spec → search context → write code → open PR → update spec → end session.

---

## Quickstart

### 1. Self-host or use the hosted service

**Self-host:**
```bash
git clone https://github.com/XferOps/winnow.git
cd hizal
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
    "hizal": {
      "url": "https://your-hizal-instance/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}
```

### 3. Your agent now has persistent memory

```
# Start a session — identity, conventions, principles load automatically
start_session(lifecycle_slug="dev")

# Search existing knowledge
search_context(query="how does auth work", project_id="...")

# Write what you learned
write_knowledge(
  query_key="auth-flow",
  title="JWT verification in middleware",
  content="The auth middleware validates JWTs by...",
  project_id="..."
)

# End session — MEMORY chunks returned for review
end_session(session_id="...")
```

Next session — knowledge is still there. Identity loads automatically. Conventions are always in context.

---

## Architecture

```
┌────────────┐      ┌────────────┐      ┌──────────────────┐
│ Your Agent │─MCP─▶│ Hizal API │─────▶│ PostgreSQL       │
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

### Design Philosophy: Primitives → Simple Experiences

Every feature is built as a primitive first, then assembled into simple experiences:

1. **Primitives** — MCP tools (`search_context`, `write_knowledge`, `compact_context`) are powerful and composable
2. **Simple experiences for agents** — Skills (`hizal-seed`, `hizal-research`, `hizal-plan`) are guided workflows built on primitives
3. **Simple experiences for humans** — UI dashboards for viewing and managing context

Power users access primitives directly. Guided workflows use the same primitives underneath. Nobody outgrows the product.

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

Pre-built workflows composed from MCP primitives:

| Skill | Purpose |
|-------|---------|
| `hizal-seed` | Populate a new project with foundational context |
| `hizal-research` | Investigate a topic, fill knowledge gaps |
| `hizal-plan` | Create implementation plans validated against context |
| `hizal-compact` | Merge overlapping chunks into cleaner summaries |
| `hizal-review` | Rate and improve context quality |
| `hizal-onboard` | Get up to speed on a project fast |

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

Built by [XferOps](https://xferops.com). We run a team of AI agents building software. Hizal is how they remember.

*The name comes from [mycorrhizal](https://en.wikipedia.org/wiki/Mycorrhiza) — the underground fungal network that connects trees, letting them share nutrients and warnings. Hizal does the same thing for AI agents.*
