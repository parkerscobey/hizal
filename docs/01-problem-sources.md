# Hizal: Problem Statement & Research Sources

## Executive Summary

Hizal is behavior-driven memory infrastructure for AI agents. It helps agents maintain identity, follow conventions, and accumulate knowledge across sessions — not just answer queries, but behave consistently and improve over time.

## Problem Statement

### The Core Problem

AI agents forget everything between sessions. Every new conversation starts from zero — rediscovering architecture, re-reading codebases, violating conventions they followed yesterday, repeating mistakes they already learned from. Context loss is the single biggest drag on agent productivity.

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

4. **Agent Identity Drift**
   - Without persistent identity, agents behave inconsistently across sessions
   - The same agent acts differently depending on what's in the context window
   - Conventions get violated because they aren't reliably present
   - No behavioral continuity between conversations

5. **Context Loss Across Sessions**
   - Agents forget personal discoveries, preferences, and working patterns
   - Episodic observations ("this API silently fails when X") are lost
   - Each session re-discovers the same gotchas
   - No mechanism for agents to build personal experience over time

6. **Poor Onboarding**
   - New agents must explore everything fresh
   - No "compressed truth" to speed understanding

---

## Research Sources

### 1. Dex Horthy — "No Vibes Allowed: Solving Hard Problems in Complex Codebases"

**Speaker:** Dex Horthy, HumanLayer

**Key Concepts Adopted:**

| Concept | Description | Hizal Alignment |
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

**What it lacks:**
- Context compaction (no way to compress/summarize)
- No scoping (project, agent, org)
- No always-inject behavior (ambient context)
- No agent identity or memory
- No human-accessible UI for reading context

**Gap Hizal fills:**
- Three scopes (PROJECT, AGENT, ORG) with always_inject behavior
- Purpose-built write tools (not generic doc writes)
- Agent identity and episodic memory
- Session lifecycle with consolidation
- Human-readable UI for viewing context

---

## The "Why Now" Question

1. **AI coding is mainstream** — Most code will be AI-generated
2. **Context windows are getting bigger** — But the "dumb zone" problem gets worse, not better
3. **Teams are adapting** — Workflow redesign is the competitive advantage
4. **Agent memory is unserved** — Existing tools manage documents, not agent behavior
5. **The tool we want doesn't exist** — We built Hizal because we needed it

---

## Design Principles (Derived)

1. **Behavior over lookup** — Memory should modulate how agents act, not just answer queries
2. **Smart zone by default** — Tools designed to avoid the dumb zone
3. **Always-inject for behavior** — Conventions and identity are ambient, not searched
4. **Compounds** — Each session makes future sessions more productive
5. **Queryable** — Agents search, don't browse
6. **Agent-maintained** — Context quality improves through agent feedback loops, not just human curation
7. **Compaction > accumulation** — Agents should synthesize and compress, not just pile up

---

*Last updated: 2026-03-19*
