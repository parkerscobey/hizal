package api

const agentOnboardingGuideMarkdown = `# Hizal Agent Onboarding Guide

Hizal is behavior-driven memory infrastructure for AI agents. It stores structured, searchable context that persists across sessions — and uses always-inject chunks to shape how agents behave, not just what they can query.

## What This Application Does

Hizal provides:

- An HTTP API for orgs, projects, agents, memberships, and API keys
- An MCP server over HTTP+SSE so coding agents can use Hizal tools directly
- A Postgres-backed store of versioned context chunks with semantic search (pgvector)
- Three scopes (PROJECT, AGENT, ORG) with always_inject behavior
- Purpose-built write tools that route to the correct scope automatically
- Session lifecycle for tracking agent work and consolidating memory

Important architectural constraint: Hizal does not do server-side summarization. The server stores, searches, versions, and returns chunks. The agent performs synthesis client-side and writes improved context back.

## Core Domain Model

The main persisted entities are:

- orgs: top-level tenant boundary
- users: human users
- projects: project-level context boundary
- project_memberships: human access to projects
- agents: named agent records with types (dev, admin, research, orchestrator)
- agent_types: registered types that control tool visibility
- agent_projects: projects an agent is assigned to
- api_keys: bearer keys used for MCP and context access (agent_id derived from key)
- context_chunks: versioned knowledge units with scope, chunk_type, and always_inject flag
- context_chunk_versions: historical versions of each chunk
- context_reviews: usefulness/correctness reviews on chunks
- chunk_types: registered types (KNOWLEDGE, MEMORY, CONVENTION, IDENTITY, PRINCIPLE, DECISION, RESEARCH, PLAN, SPEC, IMPLEMENTATION, CONSTRAINT, LESSON)
- sessions: agent work sessions that track focus and memory

## Three Scopes

Every chunk has a scope:

- PROJECT: shared project knowledge — architecture, patterns, deployment config. Visible to all agents on the project.
- AGENT: private agent memory — identity, episodic observations, preferences. Visible only to this agent.
- ORG: org-wide knowledge — team composition, values, cross-project standards. Visible to all agents in the org.

## always_inject

Orthogonal to scope. Chunks with always_inject=true are surfaced automatically as ambient context — they don't need to be searched for. They form the behavioral baseline.

- PROJECT + always_inject = write_convention (foundational rules)
- AGENT + always_inject = write_identity (who this agent is)
- ORG + always_inject = store_principle (org-wide values)

## How Agents Are Expected To Work

### Session lifecycle

1. start_session(lifecycle_slug="dev") — creates a session, injects always_inject chunks
2. register_focus(task="...", project_id="...") — tell Hizal what you're working on
3. During work: search, read, write knowledge and memory
4. end_session(session_id="...") — returns MEMORY chunks for review/promotion

### Default operating loop

1. search_context for the topic or subsystem (pass project_id)
2. read_context for the most relevant chunks
3. If context is missing, stale, or incomplete:
   - write_knowledge for project facts worth sharing
   - write_memory for personal observations
   - update_context for fixing existing chunks
4. Before handoff or when context gets noisy: compact_context, summarize client-side, write back
5. After using context: review_context to rate usefulness and correctness

## MCP Tools

### Session lifecycle
- start_session: begin a work session
- resume_session: resume after restart
- get_active_session: check for existing session
- register_focus: declare current task
- end_session: end session, get MEMORY chunks for review

### Purpose-built write tools (preferred)
- write_identity: AGENT scope, always_inject=true — who this agent is
- write_memory: AGENT scope, always_inject=false — episodic observations
- write_knowledge: PROJECT scope, always_inject=false — project facts
- write_convention: PROJECT scope, always_inject=true — foundational rules
- write_org_knowledge: ORG scope, always_inject=false — org-wide facts
- store_principle: ORG scope, always_inject=true — org values (requires promoted_by_user_id)

### Read and search
- search_context: semantic search with scope, chunk_type, always_inject filters
- read_context: read by ID or query_key
- get_context_versions: version history
- compact_context: fetch chunks for agent-side compaction

### Other
- update_context: versioned update with change_note
- delete_context: remove a chunk
- review_context: rate usefulness and correctness
- write_context: legacy (deprecated — use purpose-built tools)
- list_projects: list accessible projects
- list_agents: list org agents (admin/orchestrator only)
- create_project: create project (admin/orchestrator only)
- add_agent_to_project / remove_agent_from_project: manage assignments

## Chunk Schema

All chunks support:
- query_key: stable grouping key (e.g. "auth-middleware")
- title: short descriptive title
- content: the knowledge (markdown or structured text)
- source_file: optional file path reference
- source_lines: optional [start, end] line numbers
- gotchas: optional list of warnings
- related: optional related query_keys
- scope: PROJECT | AGENT | ORG
- chunk_type: KNOWLEDGE | MEMORY | CONVENTION | IDENTITY | PRINCIPLE | DECISION | RESEARCH | PLAN | SPEC | IMPLEMENTATION | CONSTRAINT | LESSON
- always_inject: boolean

## MCP Setup

JSON MCP client format:

` + "```json" + `
{
  "mcpServers": {
    "hizal": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer <agent-api-key>"
      }
    }
  }
}
` + "```" + `

Codex CLI config.toml format:

` + "```toml" + `
[mcp_servers.hizal]
url = "https://winnow-api.xferops.dev/mcp"
http_headers = { Authorization = "Bearer <agent-api-key>" }
` + "```" + `

## How To Onboard A New Agent

1. Connect MCP with your API key
2. Call get_active_session() — resume if one exists, otherwise start_session()
3. Call list_projects to find your target project
4. Pass project_id on all tool calls
5. Check if context exists: search_context(query="*", project_id="...", limit=5)
6. If empty: use the hizal-seed skill to populate foundational context first
7. If context exists: search for architecture, auth, data model, deployment
8. Read the top results before touching code
9. register_focus(task="...", project_id="...") to declare your task
10. During work: write_knowledge for project facts, write_memory for personal observations
11. At session end: compact, review, then end_session

## Guardrails for Always-Inject Tools

write_identity, write_convention, and store_principle consume context on every call. Before using:

1. Will this still be true in 6 months?
2. Does every agent need to know this?
3. Would NOT knowing this cause a meaningful mistake?

If any answer is no — use write_knowledge, write_memory, or write_org_knowledge instead.

## What Good Context Looks Like

- query_key: stable grouping key like "auth-middleware"
- title: short descriptive summary
- content: concise explanation with concrete details
- source_file + source_lines: traceable to the codebase
- gotchas: edge cases, hidden constraints
- related: neighboring query keys for graph traversal

Search before writing. Prefer updating existing chunks over creating overlapping ones.
`
