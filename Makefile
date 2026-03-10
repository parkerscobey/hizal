.PHONY: build run test migrate-up migrate-down seed docker-build

# Load .env if it exists
-include .env
export

BINARY     = server
BUILD_DIR  = ./bin
MIGRATIONS = ./internal/db/migrations
SEEDS      = ./internal/db/seeds/001_dev_seed.sql
MIGRATE_IMAGE = migrate/migrate:v4.18.3
PSQL_IMAGE = postgres:16-alpine
DATABASE_URL_DOCKER = $(subst localhost,host.docker.internal,$(DATABASE_URL))

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-w -s" -o $(BUILD_DIR)/$(BINARY) ./cmd/server
	@echo "✅ Built $(BUILD_DIR)/$(BINARY)"

run: build
	$(BUILD_DIR)/$(BINARY)

test:
	go test ./... -v -race -timeout 60s

migrate-up:
	@if command -v migrate >/dev/null 2>&1; then \
		migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" up; \
	else \
		docker run --rm -v "$(PWD)/internal/db/migrations:/migrations" $(MIGRATE_IMAGE) -path=/migrations -database "$(DATABASE_URL_DOCKER)" up; \
	fi

migrate-down:
	@if command -v migrate >/dev/null 2>&1; then \
		migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" down 1; \
	else \
		docker run --rm -v "$(PWD)/internal/db/migrations:/migrations" $(MIGRATE_IMAGE) -path=/migrations -database "$(DATABASE_URL_DOCKER)" down 1; \
	fi

seed:
	@if command -v psql >/dev/null 2>&1; then \
		psql "$(DATABASE_URL)" -f $(SEEDS); \
	else \
		docker run --rm -v "$(PWD)/internal/db/seeds:/seeds" $(PSQL_IMAGE) psql "$(DATABASE_URL_DOCKER)" -f /seeds/001_dev_seed.sql; \
	fi

docker-build:
	docker build -t winnow:latest .
	@echo "✅ Docker image built: winnow:latest"

reset: migrate-down migrate-up seed
	@echo "Database reset complete"

clean: reset
	@echo "Run 'docker compose down -v' to remove volumes"
