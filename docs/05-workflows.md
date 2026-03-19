# Hizal: Workflows

## Agent Memory Lifecycle

This is the complete lifecycle for a Hizal-connected agent session:

```
Session start
  └─ start_session(lifecycle_slug="dev")
  └─ Identity injected (always_inject, AGENT scope)
  └─ Org principles injected (always_inject, ORG scope)

First project engagement
  └─ Project conventions injected (always_inject, PROJECT scope)
  └─ register_focus(task="...", project_id="...")

During work
  ├─ write_memory: episodic notes (memory-enabled agents)
  ├─ write_knowledge: project facts
  ├─ search_context: find relevant existing knowledge
  └─ compact_context: compress noisy chunks

Session end
  ├─ end_session: returns MEMORY chunks for review/promotion
  ├─ hizal-compact: merge noisy/overlapping chunks
  └─ hizal-review: rate chunks used heavily
```

---

## Full Hizal Lifecycle

```
┌─────────────────────────────────────────────────────────────────┐
│                    Hizal Lifecycle                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   ┌──────────┐     ┌──────────┐     ┌──────────┐               │
│   │   SEED   │────▶│ RESEARCH │────▶│  PLAN    │──▶ IMPLEMENT  │
│   └────┬─────┘     └────┬─────┘     └────┬─────┘               │
│        │                │                │                      │
│        ▼                ▼                ▼                      │
│   write_knowledge   search_context  write_knowledge             │
│   write_convention  write_knowledge (chunk_type=PLAN)           │
│   (day zero)        write_memory                                │
│                                                                 │
│   hizal-seed       hizal-         hizal-                     │
│   (first use)       research         plan                       │
│                                                                 │
│   ┌──────────┐     ┌──────────┐     ┌──────────┐               │
│   │ COMPACT  │◀────│ IMPLEMENT│────▶│  REVIEW  │               │
│   └──────────┘     └──────────┘     └──────────┘               │
│                                                                 │
│   compact_context  write_memory     review_context              │
│   write_knowledge  write_knowledge                              │
│                                                                 │
│   hizal-compact   (write code)     hizal-review               │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## RPI Workflow with Hizal

Research → Plan → Implement, mapped to Hizal's purpose-built tools:

```
┌─────────────────────────────────────────────────────────────────┐
│                    RPI Workflow with Hizal                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   ┌──────────┐     ┌──────────┐     ┌──────────┐               │
│   │ RESEARCH │────▶│  PLAN    │────▶│ IMPLEMENT│               │
│   └────┬─────┘     └────┬─────┘     └────┬─────┘               │
│        │                │                │                      │
│        ▼                ▼                ▼                      │
│   search_context    write_knowledge  search_context             │
│   read_context      (chunk_type=     write_memory               │
│   write_knowledge    PLAN)           write_knowledge            │
│   write_memory                       compact_context            │
│                                                                 │
│   hizal-research   hizal-plan     hizal-compact              │
│                                     hizal-review               │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## Session Start Flow

```
Agent starts
    │
    ▼
get_active_session()
    │
    ├── Active session found? ──▶ resume_session(session_id)
    │
    └── No active session ──▶ start_session(lifecycle_slug="dev")
                                    │
                                    ▼
                              Session created
                                    │
                                    ▼
                              Always-inject chunks loaded:
                              ├── AGENT scope: identity
                              ├── ORG scope: principles
                              └── PROJECT scope: conventions
                                    │
                                    ▼
                              register_focus(task="...", project_id="...")
                                    │
                                    ▼
                              Ready to work
```

---

## Research Phase

```
                    ┌─────────────────────┐
                    │  Start Task         │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  search_context     │
                    │  (project_id, topic)│
                    └──────────┬──────────┘
                               │
                    ┌──────────┴───────────┐
                    │                      │
                    ▼                      ▼
               Context found?         No context
                    │                      │
                    ▼                      ▼
            Read + check             Explore codebase
            staleness                      │
                    │                      ▼
                    ▼                write_knowledge
            Identify gaps            (project facts)
                    │                      │
                    ▼                write_memory
            write_knowledge          (personal observations)
            or update_context
```

---

## Compaction Flow

### When to Compact

```
Context Usage
     │
  0% ├───────────────── Smart Zone ──────────────────▶
     │
 40% ├───────────────── Dumb Zone Start ─────────────▶ [compact here]
     │
 60% ├───────────────── Quality degrades ────────────▶
     │
100% ├───────────────────────────────────────────────▶
```

### Flow

```
                    ┌─────────────────────┐
                    │  Context getting    │
                    │  noisy (15-20 min)  │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  compact_context    │
                    │  (fetches chunks)   │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Agent summarizes   │
                    │  (client-side)      │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  write_knowledge()  │
                    │  (compacted summary)│
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  delete_context()   │
                    │  (original chunks)  │
                    └─────────────────────┘
```

---

## Session End Flow

```
                    ┌─────────────────────┐
                    │  Work complete      │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  hizal-compact     │
                    │  (if chunks written)│
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  hizal-review      │
                    │  (rate used chunks) │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  end_session()      │
                    │  returns MEMORY     │
                    │  chunks for review  │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Review MEMORY:     │
                    │  ├── Promote to     │
                    │  │   write_knowledge│
                    │  ├── Keep as memory │
                    │  └── Discard        │
                    └─────────────────────┘
```

---

## Agent Handoff Flow

```
                    ┌─────────────────────┐
                    │  Agent A completes  │
                    │  work session       │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  hizal-compact     │
                    │  (save for handoff) │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  end_session()      │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Agent B starts     │
                    │  start_session()    │
                    │  + hizal-onboard   │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Agent B has        │
                    │  context from A +   │
                    │  identity + convs   │
                    └─────────────────────┘
```

---

*Last updated: 2026-03-19*
