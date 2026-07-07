#!/usr/bin/env bash
# Seeds a throwaway demo repo + tasks against a running local instance
# (./dev.sh dev), for capturing README/docs screenshots and GIFs.
#
# Usage:
#   DB_PATH=demo-screenshots.db ./dev.sh dev   # in one terminal, fresh empty DB
#   ./scripts/seed-demo.sh                     # in another, once it's up
#
# Using a dedicated DB_PATH keeps demo data isolated from your real task
# history — the Dashboard aggregates cost/run stats across ALL repos in
# the DB, so screenshots taken against your normal dev DB would leak real
# usage figures.
#
# Re-run any time the board UI changes and screenshots need retaking —
# it's idempotent-ish: re-running creates a new demo repo registration
# each time, so clean up old "demo-repo" entries via the UI if you re-run
# against the same DB file.
set -euo pipefail

API="${API_BASE:-http://localhost:8080/api/v1}"
# Must live under REPO_BASE_DIR (defaults to ~/code_projects) — repos
# outside it are rejected by the backend.
DEMO_REPO_DIR="${DEMO_REPO_DIR:-$HOME/code_projects/demo-repo}"

echo "Creating demo git repo at $DEMO_REPO_DIR..."
rm -rf "$DEMO_REPO_DIR"
mkdir -p "$DEMO_REPO_DIR/src"
cat > "$DEMO_REPO_DIR/README.md" <<'EOF'
# Demo Repo

A tiny sample project used for Agent Task Editor screenshots.
EOF
cat > "$DEMO_REPO_DIR/src/app.py" <<'EOF'
def greet(name):
    return f"Hello, {name}!"

if __name__ == "__main__":
    print(greet("world"))
EOF
git -C "$DEMO_REPO_DIR" init -q
git -C "$DEMO_REPO_DIR" -c user.name="Demo" -c user.email="demo@example.com" add -A
git -C "$DEMO_REPO_DIR" -c user.name="Demo" -c user.email="demo@example.com" commit -q -m "Initial commit"
git -C "$DEMO_REPO_DIR" branch -M main

echo "Registering repo..."
REPO_ID=$(curl -s -X POST "$API/repos" -H "Content-Type: application/json" \
  -d "{\"name\":\"demo-repo\",\"path\":\"$DEMO_REPO_DIR\"}" | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')

echo "Finding default workflow..."
WF_ID=$(curl -s "$API/workflows" | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["id"])')

curl -s -X PATCH "$API/repos/$REPO_ID" -H "Content-Type: application/json" \
  -d "{\"workflow_id\":\"$WF_ID\"}" > /dev/null

create_task() {
  curl -s -X POST "$API/tasks" -H "Content-Type: application/json" \
    -d "{\"repo_id\":\"$REPO_ID\",\"workflow_id\":\"$WF_ID\",\"title\":\"$1\",\"description\":\"$2\",\"type\":\"$3\"}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])'
}

move() {
  curl -s -X PATCH "$API/tasks/$1/label" -H "Content-Type: application/json" \
    -d "{\"to_label\":\"$2\"}" > /dev/null
}

echo "Creating demo tasks..."
T1=$(create_task "Add rate limiting to the public API" "Add a token-bucket rate limiter middleware in front of all public REST endpoints to prevent abuse from unauthenticated clients." "feature")
T2=$(create_task "Fix flaky retry test in worker pool" "TestWorkerPool_RetryBackoff intermittently fails under -race; suspect a data race on the shared attempt counter." "bug")
T3=$(create_task "Add CSV export for dashboard metrics" "Let users export the run counts and completion-rate chart data as CSV from the dashboard toolbar." "feature")
T4=$(create_task "Migrate logging to structured slog" "Replace ad-hoc fmt.Printf debug logging with structured slog calls across the worker and dispatcher packages." "chore")
T5=$(create_task "Support dark mode toggle in board settings" "Add a persisted light/dark theme toggle to the board's settings menu." "feature")
T6=$(create_task "Improve empty-state copy on Dashboard" "The empty dashboard state before any runs exist reads as an error; soften the copy and add a getting-started link." "chore")

echo "Spreading tasks across workflow labels..."
# Each task walks the chain one hop at a time — the API only allows
# transitions that exist as edges in the workflow graph.
move "$T1" plan

move "$T2" plan
move "$T2" review-plan
move "$T2" work

move "$T3" plan
move "$T3" review-plan
move "$T3" work
move "$T3" testing

move "$T4" plan
move "$T4" review-plan
move "$T4" work
move "$T4" testing
move "$T4" agent-review
move "$T4" review

move "$T5" plan
move "$T5" review-plan
move "$T5" work
move "$T5" testing
move "$T5" agent-review
move "$T5" review
move "$T5" done

# T6 stays in not_ready.

echo "Done. Demo repo: $DEMO_REPO_DIR (repo_id=$REPO_ID)"
echo "Board: not_ready(1) plan(1) work(1) testing(1) review(1) done(1)"
