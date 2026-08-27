#!/bin/sh
# Runtime UID/GID remap so bind-mounted repos stay writable regardless of the
# host user — the self-hosted image convention (PUID/PGID, à la LinuxServer.io).
#
# The image bakes a 'node' user at 1000:1000. At startup we (as root) re-point
# that account to the requested PUID/PGID, chown the paths the server and agents
# write to, then drop privileges and exec the server as node. This replaces the
# old build-time HOST_UID/HOST_GID remap, which couldn't work for a published
# image (its UID is fixed at build time).
#
# Set PUID/PGID to your host user's `id -u` / `id -g`. dev.sh and run.sh do this
# for you. Defaults to 1000:1000, which matches most single-user Linux desktops.
set -e

PUID=${PUID:-1000}
PGID=${PGID:-1000}

# If we're not root, someone pinned the runtime user (e.g. compose `user:`);
# nothing to remap — just run.
if [ "$(id -u)" != "0" ]; then
  exec "$@"
fi

cur_uid=$(id -u node)
cur_gid=$(id -g node)

# -o allows a non-unique id, so PUID/PGID that collide with an existing account
# (common on multi-service hosts) don't abort startup.
if [ "$PGID" != "$cur_gid" ]; then
  groupmod -o -g "$PGID" node
fi
if [ "$PUID" != "$cur_uid" ]; then
  usermod -o -u "$PUID" node
fi

# Own the paths the server and agents write to. Bind-mounted repos and the
# host-provided auth dirs (.claude, .claude.json, .config/gh) are deliberately
# left alone — they're already owned by the host user that PUID should match.
chown -R node:node /data /app /home/node/go /home/node/.cache 2>/dev/null || true
# QWEN_HOME dir is auto-created root-owned by the settings.json bind mount; qwen
# writes siblings (output-language.md, logs) there, so it must be node-writable.
chown node:node /home/node/qwen-home 2>/dev/null || true
# ATE_RUNTIME_DIR is bind-mounted from the host; Docker creates a missing
# source root-owned, so the server (running as node) can't stage the
# mcp-server sidecar into it or write the MCP handoff files there. Same
# situation as qwen-home above.
if [ -n "${ATE_RUNTIME_DIR:-}" ]; then
  mkdir -p "$ATE_RUNTIME_DIR" 2>/dev/null || true
  chown node:node "$ATE_RUNTIME_DIR" 2>/dev/null || true
fi
chown node:node /home/node /home/node/.gitconfig 2>/dev/null || true

# If the Docker socket is bind-mounted (per-repo runtime containers, see
# docs/runtime-containers.md), join 'node' to whatever group owns it on the
# host. The socket's gid varies per host/distro (Docker Desktop, different
# distros' package managers all pick different values) so it can't be baked
# in at build time — read it from the mounted socket itself instead. `docker`
# is a fixed group name inside this image; -o allows reusing that name even
# if its gid collides with one already present (e.g. a host gid that matches
# an existing image group).
if [ -S /var/run/docker.sock ]; then
  sock_gid=$(stat -c '%g' /var/run/docker.sock)
  if ! getent group "$sock_gid" >/dev/null 2>&1; then
    groupadd -o -g "$sock_gid" docker
  fi
  sock_group=$(getent group "$sock_gid" | cut -d: -f1)
  usermod -aG "$sock_group" node
fi

exec su-exec node:node "$@"
