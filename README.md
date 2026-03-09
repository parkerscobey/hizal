# Winnow

A context management platform for AI coding agents. Helps agents operate in the "smart zone" (efficient context usage) rather than the "dumb zone" (context overwhelm causing degraded performance).

## Why Winnow?

AI coding tools struggle in large codebases not because models are incapable, but because context is poorly managed. Winnow solves this by:

1. **Context compounds** — Each task makes future tasks easier
2. **Avoids the dumb zone** — Compaction prevents context overwhelm
3. **Versioned context** — Agents can update and track changes over time
4. **Quality feedback** — Review system to improve context quality
5. **Agent-created context** — Not static docs, but living knowledge

## Core Concepts

| Concept | Description |
|---------|-------------|
| Context Chunk | A small, composable piece of knowledge created by agents |
| Compaction | Summarizing context to reset the agent's state |
| Versioning | Context updates create new versions, preserving history |
| RPI Workflow | Research → Plan → Implement |
| Smart Zone | Efficient context usage (<40% of window) |

## Quick Links

- [Problem & Sources](./docs/01-problem-sources.md) — Why this exists
- [Architecture](./docs/02-architecture.md) — System design
- [MCP Tools](./docs/03-mcp-tools.md) — Tool specifications
- [Skills](./docs/04-skills.md) — Agent workflows
- [Workflows](./docs/05-workflows.md) — Visual diagrams

## MCP Tools

| Tool | Purpose |
|------|---------|
| `write_context` | Agent writes research finding |
| `search_context` | Find relevant context (sorted by relevance + recency) |
| `read_context` | Get specific context chunk with version info |
| `update_context` | Update existing chunk (creates new version) |
| `get_context_versions` | View version history of a chunk |
| `compact_context` | Fetch chunks for agent-side compaction |
| `review_context` | Quality review (usefulness, correctness) |

## Research Sources

- Dex Horthy: "No Vibes Allowed: Solving Hard Problems in Complex Codebases"
- The Original Development MCP (Pike13 internal)

## Status

**Researching / Designing** — Defining the product before implementation