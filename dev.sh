#!/usr/bin/env bash
export LLM_API_KEY=${LLM_API_KEY:-"your_api_key_here"}
export LLM_BASE_URL=${LLM_BASE_URL:-"http://localhost:8081/v1"}
export LLM_MODEL=${LLM_MODEL:-"gemma-4-12B-it-qat-UD-Q4_K_XL"}

# Parse optional --repo-dir <path> before the command.
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir) REPO_BASE_DIR="$2"; shift 2 ;;
    *) break ;;
  esac
done

if [[ -z "$REPO_BASE_DIR" ]]; then
  echo "Error: REPO_BASE_DIR must be set (pass --repo-dir <path> or export REPO_BASE_DIR)"
  exit 1
fi

# Reject paths that would shadow critical system directories inside the container.
_UNSAFE_PREFIXES=("/" "/app" "/bin" "/boot" "/data" "/dev" "/etc" "/home" "/lib" "/lib64" "/proc" "/root" "/run" "/sbin" "/sys" "/tmp" "/usr" "/var")
for _prefix in "${_UNSAFE_PREFIXES[@]}"; do
  if [[ "$REPO_BASE_DIR" == "$_prefix" ]]; then
    echo "Error: REPO_BASE_DIR='$REPO_BASE_DIR' is a system path and cannot be used as a repo base"
    exit 1
  fi
done
unset _prefix _UNSAFE_PREFIXES

export REPO_BASE_DIR
export UID=${UID:-$(id -u)} GID=${GID:-$(id -g)}

CMD=${1:-start}

case "$CMD" in
  start)
    docker compose up -d --build
    echo ""
    echo "  Board:   http://localhost:5173"
    echo "  API:     http://localhost:8080"
    echo ""
    ;;
  stop)
    docker compose down
    ;;
  restart)
    docker compose down
    docker compose up -d --build
    ;;
  logs)
    docker compose logs -f backend
    ;;
  login)
    # Authenticate Claude CLI inside the running backend container.
    docker compose exec backend claude login
    ;;
  shell)
    docker compose exec backend sh
    ;;
  dev-stop)
    # Kill any orphaned dev processes by port.
    kill $(lsof -ti :8080 :5173 :5174 :5175 2>/dev/null) 2>/dev/null
    pkill -f 'agent-task-editor/backend/server' 2>/dev/null
    echo "dev processes stopped"
    ;;
  dev)
    # Start backend and frontend as local processes (no Docker).
    # Requires: Go, Node.js/npm installed locally.
    SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
    trap 'kill 0' INT TERM EXIT

    echo "Building MCP server..."
    (cd "$SCRIPT_DIR/backend" && go build -o mcp-server ./cmd/mcp-server)
    MCP_SERVER_PATH="$SCRIPT_DIR/backend/mcp-server"

    echo "Building backend..."
    (cd "$SCRIPT_DIR/backend" && go build -o server ./cmd/server)

    echo "Starting backend on :8080..."
    (cd "$SCRIPT_DIR/backend" && MCP_SERVER_PATH="$MCP_SERVER_PATH" LOG_LEVEL=DEBUG ./server) &
    BACKEND_PID=$!

    echo "Starting frontend on :5173..."
    (cd "$SCRIPT_DIR/frontend" && npm install --silent && VITE_API_BASE_URL=http://localhost:8080 VITE_WS_BASE_URL=ws://localhost:8080 npm run dev) &
    FRONTEND_PID=$!

    echo ""
    echo "  Board:   http://localhost:5173"
    echo "  API:     http://localhost:8080"
    echo ""
    echo "Press Ctrl+C to stop both."
    wait $BACKEND_PID $FRONTEND_PID
    ;;
  *)
    echo "Usage: $0 [--repo-dir <path>] [start|stop|restart|logs|login|shell|dev]"
    exit 1
    ;;
esac
