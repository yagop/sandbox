#!/bin/sh
# Runs as root: make the /ca volume writable by the proxy user (this also fixes
# volumes that were first created root-owned), then drop privileges and exec the
# proxy as the unprivileged 'proxy' user.
set -e
mkdir -p /ca
chown proxy:proxy /ca 2>/dev/null || true
exec su-exec proxy:proxy /usr/local/bin/sandbox-proxy "$@"
