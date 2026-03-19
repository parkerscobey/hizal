# Agent Onboarding & Provisioning Guide

This guide covers two flows: **human provisioning** (setting up a new agent) and **agent self-setup** (for self-hosters using MCP tools directly).

---

## Understanding Scope

Before provisioning, understand the three scopes:

| Scope | What it stores | Who sees it | Example |
|-------|---------------|------------|---------|
| **PROJECT** | What the project knows — shared, factual, technical | All agents on this project | Architecture docs, API patterns, deploy config |
| **AGENT** | What this agent knows — personal, interpretive, identity | Only this agent | Observations, preferences, working patterns |
| **ORG** | What the org believes — shared values, principles, norms | All agents in the org | Team values, cross-project standards |

### always_inject

Orthogonal to scope. Chunks with `always_inject=true` are surfaced automatically — they don't need to be searched for. They form the behavioral baseline.

| Scope + always_inject | Tool | Use |
|----------------------|------|-----|
| PROJECT + always_inject | `write_convention` | Foundational rules every agent must know |
| PROJECT + on-demand | `write_knowledge` | Facts retrieved when relevant |
| AGENT + always_inject | `write_identity` | Who this agent is |
| AGENT + on-demand | `write_memory` | Episodic observations |
| ORG + always_inject | `store_principle` | Org-wide values for all agents |
| ORG + on-demand | `write_org_knowledge` | Org facts retrieved when relevant |

---

## Human Provisioning (via Wizard — coming soon)

The provisioning wizard walks through these steps:

### Step 1: Agent Basics
- Name, type (dev / admin / research / orchestrator / custom)
- Enable memory toggle (`memory_enabled`) — enables `write_identity` and `write_memory`
- Platform (OpenClaw / Cursor / Claude Code / OpenCode / Custom)

### Step 2: Identity Setup
Guided prompts generate `write_identity` chunks:
- What is this agent's role?
- What projects will they work on?
- Who do they coordinate with?
- What are their core working principles?

Preview the always_inject identity chunk before writing.

### Step 3: Org Principles (if none exist)
Only shown if the org has no `store_principle` chunks:
- Guided prompts: what does your org always believe?
- Each principle previewed and confirmed by a human before writing
- Agents can only suggest — human must confirm

### Step 4: Project Connections
- Which projects will this agent work on?
- For each project: does it have `write_convention` chunks?
  - If yes: show them so the human knows what the agent will always see
  - If no: prompt to add at least one foundational convention

### Step 5: Behavior Driver Output
Generate a ready-to-copy AGENTS.md snippet tailored to this agent:
- Hizal MCP config
- When to call each tool
- Consolidation instructions
- Session recovery guidance

### Step 6: API Key
- Generate an agent-scoped API key
- Show MCP config snippet (JSON + TOML) ready to paste

---

## Agent Self-Setup (CLI / Self-Hosted)

For self-hosters who want to provision agents manually using MCP tools.

### 1. Connect to Hizal MCP

**JSON MCP clients (Claude Desktop, Cursor, OpenClaw, OpenCode):**
```json
{
  "mcpServers": {
    "hizal": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer ctx_your-org_YOUR_KEY_HERE"
      }
    }
  }
}
```

**Codex CLI (config.toml):**
```toml
[mcp_servers.hizal]
url = "https://winnow-api.xferops.dev/mcp"
http_headers = { Authorization = "Bearer ctx_your-org_YOUR_KEY_HERE" }
```

### 2. Set Up Identity

```
write_identity(
  query_key="agent-identity",
  title="[Agent Name] — [Role]",
  content="I'm [name], the [role] at [org]. My responsibilities include...
    I coordinate with [team members]. My working principles are...
    I focus on [project areas].",
  agent_id="<agent-uuid>"
)
```

This chunk is `always_inject=true` — it will be in every session automatically.

**Guideline:** Set identity once at provisioning. Update only when role or core values meaningfully change. Updating more than once a month is a signal something is wrong.

### 3. Set Up Org Principles (if needed)

First, check what exists:
```
search_context(scope="ORG", always_inject_only=true, org_id="<org-uuid>")
```

If no principles exist, propose them via `write_org_knowledge` first, then have a human promote:
```
store_principle(
  query_key="simplicity-over-cleverness",
  title="We prefer simplicity over cleverness",
  content="When choosing between a clever solution and a simple one, choose simple...",
  org_id="<org-uuid>",
  promoted_by_user_id="<human-uuid>"
)
```

**Guideline:** Principles are always in context for every agent. Keep them few and durable. Never write unilaterally from a single observation.

### 4. Connect to Projects

```
list_projects()
```

For each project, check conventions:
```
search_context(project_id="<project-uuid>", always_inject_only=true)
```

If no conventions exist, add at least one foundational rule:
```
write_convention(
  query_key="pr-required",
  title="All changes require a PR",
  content="Never push directly to main. Every change goes through a pull request...",
  project_id="<project-uuid>"
)
```

### 5. Configure AGENTS.md

Add Hizal session lifecycle to your agent's AGENTS.md or equivalent:

```markdown
## Every Session

1. Check for active session: `get_active_session()`
2. If none, start one: `start_session(lifecycle_slug="dev")`
3. Identity + conventions + principles are now in context
4. Register focus: `register_focus(task="...", project_id="...")`

## During Work

- Search before writing: `search_context(query="...", project_id="...")`
- Write findings: `write_knowledge(...)` for project facts
- Write observations: `write_memory(...)` for personal notes

## End of Session

- Compact if needed: `hizal-compact`
- Review used chunks: `hizal-review`
- End session: `end_session(session_id="...")`
```

### 6. Generate API Key

Via REST API:
```bash
curl -X POST https://winnow-api.xferops.dev/v1/keys \
  -H "Content-Type: application/json" \
  -d '{"org_slug": "your-org", "name": "agent-name"}'
```

Save the key — it's only shown once.

---

## What Good Identity Looks Like

A useful identity chunk should cover:
- **Role:** What this agent does in the org
- **Responsibilities:** Specific areas of ownership
- **Relationships:** Who they coordinate with
- **Principles:** How they approach work
- **Boundaries:** What they don't do

**Example:**
```
I'm Seth, the junior developer at XferOps. My responsibilities include
picking up feature tickets, writing code and tests, and creating PRs.
I coordinate with Adam (orchestrator), Quinn (QA), and Marcus (security).

My working principles:
- Read the codebase before writing code — understand existing patterns
- Ask when unsure — being wrong quietly is worse than asking loudly
- Never push to main — always create a PR
- Write tests alongside features, not after

I don't touch infrastructure, IAM, or production deployments.
```

---

## Always-Inject Guardrails

Before writing any always-inject chunk (`write_identity`, `write_convention`, `store_principle`), apply the three-question test:

1. **Will this still be true and relevant in 6 months?**
2. **Does every agent (or this agent in every session) need to know this?**
3. **Would NOT knowing this cause a meaningful mistake?**

If any answer is no — use the on-demand equivalent (`write_knowledge`, `write_memory`, `write_org_knowledge`) instead.

**Rule of thumb by tool:**
- `write_identity`: Set once at provisioning. Update only when role or core values meaningfully change.
- `write_convention`: Foundational rules only. If a human would put it in a permanent README section titled "Rules," it belongs here.
- `store_principle`: Requires human intent. Agents should propose, humans promote.

---

*Last updated: 2026-03-19*
