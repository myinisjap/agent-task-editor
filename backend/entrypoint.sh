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

# docker-compose defines the SSL/TLS override vars unconditionally, so when
# SSL_CA_CERT_PATH / INSECURE_SKIP_SSL_VERIFY are unset they arrive here as
# EMPTY STRINGS, not absent. Most tools shrug that off, but not all: an empty
# SSL_CERT_FILE makes mise (rustls) load zero CA certs and fail every HTTPS
# download, and git treats a set-but-empty GIT_SSL_NO_VERIFY as true. The
# compose comments say "active only when non-empty" — enforce that here, once,
# for the server and every subprocess it spawns.
for _v in SSL_CERT_FILE SSL_CERT_DIR GIT_SSL_CAINFO NODE_EXTRA_CA_CERTS \
          GIT_SSL_NO_VERIFY NPM_CONFIG_STRICT_SSL NODE_TLS_REJECT_UNAUTHORIZED; do
  if [ -z "$(printenv "$_v")" ]; then
    unset "$_v"
  fi
done

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

# chown_if_needed only recurses into a directory when its own ownership
# doesn't already match PUID/PGID. A plain `chown -R` on every start is fine
# for /app (small, code-sized) but /home/node/.cache and /home/node/.local
# are where uv's package cache and mise's toolchain installs accumulate —
# named volumes that can grow to millions of files after a few installs, and
# nothing under them ever needs a *different* owner than the top-level dir
# once it's already correct, so re-walking the whole tree on every container
# start (the common case: same PUID/PGID as last time) is pure waste. Stat
# the top-level dir's uid/gid instead and skip the recursive chown entirely
# when it already matches.
chown_if_needed() {
  for dir in "$@"; do
    [ -e "$dir" ] || continue
    owner=$(stat -c '%u:%g' "$dir" 2>/dev/null) || continue
    if [ "$owner" != "${PUID}:${PGID}" ]; then
      chown -R node:node "$dir" 2>/dev/null || true
    fi
  done
}

# Own the paths the server and agents write to. Bind-mounted repos and the
# host-provided auth dirs (.claude, .claude.json, .config/gh) are deliberately
# left alone — they're already owned by the host user that PUID should match.
# /home/node/.cache covers uv's cache (UV_CACHE_DIR=.cache/uv); /home/node/.local
# covers mise's data dir (MISE_DATA_DIR=.local/share/mise) — see docs/runtime.md.
chown_if_needed /data /app /home/node/go /home/node/.cache /home/node/.local
# QWEN_HOME dir is auto-created root-owned by the settings.json bind mount; qwen
# writes siblings (output-language.md, logs) there, so it must be node-writable.
chown node:node /home/node/qwen-home 2>/dev/null || true
chown node:node /home/node /home/node/.gitconfig 2>/dev/null || true

exec gosu node:node "$@"
