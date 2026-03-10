# Self-Hosting Winnow

Run Winnow locally with Docker Compose for development or private deployment.

## Prerequisites

- Docker + Docker Compose
- An OpenAI API key (for embeddings)
- Git

## Quick Start

```bash
git clone https://github.com/XferOps/winnow.git
cd winnow
cp .env.example .env
# edit .env: add your OPENAI_API_KEY
docker compose up
```

Winnow will be live at `http://localhost:8080`.

---

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | ✅ | — | Postgres connection string (set automatically by Compose) |
| `OPENAI_API_KEY` | ✅ | — | Used for generating text embeddings |
| `PORT` | — | `8080` | Port the API listens on |

---

## Docker Compose

Create `docker-compose.yml` in the repo root:

```yaml
version: "3.9"

services:
  postgres:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_DB: winnow
      POSTGRES_USER: winnow
      POSTGRES_PASSWORD: winnow
    volumes:
      - pgdata:/var/lib/postgresql/data
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U winnow"]
      interval: 5s
      timeout: 5s
      retries: 10

  api:
    build: .
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://winnow:winnow@postgres:5432/winnow?sslmode=disable
      OPENAI_API_KEY: ${OPENAI_API_KEY}
      PORT: 8080
    ports:
      - "8080:8080"

volumes:
  pgdata:
```

Create `.env.example`:

```env
OPENAI_API_KEY=sk-...
```

---

## First Run

```bash
# Start services
docker compose up -d

# Verify it's healthy
curl http://localhost:8080/health
# → { "status": "ok", "version": "0.1.0" }

# Create your first API key
curl -X POST http://localhost:8080/v1/keys \
  -H "Content-Type: application/json" \
  -d '{"org_slug": "my-team"}'
# → { "key": "ctx_my-team_..." }
```

---

## MCP Config for Local Dev

```json
{
  "mcpServers": {
    "winnow-local": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer ctx_my-team_YOUR_LOCAL_KEY"
      }
    }
  }
}
```

---

## Database Migrations

Schema migrations run automatically on startup via the Go binary. No manual migration step needed.

The API uses pgvector for semantic search. The `pgvector/pgvector:pg16` image includes the extension pre-installed.

---

## Building from Source

```bash
# Build the Docker image
docker build -t winnow-api .

# Or build the binary directly (requires Go 1.23+)
go build -o server ./cmd/server
./server
```

---

## Production Considerations

For a production self-hosted deployment:

1. **Use a managed Postgres** (RDS, Supabase, Neon) with pgvector enabled
2. **Set a strong, random `POSTGRES_PASSWORD`**
3. **Use HTTPS** — put the API behind a reverse proxy (nginx, Caddy, Cloudflare Tunnel)
4. **Store `OPENAI_API_KEY` in a secret manager**, not in plain `.env`
5. **Set resource limits** on the Docker containers

---

## Troubleshooting

**`vector` extension not found**

Make sure you're using the `pgvector/pgvector:pg16` image, not plain `postgres:16`. Plain Postgres doesn't include pgvector.

**Connection refused on port 8080**

Check `docker compose logs api` — the API might be waiting for the DB healthcheck to pass. Wait a few seconds after `docker compose up`.

**Embeddings failing**

Verify your `OPENAI_API_KEY` is valid: `curl https://api.openai.com/v1/models -H "Authorization: Bearer $OPENAI_API_KEY"` should return a list of models.
