-include .env
export

.PHONY: up down logs ps migrate migrate-down seed api worker web web-install web-build test test-integration lint fmt tidy

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f --tail=100

ps:
	docker compose ps

migrate:
	cd backend && go run ./cmd/migrate up

migrate-down:
	cd backend && go run ./cmd/migrate down

seed:
	cd backend && go run ./cmd/seed

api:
	cd backend && go run ./cmd/api

# Outbox relay plus the event consumer (e-mails, cache invalidation).
worker:
	cd backend && go run ./cmd/worker

web-install:
	cd web && npm install

web:
	cd web && npm run dev

web-build:
	cd web && npm run build

test:
	cd backend && go test ./...

test-integration:
	cd backend && go test -tags=integration ./...

lint:
	cd backend && go vet ./...

fmt:
	cd backend && gofmt -w .

tidy:
	cd backend && go mod tidy
