package api

const agentOnboardingGuideMarkdown = `# Winnow Agent Onboarding Guide

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

- orgs
- users
- projects
- project_memberships
- agents
- agent_projects
- api_keys
- context_chunks
- context_chunk_versions
- context_reviews

The most important operational boundary is the project. Context is written and searched within a project scope.

## How Agents Are Expected To Work

Default operating loop:

1. search_context for the topic or subsystem
2. read_context for the most relevant chunks
3. if context is missing, stale, or incomplete, inspect the codebase and write a new chunk with write_context or fix an existing one with update_context
4. before a handoff, after a long session, or when working memory gets crowded, call compact_context, summarize the returned chunks client-side, and write back a compacted summary
5. after using context to complete work, call review_context to rate usefulness and correctness

## MCP Tools You Can Use

- list_projects
- search_context
- read_context
- write_context
- update_context
- get_context_versions
- compact_context
- review_context
- delete_context

Current chunk schema is structured around:

- query_key
- title
- content
- source_file
- source_lines
- gotchas
- related

## Current Auth And Project Scoping Behavior

Winnow exposes MCP at /mcp and expects a bearer API key in Authorization.

Project scoping matters:

- Context operations require a project scope.
- For MCP tools, pass project_id in tool arguments.
- Context REST routes still accept project_id query param or X-Project-ID header.

## MCP Setup

JSON MCP client format:

` + "```json" + `
{
  "mcpServers": {
    "winnow": {
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
[mcp_servers.winnow]
url = "https://winnow-api.xferops.dev/mcp"
http_headers = { Authorization = "Bearer <agent-api-key>" }
` + "```" + `

## How To Onboard A New Agent

1. Call this onboarding endpoint with your API key.
2. Inspect available_projects or call list_projects and pick a project if needed.
3. Pass that project_id on MCP tool calls.
4. Use search_context to find architecture, auth, data model, deployment, and recent change context.
5. Read the top results before touching code.
6. If foundational context is missing, create it immediately.
7. At handoff, compact the relevant topic and write a summary chunk for the next agent.

## What Good Context Looks Like

A useful context chunk should answer:

- What is this subsystem or decision?
- Which files matter?
- What are the gotchas?
- What related topics should a future agent inspect next?

Preferred chunk shape:

- query_key
- title
- content
- source_file and source_lines
- gotchas
- related

## Recommended Operating Rules For Agents

- Search before writing.
- Prefer updating an existing chunk over creating a second overlapping chunk.
- Include concrete file references whenever knowledge came from the codebase.
- Use compaction aggressively.
- Review context after using it.
- If a chunk seems foundational, inspect version history before trusting it blindly.
`
