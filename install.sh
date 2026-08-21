#!/usr/bin/env bash
# install.sh — fetch just what's needed to run Agent Task Editor from
# prebuilt GHCR images, without cloning the full source repo.
#
#   curl -fsSL https://raw.githubusercontent.com/myinisjap/agent-task-editor/main/install.sh | bash
#
# Downloads run.sh + docker-compose.release.yml + docker-compose.traefik.yml
# into ./agent-task-editor, then runs `./run.sh`. Anyone who wants the source
# (to build locally or contribute) should still `git clone` — this is only
# for the run-from-images path.
set -euo pipefail

REPO=${ATE_REPO:-myinisjap/agent-task-editor}
REF=${ATE_REF:-main}
RAW="https://raw.githubusercontent.com/${REPO}/${REF}"
DIR=${1:-agent-task-editor}

mkdir -p "$DIR"
cd "$DIR"

for f in run.sh docker-compose.release.yml docker-compose.traefik.yml; do
  curl -fsSL "$RAW/$f" -o "$f"
done
chmod +x run.sh

echo "Downloaded to $(pwd)"
echo "Starting: ./run.sh"
exec ./run.sh
