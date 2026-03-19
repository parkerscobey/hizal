# Winnow

**Behavior-driven memory infrastructure for AI agents.** Winnow doesn't just store context — it modulates how agents think, remember, and collaborate across sessions.

- 🔗 **API:** https://winnow-api.xferops.dev
- 🖥️ **UI:** https://winnow.xferops.dev
- 📖 **Docs:** [`docs/`](./docs/)

---

## The Problem

AI agents forget everything between sessions. Every new conversation starts from zero — rediscovering architecture, re-reading codebases, repeating mistakes. Context loss is the single biggest drag on agent productivity.

The fix isn't a bigger context window. It's structured, persistent memory that shapes how agents behave.

| Problem | Winnow Solution |
|---------|----------------|
| Agents forget what they learned | Persistent memory across sessions (three scopes: project, agent, org) |
| Identity drifts between sessions | `write_identity` — always-injected agent identity |
| Conventions violated repeatedly | `write_convention` — foundational rules always in context |
| Context window fills with noise | Compaction resets state without losing knowledge |
| Knowledge goes stale | Versioned updates with full history |
| No shared organizational values | `store_principle` — org-wide norms for all agents |

---

## Beyond Lookup: Behavior-Driven Agents

Winnow is not a RAG system. It's behavior infrastructure.

Traditional context tools answer the question "what does the agent know?" Winnow answers a different question: **"how does the agent behave?"**

- **Identity chunks** (always injected) define who the agent is — role, values, working style
- **Convention chunks** (always injected) define the rules the agent must always follow
- **Principle chunks** (always injected) define org-wide values across all agents
- **Memory chunks** (retrieved on demand) capture episodic observations and learned patterns
- **Knowledge chunks** (retrieved on demand) store project facts, architecture, decisions

The `always_inject` flag is the key differentiator. Chunks marked `always_inject` are surfaced automatically as ambient context — they don't need to be searched for. They form the behavioral baseline that shapes every interaction.

---

## Quickstart (5 minutes)

### 1. Get an API key

```bash
curl -X POST https://winnow-api.xferops.dev/v1/keys \
  -H "Content-Type: application/json" \
  -d '{"org_slug": "your-org"}'
```

### 2. Configure your MCP client

**Claude Desktop / Cursor / OpenClaw / OpenCode** — add to your MCP config:

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

### 3. Start a session and work

```
start_session(lifecycle_slug="dev")
→ returns session_id, injects identity + conventions automatically

search_context(query="how does authentication work", project_id="...")
→ returns relevant chunks ranked by semantic similarity

write_knowledge(
  query_key="auth-flow",
  title="JWT verification in middleware",
  content="The auth middleware validates JWTs by...",
  project_id="...",
  source_file="internal/auth/middleware.go"
)
→ stores as PROJECT-scoped knowledge with embedding

end_session(session_id="...")
→ returns MEMORY chunks for review and promotion
```

---

## Three Scopes

Every chunk in Winnow has a scope that determines who sees it:

| Scope | Who sees it | Example |
|-------|------------|---------|
| **PROJECT** | All agents on this project | Architecture docs, API patterns, deployment config |
| **AGENT** | Only this agent | Personal observations, work preferences, episodic memory |
| **ORG** | All agents in the org | Team composition, org values, cross-project standards |

Scope is orthogonal to `always_inject`. A PROJECT convention is always in context for every agent on that project. An AGENT memory is retrieved on demand only for that agent.

---

## Purpose-Built Write Tools

Instead of a generic `write_context`, Winnow provides six tools whose names communicate intent:

| Tool | Scope | Always Inject | Use When |
|------|-------|---------------|----------|
| `write_identity` | AGENT | ✅ yes | Provisioning — who this agent is |
| `write_memory` | AGENT | no | During work — personal observations, learned patterns |
| `write_knowledge` | PROJECT | no | Sharing facts — architecture, patterns, decisions |
| `write_convention` | PROJECT | ✅ yes | Foundational rules every agent must always know |
| `write_org_knowledge` | ORG | no | Org-wide facts retrieved on demand |
| `store_principle` | ORG | ✅ yes | Org values — requires human intent |

The legacy `write_context` tool remains available (defaults to PROJECT scope, `always_inject=false`) but is deprecated in favor of these purpose-built tools.

### Guardrails for Always-Inject Tools

