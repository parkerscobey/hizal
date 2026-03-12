# Winnow Agent Onboarding Guide

This guide is for an AI agent being onboarded to Winnow itself.

Winnow is a context management platform for AI coding agents. Its job is to help agents stay effective in large codebases by storing research as structured, searchable context instead of forcing every new session to rediscover everything from scratch.

## What This Application Does

Winnow provides:

- An HTTP API for orgs, projects, agents, memberships, and API keys
- An MCP server over HTTP+SSE so coding agents can use Winnow tools directly
- A Postgres-backed store of versioned context chunks with embeddings
- Workflows for research, planning, compaction, review, and handoff

The core product idea is simple:

1. An agent researches a topic.
2. The agent writes what it learned as a context chunk.
3. Later agents search and reuse that chunk.
4. When context becomes noisy or stale, agents compact or update it.
5. After using context, agents review it so the knowledge base improves over time.

Important architectural constraint: Winnow does not do server-side summarization. The server stores, searches, versions, and returns chunks. The agent performs synthesis client-side and writes improved context back.

## Core Domain Model

The main persisted entities are:

- `orgs`: top-level tenant boundary
- `users`: human users
- `projects`: project-level context boundary
- `project_memberships`: human access to projects
- `agents`: named agent records owned inside an org
- `agent_projects`: projects an agent is assigned to
- `api_keys`: bearer keys used for MCP and context access
- `context_chunks`: versioned knowledge units scoped to a project
- `context_chunk_versions`: historical versions of each chunk
- `context_reviews`: usefulness/correctness reviews on chunks

The most important operational boundary is the `project`. Context is written and searched within a project scope.

## How Agents Are Expected To Work

An agent should not start by reading random files. It should use Winnow first.

Default operating loop:

1. `search_context` for the topic or subsystem.
2. `read_context` for the most relevant chunks.
3. If context is missing, stale, or incomplete, inspect the codebase and write a new chunk with `write_context` or fix an existing one with `update_context`.
4. Before a handoff, after a long session, or when working memory gets crowded, call `compact_context`, summarize the returned chunks client-side, and write back a compacted summary.
5. After using context to complete work, call `review_context` to rate usefulness and correctness.

This is the intended workflow reflected in `docs/03-mcp-tools.md`, `docs/04-skills.md`, and `docs/05-workflows.md`.

## MCP Tools You Can Use

Implemented MCP tools:

- `search_context`
- `read_context`
- `write_context`
- `update_context`
- `get_context_versions`
- `compact_context`
- `review_context`
- `delete_context`

Current schema is structured around:

- `query_key`
- `title`
- `content`
- `source_file`
- `source_lines`
- `gotchas`
- `related`

Do not assume tag-based or free-form document APIs. Winnow stores structured chunks, not generic notes.

## Current Auth And Project Scoping Behavior

Winnow exposes MCP at `/mcp` and expects a bearer API key in `Authorization`.

Project scoping matters:

- Context operations require a project scope.
- The MCP server reads project scope from the `X-Project-ID` header.
- Tool handlers fail if `project_id` is missing.

For practical onboarding, each agent should be treated as operating in one explicit project context at a time.

Recommended MCP config shape:

```json
{
  "mcpServers": {
    "winnow": {
      "url": "https://winnow-api.xferops.dev/mcp",
      "headers": {
        "Authorization": "Bearer <agent-api-key>",
        "X-Project-ID": "<project-uuid>"
      }
    }
  }
}
```

## How To Onboard A New Agent

When a new agent starts on a project:

1. Connect to the Winnow MCP server with the agent API key and the target `X-Project-ID`.
2. Search for high-level project context first:
   - architecture
   - auth
   - data model
   - deployment
   - current roadmap or recent changes
3. Read the top results and build a mental model before touching code.
4. If foundational context is missing, create it immediately.
5. During work, keep chunks narrow and factual. One chunk should usually describe one concept, subsystem, flow, or decision.
6. At handoff, compact the relevant topic and write a summary chunk for the next agent.

## What Good Context Looks Like

A useful context chunk should answer:

- What is this subsystem or decision?
- Which files matter?
- What are the gotchas?
- What related topics should a future agent inspect next?

Preferred chunk shape:

- `query_key`: stable grouping key such as `auth-middleware` or `project-memberships`
- `title`: short descriptive summary
- `content`: concise explanation of the implementation or decision
- `source_file` and `source_lines`: concrete trace back into the codebase
- `gotchas`: edge cases, permissions, stale-doc risks, hidden constraints
- `related`: neighboring query keys or chunks

## Recommended Operating Rules For Agents

- Search before writing. Duplicate chunks reduce retrieval quality.
- Prefer updating an existing chunk over creating a second chunk with overlapping facts.
- Include concrete file references whenever the knowledge came from the codebase.
- Use compaction aggressively. The point is to preserve learning without carrying every detail in-session.
- Review context after using it. Winnow is supposed to improve over repeated use.
- If a chunk seems foundational, inspect version history before trusting it blindly.

## Important Reality Check About `skills/`

The local `skills/` directory is useful as workflow guidance, but several examples are not aligned with the currently implemented MCP schema.

Examples of mismatch:

- Some skills show `projectId` in tool arguments. The implemented MCP server reads project scope from `X-Project-ID`.
- Some skills use fields like `tags` or `source`. The implemented tool schema uses `query_key`, `title`, `content`, `source_file`, `source_lines`, `gotchas`, and `related`.
- Some skills describe `compact_context(ids=[...])`. The implemented tool accepts a semantic `query` and returns matching chunks for agent-side summarization.
- Some review examples use a single `rating`. The implemented review flow tracks usefulness, correctness, notes, and an action.

Treat the `skills/` files as workflow intent, not as the exact API contract. When there is a conflict, follow the implemented handlers and MCP schemas.

## Suggested System Prompt For A Winnow-Connected Agent

Use Winnow as your first stop for project knowledge. Before exploring the codebase, search existing context and read the most relevant chunks. If context is missing, stale, or incomplete, research the code and write or update structured chunks with file references and gotchas. Before handoff or after a long session, compact the relevant topic and write back a summary. After relying on context to complete work, review its usefulness and correctness.

## High-Value First Chunks To Seed

If the knowledge base is still sparse, seed these first:

- Project architecture overview
- Authentication and authorization model
- Org/project/agent/API key relationships
- MCP request flow and project scoping behavior
- Data model and migrations overview
- Deployment and environment configuration

These give a new agent enough structure to become useful quickly.
