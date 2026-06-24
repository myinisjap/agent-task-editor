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
  *)
    echo "Usage: $0 [start|stop|restart|logs|login|shell]"
    exit 1
    ;;
esac
