.PHONY: build run test migrate-up migrate-down docker-build

# Load .env if it exists
-include .env
export

BINARY     = server
BUILD_DIR  = ./bin
MIGRATIONS = ./internal/db/migrations

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-w -s" -o $(BUILD_DIR)/$(BINARY) ./cmd/server
	@echo "✅ Built $(BUILD_DIR)/$(BINARY)"

run: build
	$(BUILD_DIR)/$(BINARY)

test:
	go test ./... -v -race -timeout 60s

migrate-up:
	migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" down 1

docker-build:
	docker build -t winnow:latest .
	@echo "✅ Docker image built: winnow:latest"

reset: migrate-down migrate-up seed
	@echo "Database reset complete"

clean: reset
	@echo "Run 'docker compose down -v' to remove volumes"
