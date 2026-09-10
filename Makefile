.PHONY: up down run run-api sync-once migrate test lint tidy

up:
	docker compose up -d postgres redis
	$(MAKE) migrate
	docker compose up -d --build

down:
	docker compose down -v

run: run-api

run-api:
	go run ./cmd/api

# Apply SQL migrations (Postgres must be reachable on the compose network).
migrate:
	docker compose up -d postgres
	docker compose run --rm migrate

# One-shot sync (same as a CronJob tick).
sync-once:
	go run ./cmd/worker

test:
	go test ./...

tidy:
	go mod tidy

lint:
	go vet ./...
