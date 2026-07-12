#!/bin/sh
# Install the proxy CA into the system trust store, then hand off to the
# workload. Runs at container start so a freshly generated CA is picked up.
set -e

if [ -f /ca/ca.crt ]; then
  cp /ca/ca.crt /usr/local/share/ca-certificates/sandbox-proxy.crt
  update-ca-certificates >/dev/null 2>&1 || true
else
  echo "sandbox-entrypoint: WARNING /ca/ca.crt not found; HTTPS will fail" >&2
fi

# Route git-over-HTTPS through the proxy. git sends the request unauthenticated;
# the proxy injects the real credential on the way out (git never holds it).
git config --global http.proxy "$HTTPS_PROXY" 2>/dev/null || true

# Go reads HTTP(S)_PROXY from the environment and the system trust store (which
# now includes the proxy CA), so `go get` / module downloads work transparently.
# Allow-list proxy.golang.org and sum.golang.org (or set allow_all) for modules.

# npm reads the proxy from env automatically; pin the registry to https.
npm config set //registry.npmjs.org/:_authToken=dummy >/dev/null 2>&1 || true

# Claude Code keeps ~/.claude.json OUTSIDE the persisted ~/.claude directory, so
# it is otherwise lost across --rm sandboxes. When ~/.claude is a mounted volume,
# relocate the file into it (once) and symlink, so app state (MCP servers, trust
# decisions, UI prefs) persists. Auth already persists via ~/.claude/.credentials.json.
# Caveat: if a tool rewrites the file via temp-then-rename it may replace the
# symlink; if you see it not persisting, mount the home dir instead.
if [ -d /root/.claude ]; then
  if [ ! -e /root/.claude/claude.json ] && [ -f /root/.claude.json ] && [ ! -L /root/.claude.json ]; then
    mv /root/.claude.json /root/.claude/claude.json
  fi
  ln -sfn /root/.claude/claude.json /root/.claude.json
fi

exec "$@"
