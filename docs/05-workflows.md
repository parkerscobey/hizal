# Winnow: Workflows

## RPI Workflow Overview

Dex Horthy's Research → Plan → Implement workflow, mapped to Winnow tools:

```
┌─────────────────────────────────────────────────────────────────┐
│                    RPI Workflow with Winnow                  │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   ┌──────────┐     ┌──────────┐     ┌──────────┐                │
│   │ RESEARCH │────▶│  PLAN    │────▶│ IMPLEMENT│                │
│   └────┬─────┘     └────┬─────┘     └────┬─────┘                │
│        │                │                │                      │
│        ▼                ▼                ▼                      │
│   search_context    write_context    search_context             │
│   read_context      (plan as         read_context               │
│   write_context       context)                                  │
│                                                                 │
│   winnow-         winnow-       winnow-                │
│   research           plan             onboard (if handoff)      │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## Research Phase

### Flow

```
                    ┌─────────────────────┐
                    │  Start Task         │
                    │  (e.g., "add auth") │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  search_context     │
                    │  query: "[topic]"   │
                    └──────────┬──────────┘
                               │
                    ┌──────────┴───────────┐
                    │                      │
                    ▼                      ▼
               Context found?         No context
                    │                      │
          ┌─────────┴───────┐              │
          ▼                 ▼              ▼
    Read context      gaps exist?    Explore codebase
          │                 │              │
          ▼                 ▼              ▼
    Identify gaps    Write context    Read files
          │                 │              │
          └────────┬────────┘              │
                   ▼                       │
              Write new context ◀──────────┘
                   │
                   ▼
           Research complete
```

### Tools Used
- `search_context` — find existing knowledge
- `read_context` — dive into specific chunks
- `write_context` — capture new findings

---

## Plan Phase

### Flow

```
                    ┌─────────────────────┐
                    │  Research complete  │
                    │  (context written)  │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Review all context │
                    │  for task           │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Structure plan:    │
                    │  - File paths       │
                    │  - Line numbers     │
                    │  - Code patterns    │
                    │  - Test approach    │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Validate plan:     │
                    │  - Can explain?     │
                    │  - Specific?        │
                    │  - Testable?        │
                    └──────────┬──────────┘
                               │
                    ┌──────────┴──────────┐
                    │                     │
                    ▼                     ▼
               Plan good             Plan needs work
                    │                     │
                    ▼                     ▼
           Write as context      Revise with more detail
           (for review/handoff)           │
                    │                     │
                    └────────┬────────────┘
                             ▼
                       Plan ready
```

### Tools Used
- `read_context` — review research
- `write_context` — store plan (as context)

---

## Implement Phase

### Flow

```
                    ┌─────────────────────┐
                    │  Plan ready         │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Execute plan       │
                    │  (write code)       │
                    └──────────┬──────────┘
                               │
                    ┌──────────┴──────────┐
                    │                     │
                    ▼                     ▼
              Need more info?         No - continue
                    │                     │
                    ▼                     ▼
            search_context          Running long?
            read_context            (check context usage)
                    │                      │
                    └────────┬─────────────┘
                             ▼
                    ┌─────────────────────┐
                    │  Checkpoint:        │
                    │  - Context >40%?    │
                    └─────────────────────┐
                    │ Entered "dumb zone"?│
                    └──────────┬──────────┘
                               │
                    ┌──────────┴──────────┐
                    │                     │
                    ▼                     ▼
                   Yes                    No
                    │                     │
                    ▼                     ▼
           compact_context         Task complete
           (reset context)               │
           and continue                  ▼
                              ┌─────────────────────┐
                              │  Final: write       │
                              │  context (optional) │
                              └─────────────────────┘
```

### Tools Used
- `search_context` — find additional info mid-work
- `read_context` — review code while implementing
- `compact_context` — avoid/combat dumb zone

---

## Context Compaction Flow

### When to Compact

```
Context Usage
     │
  0% ├───────────────────────── Smart Zone ─────────────────────────▶
     │
 40% ├──────────────────────── Dumb Zone Start ──────────────────────▶
     │
 60% ├──────────────────────────────▶ [Quality degrades]
     │
100% ├───────────────────────────────────────────────────────────────▶
```

### Compaction Flow

**All summarization happens agent-side.** The server only fetches matching chunks.

```
                    ┌─────────────────────┐
                    │  Working on task    │
                    │  for 15-20 min      │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  compact_context    │
                    │  query: "[topic]"   │
                    │  (fetches chunks)   │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Agent summarizes   │
                    │  (client-side):     │
                    │  - What learned?    │
                    │  - Key files?       │
                    │  - Gotchas?         │
                    │  - Gaps?            │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  write_context()    │
                    │  (store compacted   │
                    │   summary)          │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Decision:          │
                    │  a) Start fresh     │
                    │     session         │
                    │  b) Continue with   │
                    │     compressed ctx  │
                    └─────────────────────┘
```

---

## Agent Handoff Flow

```
                    ┌─────────────────────┐
                    │  Agent A completes  │
                    │  task context       │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Compact context    │
                    │  purpose: handoff   │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Write summary      │
                    │  as context         │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Agent B starts     │
                    │  search_context     │
                    │  + read_context     │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Agent B has        │
                    │  context from A!    │
                    └─────────────────────┘
```

---

## Review Flow

### When to Review

```
After task completion (agent-initiated):
  └─> Used context to complete work
  └─> Rate usefulness and correctness
  └─> Submit review

On user feedback (user-directed):
  └─> User unhappy with agent generation
  └─> Agent reviews context that may have contributed
  └─> Update context if needed
```

### Review Flow Diagram

```
                    ┌─────────────────────┐
                    │  Task completed     │
                    │  (or user feedback) │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Identify context   │
                    │  used in task       │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Assess quality:    │
                    │  - Usefulness (1-5) │
                    │  - Correctness (1-5)│
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Determine action:  │
                    │  useful → done      │
                    │  needs_update → edit│
                    │  outdated → refresh │
                    │  incorrect → delete │
                    └──────────┬──────────┘
                               │
                    ┌──────────┴──────────┐
                    │                     │
                    ▼                     ▼
               Action: useful          Action needed
                    │                     │
                    ▼                     ▼
           review_context()         Update context chunk
                                        (write_context)
```

---

## File Structure

```
winnow/
├── docs/
│   ├── 01-problem-sources.md    # Problem, Dex talk, original MCP
│   ├── 02-architecture.md       # System design, components
│   ├── 03-mcp-tools.md          # MCP tool specifications
│   ├── 04-skills.md             # Agent skills (research, compact, etc.)
│   └── 05-workflows.md          # RPI workflow diagrams
├── diagrams/
│   └── (ASCII/mermaid diagrams)
└── README.md
```

---

*Last updated: 2026-03-08*
*Status: Draft / Iterating*
