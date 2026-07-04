#!/usr/bin/env bash
# run.sh — start Agent Task Editor from prebuilt GHCR images (no local build).
#
# This is the counterpart to dev.sh: same env-var injection (repo mount, GitHub
# token, Claude auth, SSL bypass), but it pulls published images via
# docker-compose.release.yml instead of building from source.
#
#   ./run.sh                       # pull + start :latest
#   ATE_VERSION=v1.2.3 ./run.sh    # pin a specific release
#   ./run.sh --repo-dir ~/code start

# Load .env if present (without overriding existing shell vars)
if [[ -f "$(dirname "$0")/.env" ]]; then
  set -o allexport
  source "$(dirname "$0")/.env"
  set +o allexport
fi

# Image tag to run. Override with ATE_VERSION=v1.2.3 (or a bare "1.2.3").
export ATE_VERSION=${ATE_VERSION:-latest}

export LLM_BASE_URL=${LLM_BASE_URL:-"http://host.docker.internal:8081/v1"}

# Parse optional --repo-dir <path> before the command.
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir) REPO_BASE_DIR="$2"; shift 2 ;;
    *) break ;;
  esac
done

if [[ -z "$REPO_BASE_DIR" ]]; then
  REPO_BASE_DIR="/tmp/repos"
  echo "Warning: REPO_BASE_DIR not set — defaulting to $REPO_BASE_DIR (pass --repo-dir <path> or export REPO_BASE_DIR to override)"
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

# Compute SSL-bypass env vars here rather than in the compose file, because
# compose's ${VAR:+word} expansion fires on any non-empty string (including
# "false"), which would silently disable SSL when a user sets
# INSECURE_SKIP_SSL_VERIFY=false in their shell or .env file.
if [[ "${INSECURE_SKIP_SSL_VERIFY:-}" == "true" ]]; then
  export GIT_SSL_NO_VERIFY=true
  export NPM_CONFIG_STRICT_SSL=false
  export NODE_TLS_REJECT_UNAUTHORIZED=0
else
  export GIT_SSL_NO_VERIFY=
  export NPM_CONFIG_STRICT_SSL=
  export NODE_TLS_REJECT_UNAUTHORIZED=
fi

COMPOSE="docker compose -f docker-compose.release.yml"
if [[ -n "$TRAEFIK_HOST" ]]; then
  COMPOSE="$COMPOSE -f docker-compose.traefik.yml"
fi

# Extract GH token from gh CLI (keyring or hosts.yml) if not already set.
if [[ -z "$GH_TOKEN" ]] && command -v gh &>/dev/null; then
  GH_TOKEN=$(gh auth token 2>/dev/null) && export GH_TOKEN
fi

# On macOS, Claude Code stores OAuth credentials in the Keychain rather than a
# file, so the container can't read them. Sync to ~/.claude/.credentials.json
# (which is inside the already-mounted ~/.claude volume) before starting.
if [[ "$(uname)" == "Darwin" ]] && command -v security &>/dev/null; then
  if security find-generic-password -s "Claude Code-credentials" -w \
      > "$HOME/.claude/.credentials.json" 2>/dev/null; then
    echo "Claude credentials synced from macOS Keychain → ~/.claude/.credentials.json"
  fi
fi

CMD=${1:-start}

case "$CMD" in
  start)
    $COMPOSE up -d --pull always
    echo ""
    echo "  Running images: ghcr.io/myinisjap/agent-task-editor-{backend,frontend}:${ATE_VERSION}"
    echo "  Board:   http://localhost:5173"
    echo "  API:     http://localhost:8080"
    echo ""
    ;;
  pull)
    $COMPOSE pull
    ;;
  stop)
    $COMPOSE down
    ;;
  restart)
    $COMPOSE down
    $COMPOSE up -d --pull always
    ;;
  logs)
    $COMPOSE logs -f backend
    ;;
  login)
    # Authenticate Claude CLI inside the running backend container.
    $COMPOSE exec backend claude login
    ;;
  shell)
    $COMPOSE exec backend sh
    ;;
  *)
    echo "Usage: $0 [--repo-dir <path>] [start|pull|stop|restart|logs|login|shell]"
    exit 1
    ;;
esac
