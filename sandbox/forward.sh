#!/bin/sh
# forward <ip> <port> [port...] — reach a raw-TCP LAN service through the proxy.
# Each port is exposed on 127.0.0.1:<port> and tunnelled (HTTP CONNECT ->
# blind-tunnel) to <ip>:<port>. Runs in the foreground; Ctrl-C tears it down,
# or append `&` to background it. Point your client at 127.0.0.1:<port>.
set -e
[ "$#" -ge 2 ] || { echo "usage: forward <ip> <port> [port...]" >&2; exit 1; }

ip=$1; shift
p=${HTTPS_PROXY#*://}; p=${p%/}          # e.g. sandbox-proxy:3128
phost=${p%:*}; pport=${p##*:}
[ -n "$phost" ] && [ -n "$pport" ] || { echo "forward: no HTTPS_PROXY set" >&2; exit 1; }

for port in "$@"; do
  socat "TCP-LISTEN:$port,reuseaddr,fork" \
        "PROXY:$phost:$ip:$port,proxyport=$pport" &
  echo "forward: 127.0.0.1:$port -> $ip:$port (via $phost:$pport)"
done
wait
