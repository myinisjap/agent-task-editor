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
# nothing to remap — just run. The server then has no way to switch uids, so it
# starts with agent-user isolation off and logs a warning.
if [ "$(id -u)" != "0" ]; then
  unset AGENT_UID AGENT_GID AGENT_HOME
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

# --- Agent user isolation (security finding H1) -----------------------------
# Provider CLIs (task runs, chat terminals) execute model-directed commands, so
# they must not run as the same user as the server: that user can read /data
# (the SQLite DB), /proc/<server pid>/environ (API_TOKEN, GITHUB_TOKEN, provider
# keys) and ~/.config/gh. We run them as a separate unprivileged 'agent' user.
# The 'agent' account's primary group is node's group (gid PGID), so repos and
# the shared mise/uv/go caches are shared via group permissions, while /data and
# the server's own files stay owner-only. See docs/getting-started.md.
#
# AGENT_ISOLATION=false opts out. AGENT_UID picks the agent's uid (default 10001,
# bumped if that equals PUID). The server (still non-root 'node') is started
# with CAP_SETUID/CAP_SETGID/CAP_KILL as ambient caps so it can fork children
# as 'agent' and signal them; the Go side strips those caps from every child.
umask 002   # server- and agent-created files stay group-writable (node + agent share a group)

ISOLATION=$(printf '%s' "${AGENT_ISOLATION:-true}" | tr 'A-Z' 'a-z')
AGENT_UID=${AGENT_UID:-}
if [ -z "$AGENT_UID" ]; then
  AGENT_UID=10001
  [ "$PUID" = "$AGENT_UID" ] && AGENT_UID=10002
fi

# group_rw makes a path group-readable/writable so the agent user (member of
# node's group) can use it. Recursion happens only when the top-level path is
# not already group-writable (cheap steady state), or always when mode=always.
group_rw() {
  mode=$1; shift
  for p in "$@"; do
    [ -e "$p" ] || continue
    if [ "$mode" != "always" ]; then
      perm=$(stat -c '%a' "$p" 2>/dev/null) || continue
      [ $(( 0$perm & 020 )) -ne 0 ] && continue
    fi
    chmod -R g+rwX "$p" 2>/dev/null || true
  done
}

if [ "$ISOLATION" = "false" ] || [ "$ISOLATION" = "0" ] || [ "$ISOLATION" = "no" ] || [ "$ISOLATION" = "off" ]; then
  unset AGENT_UID AGENT_GID AGENT_HOME
  echo "entrypoint: AGENT_ISOLATION=false - agents will run as the server user" >&2
elif [ "$AGENT_UID" = "$PUID" ]; then
  echo "entrypoint: WARNING AGENT_UID ($AGENT_UID) equals PUID; agent isolation disabled" >&2
  unset AGENT_UID AGENT_GID AGENT_HOME
elif ! id agent >/dev/null 2>&1; then
  echo "entrypoint: WARNING no 'agent' account in this image; agent isolation disabled" >&2
  unset AGENT_UID AGENT_GID AGENT_HOME
else
  usermod -o -u "$AGENT_UID" -g "$PGID" agent
  chown_agent=$(stat -c '%u' /home/agent 2>/dev/null || echo "")
  if [ "$chown_agent" != "$AGENT_UID" ]; then
    chown -R agent:node /home/agent 2>/dev/null || true
  fi
  # Auth dirs are mounted at /home/node/... (server reads them) and, in the
  # shipped compose files, a second time at /home/agent/... (agent HOME). If
  # the second mount is absent, link to the first so older compose files work.
  [ -e /home/agent/.claude ] || ln -s /home/node/.claude /home/agent/.claude 2>/dev/null || true
  [ -e /home/agent/.claude.json ] || ln -s /home/node/.claude.json /home/agent/.claude.json 2>/dev/null || true

  # Server-only secrets stay owner-only; the agent shares node's group.
  chmod 700 /data 2>/dev/null || true
  chmod 700 /home/node/.config/gh 2>/dev/null || true   # no-op on a :ro mount
  chmod g+x /home/node 2>/dev/null || true               # traverse to the shared caches
  # Shared toolchain caches, qwen home and the Claude auth dir/file must be
  # usable by the agent. (Host-owned; this adds the group-write bit.)
  group_rw auto /home/node/go /home/node/.cache /home/node/.local /home/node/qwen-home \
      /home/node/.claude /home/node/.claude.json
  chmod g+rwx /home/node/qwen-home 2>/dev/null || true
  # Bind-mounted repos are owned by the host user (PUID); give the group write
  # access so agent-created commits/edits work. AGENT_FIX_REPO_PERMS=never skips
  # this (you then manage repo permissions yourself), =always re-walks each start.
  FIX=${AGENT_FIX_REPO_PERMS:-auto}
  if [ "$FIX" != "never" ] && [ -n "${REPO_BASE_DIR:-}" ] && [ -d "$REPO_BASE_DIR" ]; then
    for repo in "$REPO_BASE_DIR"/*/; do
      group_rw "$FIX" "${repo%/}"
    done
  fi

  export AGENT_UID AGENT_GID="$PGID" AGENT_HOME=/home/agent
  # Prefer setpriv (ambient caps for the unprivileged server); probe first so a
  # restricted container (cap_drop, old util-linux) falls back instead of dying.
  CAPS=+setuid,+setgid,+kill
  if command -v setpriv >/dev/null 2>&1 && \
     setpriv --reuid=node --regid=node --init-groups --inh-caps=$CAPS --ambient-caps=$CAPS true 2>/dev/null; then
    exec setpriv --reuid=node --regid=node --init-groups --inh-caps=$CAPS --ambient-caps=$CAPS "$@"
  fi
  echo "entrypoint: WARNING cannot grant ambient capabilities (setpriv unavailable or caps dropped); agent isolation disabled" >&2
  unset AGENT_UID AGENT_GID AGENT_HOME
fi

exec gosu node:node "$@"
