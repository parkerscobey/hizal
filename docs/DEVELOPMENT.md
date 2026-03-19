# Local Development

## Prerequisites

- Go 1.23+
- Docker (for Postgres + pgvector + Mailpit)

## Quick Start

```bash
cp .env.example .env
docker compose up -d
make migrate-up
make seed
make run
```

Verify the server:

```bash
curl http://localhost:8080/health
```

Open Mailpit at `http://localhost:8025` to inspect invite emails locally.

## Configuration

### Environment Variables

Copy `.env.example` to `.env`:

```bash
# Server
PORT=8080
ENV=development
# For local UI development (Vite on :5173)
CORS_ALLOW_ORIGINS=http://localhost:5173
# Or disable origin checks locally:
# CORS_ALLOW_ORIGINS=*

# Database (matches docker-compose.yml)
DATABASE_URL=postgres://user:password@localhost:5434/hizal?sslmode=disable

# App URLs
APP_BASE_URL=http://localhost:5173

# Local email delivery (Mailpit)
EMAIL_FROM=Hizal Local <noreply@hizal.local>
SMTP_HOST=localhost
SMTP_PORT=1025

# OpenAI
OPENAI_API_KEY=sk-...
```

### Database

The local Postgres runs on port 5434 to avoid conflicts:

```bash
# Reset database entirely:
docker compose down -v
docker compose up -d
make migrate-up
make seed
```

Or use the shortcut:

```bash
make reset
```

## Available Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Compile server to `./bin/server` |
| `make run` | Build and run the API locally |
| `make test` | Run Go tests with race detector |
| `make migrate-up` | Apply all migrations |
| `make migrate-down` | Roll back last migration |
| `make migrate-force VERSION=<n>` | Force recorded migration version and clear dirty state |
| `make seed` | Load seed data |
| `make reset` | Full DB reset (migrate-down + migrate-up + seed) |
| `make docker-build` | Build Docker image |

## Local Invite Flow

With the default local `.env`, invite emails are delivered to Mailpit instead of SES.

1. Start local services:
   ```bash
   docker compose up -d
   ```
2. Run the API:
   ```bash
   make run
   ```
3. Create an invite through the API or UI.
4. Open `http://localhost:8025`.
5. Open the invite email and click the `Accept Invitation` link.

If your frontend is running somewhere other than `http://localhost:5173`, update `APP_BASE_URL` to match so invite links open the correct local app.

## Troubleshooting

- If you have an old Postgres volume, reset it:
  ``` changed DB settings andbash
  docker compose down -v
  docker compose up -d
  make migrate-up
  make seed
  ```

- `make migrate-up` and `make seed` work even if `migrate`/`psql` are not installed locally (Docker fallbacks are built in).
