# Winnow

Context management for AI coding agents.

## The Problem

AI coding tools struggle in large codebases not because models are incapable, but because context windows are finite. Current solutions—clipboard managers, clipboard managers with LLM summarization, or manual note-taking—are fragmented, lossy, and disconnected from the tools.

## The Solution

Winnow captures, organizes, and retrieves context for AI coding agents. Think of it as "a second brain for your AI coding tools."

## Status

**Researching / Designing** — Defining the product before implementation

## Architecture

Winnow is built as a standalone Go API with an MCP server interface:

- **API**: Go HTTP server (`cmd/server/`)
- **Storage**: PostgreSQL with pgvector for semantic search
- **MCP Tools**: Exposes context management tools to AI agents via the Model Context Protocol

## Development

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for local setup.
