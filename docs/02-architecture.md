# Winnow: Architecture

## System Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         Winnow Platform                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────────┐    │
│  │   Client    │────▶│   API       │────▶│   Storage       │    │
│  │   (Agent)   │     │   Layer     │     │   (Postgres)    │    │
│  └─────────────┘     └──────┬──────┘     └─────────────────┘    │
│                             │                                   │
│                      ┌──────▼──────┐                            │
│                      │  MCP Server │                            │
│                      │ (HTTP/stdio)│                            │
│                      └─────────────┘                            │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
         ▲
         │ Push context (optional)
         │
┌────────┴────────┐
│  Customer       │
│  Codebase       │
│  (optional)     │
└─────────────────┘
```

---

## Core Components

### 0. Human UI (Web Interface)

**Responsibilities:**
- Human-readable view of all context chunks
- Read-only access to context (mirrors MCP read tools)
- Review/feedback interface for quality validation
- Search and browse functionality

**Key feature:** Humans should be able to read context in the same structure that Agents read them.

---

### 1. API Layer

**Responsibilities:**
- Authentication (API key validation)
- Authorization (permissions per key)
- Request validation
- Rate limiting

**Endpoints:**
```
POST /v1/context          # Write context
GET  /v1/context/:id      # Read context
GET  /v1/context/search   # Search contexts
GET  /v1/context/compact  # Fetch chunks for agent-side compaction
DELETE /v1/context/:id    # Delete context
```

### 1. Storage (Postgres)

**Tables:**

```sql
-- Organizations (companies, teams)
CREATE TABLE orgs (
  id UUID PRIMARY KEY,
  name VARCHAR(255),
  slug VARCHAR(100) UNIQUE,  -- for URLs
  tier VARCHAR(50),  -- 'free', 'starter', 'growth', 'enterprise'
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Users (can belong to multiple orgs and projects)
CREATE TABLE users (
  id UUID PRIMARY KEY,
  email VARCHAR(255) UNIQUE,
  name VARCHAR(255),
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- User-Org membership (many-to-many)
CREATE TABLE org_memberships (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  org_id UUID REFERENCES orgs(id),
  role VARCHAR(50),  -- 'owner', 'admin', 'member'
  created_at TIMESTAMP,
  UNIQUE(user_id, org_id)
);

-- Projects (each has its own context space)
CREATE TABLE projects (
  id UUID PRIMARY KEY,
  org_id UUID REFERENCES orgs(id),
  name VARCHAR(255),
  slug VARCHAR(100),
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  UNIQUE(org_id, slug)
);

-- User-Project membership (many-to-many)
CREATE TABLE project_memberships (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  project_id UUID REFERENCES projects(id),
  role VARCHAR(50),  -- 'owner', 'contributor', 'viewer'
  created_at TIMESTAMP,
  UNIQUE(user_id, project_id)
);

-- API Keys (belong to users, scoped to all or fixed projects)
CREATE TABLE api_keys (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  key_hash VARCHAR(255) UNIQUE,
  name VARCHAR(100),  -- "Production key", "Dev key"
  scope_all_projects BOOLEAN DEFAULT false,
  allowed_project_ids UUID[],  -- if scope_all_projects is false
  permissions JSONB,  -- ['read', 'write', 'compact', 'review']
  created_at TIMESTAMP,
  last_used_at TIMESTAMP
);

-- Context Chunks (scoped to project)
CREATE TABLE context_chunks (
  id UUID PRIMARY KEY,
  project_id UUID REFERENCES projects(id),
  query_key VARCHAR(255),  -- for grouping, e.g., "auth-system"
  title VARCHAR(500),
  content JSONB,  -- flexible schema for different context types
  source_file VARCHAR(500),  -- optional file reference
  source_lines JSONB,  -- optional line numbers
  created_by_agent VARCHAR(255),
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Context Versions (for history/compaction)
CREATE TABLE context_versions (
  id UUID PRIMARY KEY,
  chunk_id UUID REFERENCES context_chunks(id),
  content JSONB,
  compacted_from JSONB,  -- IDs of chunks that were compacted
  created_at TIMESTAMP
);

-- Context Reviews (quality tracking - inspired by the original development MCP add_doc_review)
CREATE TABLE context_reviews (
  id UUID PRIMARY KEY,
  chunk_id UUID REFERENCES context_chunks(id),
  task VARCHAR(500),  -- what the agent was working on
  usefulness INTEGER,  -- 1-5 scale
  usefulness_note TEXT,
  correctness INTEGER,  -- 1-5 scale
  correctness_note TEXT,
  action VARCHAR(100),  -- 'useful', 'needs_update', 'outdated', 'incorrect'
  created_at TIMESTAMP
);
```

**Search:**
- Semantic search using pgvector (embeddings stored alongside chunks)
- Full-text search via `tsvector` as fallback/complement

### 3. MCP Server

**Protocol:** HTTP + SSE (or stdio for local)

**Tools exposed:**

| Tool | Input | Output |
|------|-------|--------|
| `write_context` | `{query_key, title, content, source?, gotchas?}` | `{id, created_at}` |
| `search_context` | `{query, limit?}` | `[{id, title, content, score}]` |
| `read_context` | `{id}` | `{id, query_key, title, content, ...}` |
| `compact_context` | `{query, limit?}` | `{chunks: [...], total}` |
| `review_context` | `{chunk_id, task, usefulness, correctness, action}` | `{id, created_at}` |

---

## Data Flow

### Write Flow

```
Agent
   │
   ▼
write_context({query_key: "auth", title: "Session handling", ...})
   │
   ▼
API Key validated → Tenant identified → Permissions checked
   │
   ▼
Stored in context_chunks table
   │
   ▼
Returns: {id: "ctx_xxx", created_at: "..."}
```

### Search Flow

```
Agent
   │
   ▼
search_context({query: "how does auth work?", limit: 10})
   │
   ▼
API Key validated → Tenant identified → Permissions checked
   │
   ▼
Full-text search over context_chunks.content
   │
   ▼
Returns: [{id, title, content, score}, ...]
```

### Compact Flow

**Important:** No server-side AI inference. The server fetches matching chunks; the agent does all summarization client-side.

```
Agent
   │
   ▼
compact_context({query: "auth", limit: 50})
   │
   ▼
API Key validated → Tenant identified → Permissions checked
   │
   ▼
Find all chunks matching "auth"
   │
   ▼
Returns: {chunks: [...], total: N}
   │
   ▼
Agent summarizes chunks locally (in its own context window)
   │
   ▼
Agent writes compacted summary back via write_context()
```

---

## Deployment Options

### Option A: SaaS (Managed)

```
api.winnow.io   ──▶  Your hosted Postgres
         │
         └── MCP runs on our servers
```

**Pros:**
- Zero setup, managed for customers
- Revenue-generating (subscription pricing)

**Cons:**
- Data leaves customer infrastructure

---

---

## Security Model

### Authentication

```
Authorization: Bearer dk_live_xxxxx_xxxxxxxx
```

- API key format: `dk_live_<tenant_id>_<random>`
- Keys stored as bcrypt hash

### Authorization

| Permission | write_context | search_context | compact_context |
|------------|---------------|----------------|-----------------|
| read       | ❌            | ✅             | ✅ (read only)  |
| write      | ✅            | ✅             | ✅              |
| compact    | ✅            | ✅             | ✅              |

---

## Open Questions

- [x] Vector search vs. full-text?
  - **Answer:** pgvector from day one for semantic search. Full-text search via tsvector as complement.
- [x] How to handle context conflicts?
  - **Approach:** Server-side AI workflow audits context chunks for inconsistencies and flags them. Review via separate high-permission skills.
- [ ] Should we support read ACLs per chunk?
- [x] How to validate context quality?
  - **Approach:** Follow the original development MCP's `add_doc_review` pattern. Agents can review context; users can direct agents to review context when generation is unsatisfactory.
- [ ] What's the pricing model?

---

## Billing Model (Stripe Integration)

```sql
-- Stripe Customer mapping
CREATE TABLE stripe_customers (
  id UUID PRIMARY KEY,
  org_id UUID REFERENCES orgs(id) UNIQUE,
  stripe_customer_id VARCHAR(100) UNIQUE,
  stripe_subscription_id VARCHAR(100),
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Subscriptions
CREATE TABLE subscriptions (
  id UUID PRIMARY KEY,
  org_id UUID REFERENCES orgs(id),
  tier VARCHAR(50),  -- 'free', 'starter', 'growth', 'enterprise'
  status VARCHAR(50),  -- 'active', 'past_due', 'canceled', 'trialing'
  
  -- Stripe data
  stripe_subscription_id VARCHAR(100),
  stripe_price_id VARCHAR(100),
  
  -- Period tracking
  current_period_start TIMESTAMP,
  current_period_end TIMESTAMP,
  cancel_at_period_end BOOLEAN DEFAULT false,
  
  -- Limits (denormalized for quick checks)
  max_projects INTEGER,
  max_users INTEGER,
  
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Daily usage for analytics (not billing)
CREATE TABLE daily_usage (
  id UUID PRIMARY KEY,
  subscription_id UUID REFERENCES subscriptions(id),
  date DATE,  -- YYYY-MM-DD
  
  -- Metrics (end-of-day snapshots for analytics)
  api_calls INTEGER DEFAULT 0,
  context_chunks_created INTEGER DEFAULT 0,
  context_chunks_read INTEGER DEFAULT 0,
  context_versions_created INTEGER DEFAULT 0,
  reviews_submitted INTEGER DEFAULT 0,
  
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  UNIQUE(subscription_id, date)
);
```

### How It Works

1. **Org signup** → Creates Stripe customer → Creates subscription
2. **Payment** → Stripe webhook updates subscription status
3. **Access check** → On API call, verify org's subscription is active
4. **Limits** → Enforce max_projects, max_users from subscription
5. **Self-hosted** → License key linked to subscription/org
6. **Analytics** → Daily usage recorded for dashboards/reporting

### Enforcement Points

- API key validation includes org + subscription status check
- Project/user creation checks limits against subscription
- MCP tools return 402 if subscription expired

### Usage Tracking (Analytics Only)

Daily snapshots for analytics dashboards — not used for billing in v0.1:
- API calls per day
- Context chunks created/read
- Reviews submitted

Later: Add overage billing if needed.

---

## Pricing Tiers (Future)

| Tier | Projects | Users | Features |
|------|----------|-------|----------|
| **Solo** | 1-3 | 1 | Basic MCP tools, limited storage |
| **Starter** | 5 | 3 | All MCP tools, reviews, compaction |
| **Growth** | 15 | 10 | Priority support, advanced analytics |
| **Enterprise** | Unlimited | Unlimited | Custom pricing, SLA, SSO |

### Tier Enforcement

- API keys can be scoped to projects
- Rate limits per tier
- Storage limits per project
- Feature flags based on tier

---

## Related Docs

- [Problem & Sources](./01-problem-sources.md)
- [MCP Tools Spec](./03-mcp-tools.md)
- [Skills](./04-skills.md)
- [Workflows](./05-workflows.md)

---

*Last updated: 2026-03-08*
*Status: Draft / Iterating*