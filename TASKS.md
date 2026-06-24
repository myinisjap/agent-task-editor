# Tasks

## dev
Starts the backend with hot reload and the Vite frontend dev server in parallel.

Requires:
- gen

```sh
cd backend && air -c .air.toml &
cd frontend && npm run dev
```

## backend
Starts only the backend with hot reload.

Requires:
- gen

```sh
cd backend && air -c .air.toml
```

## frontend
Starts only the Vite dev server.

```sh
cd frontend && npm run dev
```

## gen
Runs sqlc codegen to regenerate Go types from SQL queries.

```sh
cd backend && sqlc generate
```

## migrate
Runs all pending database migrations (migrations run automatically on server start, this is for manual inspection).

```sh
cd backend && go run ./cmd/server -migrate-only 2>/dev/null || echo "Run the server to apply migrations automatically"
```

## build
Builds the backend binary to backend/bin/server.

Requires:
- gen

```sh
cd backend && go build -o bin/server ./cmd/server && echo "Built: backend/bin/server"
```

## build-mcp
Builds the MCP sidecar binary to backend/bin/mcp-server.

```sh
cd backend && go build -o bin/mcp-server ./cmd/mcp-server && echo "Built: backend/bin/mcp-server"
```

## test
Runs all backend tests.

```sh
cd backend && go test ./...
```

## tidy
Runs go mod tidy and npm install to sync dependencies.

```sh
cd backend && go mod tidy
cd frontend && npm install
```

## lint
Runs go vet on the backend.

```sh
cd backend && go vet ./...
```
