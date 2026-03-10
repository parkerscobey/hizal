# Winnow: Problem Statement & Research Sources

## Executive Summary

Winnow is a context management platform for AI coding agents. It helps agents operate in the "smart zone" (efficient context usage) rather than the "dumb zone" (context overwhelm causing degraded performance).

## Problem Statement

### The Core Problem

AI coding tools perform poorly in large, complex ("brownfield") codebases because developers use them incorrectly. The problem isn't the models — it's poor context management.

### Specific Pain Points

1. **The "Slop Cycle"**
   - Ask AI to write code → produces incorrect code
   - Developer corrects → AI drifts further
   - Context window fills with noise
   - Result: code churn, technical debt, wasted time

2. **The "Dumb Zone"**
   - Large context windows degrade model performance
   - Around ~40% context usage, quality declines
   - Beyond that: reasoning quality drops, mistakes increase
   - Agents become unreliable despite having "more information"

3. **Context Doesn't Compound**
   - Each agent starts from scratch
   - No shared learning between runs
   - Tribal knowledge walks out the door

4. **Poor Onboarding**
   - New agents must explore everything fresh
   - No "compressed truth" to speed understanding

---

## Research Sources

### 1. Dex Horthy — "No Vibes Allowed: Solving Hard Problems in Complex Codebases"

**Speaker:** Dex Horthy, HumanLayer

**Key Concepts Adopted:**

| Concept | Description | Winnow Alignment |
|---------|-------------|---------------------|
| Context Engineering | Actively managing what info is in the context window | Core to design |
| Dumb Zone | Context >40% causes performance degradation | Drives compaction feature |
| Context Compaction | Summarize and compress before continuing | `compact_context` tool |
| Sub-agent Isolation | Use agents for context isolation, not role-playing | Context chunks are isolated |
| RPI Workflow | Research → Plan → Implement | Skills map to each phase |
| Compression of Truth | Research should produce compressible artifacts | Write = research compression |
| On-demand Context | Generate context when needed, not static docs | Dynamic, agent-written |

**Quote:**
> "AI can solve complex problems in large codebases today, but only when developers deliberately manage context, avoid the dumb zone, and use structured workflows that keep humans responsible for the thinking."

---

### 2. The Original Development MCP

**Source:** Pike13's internal development MCP

**What it does:**
- Serves documentation and metadata via MCP
- Full toolset: `ping`, `search_docs`, `read_file`, `write_doc`, `recent_migrations`, `rails_routes`, `env_info`, `business_terms`, `add_doc_review`

**Key capabilities:**
- **Read:** `search_docs`, `read_file`, `rails_routes`, `recent_migrations`, `business_terms`, `env_info`
- **Write:** `write_doc` (create/overwrite/append/patch markdown docs), `add_doc_review` (quality ranking system)

**What it lacks:**
- Context compaction (no way to compress/summarize)
- Context chunks aren't optimized for agent consumption (flat markdown, not structured data)
- No human-accessible UI for reading context
- Compounding is limited (docs get stale, no version tracking per-chunk)

**Lessons Learned:**

| What Works | What Doesn't |
|------------|--------------|
| Full-text search across docs | Context chunks are flat markdown, not structured |
| Domain-specific terms (business_terms) | No compaction/summarization |
| Code-aware queries (routes, migrations) | No review/validation workflow for agents |
| Quality tracking (add_doc_review) | Humans can't easily read raw context |
| Write capability (write_doc) | No human UI equivalent |

**Gap Winnow fills:**
- Structured context chunks (not flat markdown)
- Context compaction (summarize and reset)
- Human-readable UI for viewing context
- Persistent, compounding context (survives sessions)

---

## The "Why Now" Question

1. **AI coding is mainstream** — Most code will be AI-generated
2. **Context windows are getting bigger** — But the "dumb zone" problem gets worse
3. **Teams are adapting** — Workflow redesign is the competitive advantage
4. **The tool we want doesn't exist** — Docs platforms, not context platforms

---

## Design Principles (Derived)

1. **Context over documentation** — Malleable, agent-created, compounds over time
2. **Smart zone by default** — Tools designed to avoid the dumb zone
3. **RPI-native** — Each phase supported explicitly
4. **Compounds** — Each task makes future tasks easier
5. **Queryable** — Agents search, don't browse

---

## Open Questions

- [x] Should context be persistent (survives sessions) or ephemeral (per-session)?
  - **Answer:** Context should be persistent. Agents should always be writing context for future usage.

- [x] How do we validate context quality?
  - **Answer:** Follow the original development MCP's lead with `add_doc_review`. Implement a paired skill that allows the Agent to intelligently review context. If the user is displeased with the generation, they can direct the agent to review the context — the context may be partially to blame. The review data should feed back into improving context quality.

- [x] What's the minimal viable write schema?
  - **Answer:** Defined in 03-mcp-tools.md — see `write_context` tool. Core fields: query_key, title, content. Optional: source_file, source_lines, gotchas, related.

- [ ] How do we handle context conflicts?
  - **Answer:** Open question. Suggested approach: a server-side AI workflow audits context chunks for inconsistencies and flags them for the agent to review. Reviewing conflicts should be done via a separate set of skills, available to higher-permissioned API keys.

- [x] Should humans be able to read/write context too?
  - **Answer:** Yes. We need a UI for humans to read raw context chunks in the same structure that Agents read them. Essentially, a human-accessible equivalent of what the MCP provides agents.

---

*Last updated: 2026-03-08*
*Status: Draft / Iterating*