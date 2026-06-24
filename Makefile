.PHONY: start stop restart logs login build dev test

# Start the full stack (builds images if needed)
start:
	docker compose up -d --build
	@echo ""
	@echo "  Board:   http://localhost:5173"
	@echo "  API:     http://localhost:8080"
	@echo ""

# Stop all services
stop:
	docker compose down

# Restart and rebuild
restart:
	docker compose down
	docker compose up -d --build

# Follow backend logs (Ctrl-C to exit)
logs:
	docker compose logs -f backend

# Authenticate Claude CLI inside the running container.
# Run this once if 'claude login' hasn't been done on the host yet.
login:
	docker compose exec backend claude login

# Open a shell in the backend container
shell:
	docker compose exec backend sh

# Local dev — backend on :8080, frontend on :5173 (no Docker)
dev:
	@echo "Starting backend..."
	cd backend && go run ./cmd/server &
	@echo "Starting frontend..."
	cd frontend && npm run dev

# Run backend tests
test:
	cd backend && go test ./...

# Rebuild sqlc generated code after editing .sql files
generate:
	cd backend && go generate ./...