`write_identity`, `write_convention`, and `store_principle` consume context on **every call** for **every affected agent**. Before using them, ask:

1. Will this still be true and relevant in 6 months?
2. Does every agent (or this agent in every session) need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no — use `write_knowledge`, `write_memory`, or `write_org_knowledge` instead.

---

## Session Lifecycle

Winnow sessions track agent work across a conversation:

```
Session start
  └─ start_session(lifecycle_slug="dev")
  └─ Identity + org principles injected automatically (always_inject, AGENT + ORG scope)

First project engagement
  └─ Project conventions injected (always_inject, PROJECT scope)
  └─ register_focus(task="implement billing webhooks", project_id="...")

During work
  ├─ write_memory: episodic notes (memory-enabled agents)
  ├─ write_knowledge: project facts to share with team
  ├─ search_context: find relevant existing knowledge
  └─ compact_context: compress noisy chunks

Session end
  └─ end_session(session_id="...")
  └─ Returns MEMORY chunks written during session for review/promotion
```

---

## Chunk Types

Every chunk has a `chunk_type` that describes what it contains:

| Type | Purpose | Consolidation |
|------|---------|---------------|
| IDENTITY | Who this agent is | KEEP |
| MEMORY | Episodic observations | SURFACE at session end |
| KNOWLEDGE | Project facts | KEEP |
| CONVENTION | Foundational rules | KEEP |
| PRINCIPLE | Org values | KEEP |
| DECISION | Architectural decisions | KEEP |
| RESEARCH | Explorations, investigations | SURFACE |
| PLAN | Implementation plans | SURFACE |
| SPEC | Feature specifications | SURFACE |
| IMPLEMENTATION | Build notes | SURFACE |
| CONSTRAINT | Hard boundaries | KEEP |
| LESSON | Learned patterns | SURFACE |

Chunk types are metadata, not workflow state. Winnow labels chunks; it does not enforce transitions.

---

## Agent Types

Agents register with a type that determines which MCP tools they can access:

| Type | Tools | Use Case |
|------|-------|----------|
| `dev` | All read/write/session tools | Development agents |
| `admin` | All tools + project management | Orchestrator agents |
| `research` | Read/write/search/compact | Research-focused agents |
| `orchestrator` | All tools + create_project, list_agents, agent management | Full platform access |

---

## How We Use It

Winnow handles memory and context. It doesn't replace your task tracker or your orchestrator — it works alongside them.

**Our setup at XferOps:**

1. A long-running orchestrator agent (OpenClaw) receives a task via Telegram
2. The orchestrator searches Winnow for context, creates a spec, and spawns a dev agent (OpenCode)
3. The dev agent calls `start_session`, reads the spec, searches Winnow for relevant context, then implements and opens a PR
4. At session end, `end_session` returns MEMORY chunks for review and promotion

**The specification layer is deliberately generic.** Winnow doesn't care where your task spec comes from — Forge, Linear, Jira, a plain file, or a Winnow chunk itself.

---

## Documentation

| Doc | Contents |
|-----|---------|
| [`docs/01-problem-sources.md`](./docs/01-problem-sources.md) | Problem statement, research sources |
| [`docs/02-architecture.md`](./docs/02-architecture.md) | System design, data model, components |
| [`docs/03-mcp-tools.md`](./docs/03-mcp-tools.md) | Full MCP tool reference |
| [`docs/04-skills.md`](./docs/04-skills.md) | Agent skill specifications |
| [`docs/05-workflows.md`](./docs/05-workflows.md) | Session lifecycle and workflow diagrams |
| [`docs/06-agent-onboarding.md`](./docs/06-agent-onboarding.md) | Provisioning guide (wizard + self-hosted) |
| [`docs/api-reference.md`](./docs/api-reference.md) | REST API reference |

---

## Development

**v0.2** — Production API live at `winnow-api.xferops.dev`. Sessions, scopes, agent types, chunk types, and purpose-built write tools all shipped.

### Database Model Contract

- `internal/models` is the canonical package for database-backed types.
- Every table and column introduced by `internal/db/migrations/` must be reflected in `internal/models`.
- When API handlers or other packages scan rows from persisted tables, prefer scanning into `internal/models` types first.
- Keep join, aggregate, and transport-only response structs local to the package that serves them; do not add those shapes to `internal/models`.
