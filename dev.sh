#!/usr/bin/env bash
set -euo pipefail

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
  dev)
    # Start backend and frontend as local processes (no Docker).
    # Requires: Go, Node.js/npm installed locally.
    SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
    trap 'kill 0' INT TERM EXIT

    echo "Starting backend on :8080..."
    (cd "$SCRIPT_DIR/backend" && go run ./cmd/server) &
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
    echo "Usage: $0 [start|stop|restart|logs|login|shell|dev]"
    exit 1
    ;;
esac
