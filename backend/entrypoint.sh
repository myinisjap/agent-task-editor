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
chown node:node /home/node /home/node/.gitconfig 2>/dev/null || true

exec su-exec node:node "$@"
