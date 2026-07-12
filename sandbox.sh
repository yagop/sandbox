# sandbox.sh — source this to get the `sandbox` command.
#
#   source sandbox.sh
#   sandbox proxy up        # build (if needed) + start the one shared proxy
#   sandbox proxy status    # is it running? on which networks?
#   sandbox proxy reload     # restart the proxy picking up current env (tokens)
#   sandbox proxy down       # stop + remove the proxy
#   sandbox proxy logs       # follow proxy logs
#   sandbox run [cmd...]     # ensure proxy is up, run a sandbox in $PWD
#   sandbox build [proxy|box|all]   # force a rebuild
#   sandbox ps               # list running sandbox containers
#
# One proxy is shared by every sandbox. Sandboxes sit on an internal Docker
# network; HTTP(S) goes through the proxy (which injects credentials), and with
# SANDBOX_OPEN_NET=1 (default) other traffic egresses directly. Set
# SANDBOX_OPEN_NET=0 to make the proxy the only route out. Tokens
# (GH_TOKEN, NPM_TOKEN, ...) live ONLY in the proxy container's environment.

# --- resolve where this script lives (bash or zsh), overridable ---------------
if [ -z "${SANDBOX_DIR:-}" ]; then
  if [ -n "${BASH_SOURCE:-}" ]; then
    _sbx_self="${BASH_SOURCE[0]}"
  else
    eval '_sbx_self=${(%):-%x}' 2>/dev/null || _sbx_self="$0"
  fi
  SANDBOX_DIR="$(cd "$(dirname "$_sbx_self")" && pwd)"
  unset _sbx_self
fi

# --- config (override via env before sourcing) --------------------------------
: "${SANDBOX_NET:=sandbox-net}"          # internal network: sandboxes + proxy
: "${SANDBOX_EGRESS:=sandbox-egress}"    # egress network: proxy only
: "${SANDBOX_CA_VOL:=sandbox-ca}"        # proxy-only volume holding ca.crt + ca.key
# Sandboxes get ONLY the public ca.crt (bind-mounted read-only), never the CA
# volume — so a workload can't read ca.key and mint trusted certs.
: "${SANDBOX_CACERT_FILE:=${XDG_CACHE_HOME:-$HOME/.cache}/sandbox-proxy/ca.crt}"
: "${SANDBOX_PROXY_NAME:=sandbox-proxy}"
: "${SANDBOX_PROXY_IMAGE:=sandbox-proxy:latest}"
: "${SANDBOX_BOX_IMAGE:=sandbox-box:latest}"
# Which env vars to forward into the proxy (the secrets your rules reference):
: "${SANDBOX_SECRET_ENVS:=GH_TOKEN NPM_TOKEN SCW_SECRET_KEY SCW_COCKPIT_TOKEN_FR_PAR SCW_COCKPIT_TOKEN_NL_AMS SCW_COCKPIT_TOKEN_PL_WAW FLY_API_TOKEN}"
# For any secret VAR that isn't set in the environment, SANDBOX_<VAR>_CMD (if
# defined) is run on the host to obtain it. Defaults: get GH_TOKEN from gh and
# FLY_API_TOKEN from flyctl.
: "${SANDBOX_GH_TOKEN_CMD:=gh auth token}"
: "${SANDBOX_FLY_API_TOKEN_CMD:=fly auth token}"

# Open network: when 1 (default), sandboxes are ALSO attached to the egress
# network, so traffic that doesn't go through the proxy (raw TCP, ssh, db
# connections, ...) reaches the internet directly. HTTP(S) still goes through
# the proxy via HTTP(S)_PROXY for header/credential injection. Set to 0 for
# strict isolation where the proxy is the only way out.
: "${SANDBOX_OPEN_NET:=1}"

# Extra volumes mounted into every `sandbox run`, whitespace-separated docker -v
# specs. Persist tool configs across sandboxes here, e.g.:
#   export SANDBOX_VOLUMES="claude-config:/root/.claude codex-config:/root/.codex pi-config:/root/.pi"
: "${SANDBOX_VOLUMES:=}"

# Extra env vars set in every sandbox container, whitespace-separated docker -e
# specs (NAME=value, or bare NAME to forward from the host). Placeholder tokens
# (GH_TOKEN, FLY_API_TOKEN) are already baked into the image as ENV. Never put
# real secrets here — those belong in the proxy.
: "${SANDBOX_ENVS:=}"

# --- internals ----------------------------------------------------------------
_sbx_have_docker() {
  command -v docker >/dev/null 2>&1 || { echo "sandbox: docker not found in PATH" >&2; return 1; }
}

_sbx_proxy_cid() {  # prints container id if the proxy is running, else nothing
  docker ps --filter "name=^/${SANDBOX_PROXY_NAME}$" --filter status=running -q 2>/dev/null
}

_sbx_ensure_networks() {
  docker network inspect "$SANDBOX_NET" >/dev/null 2>&1 || \
    docker network create --internal "$SANDBOX_NET" >/dev/null
  docker network inspect "$SANDBOX_EGRESS" >/dev/null 2>&1 || \
    docker network create "$SANDBOX_EGRESS" >/dev/null
}

_sbx_ensure_image() {  # build only if missing. $1 = proxy|box
  case "$1" in
    proxy) docker image inspect "$SANDBOX_PROXY_IMAGE" >/dev/null 2>&1 || _sbx_build proxy ;;
    box)   docker image inspect "$SANDBOX_BOX_IMAGE"   >/dev/null 2>&1 || _sbx_build box ;;
  esac
}

_sbx_build() {  # force build. $1 = proxy|box|all (default all)
  local what="${1:-all}"
  case "$what" in
    proxy|all) echo "sandbox: building $SANDBOX_PROXY_IMAGE" >&2
               docker build -t "$SANDBOX_PROXY_IMAGE" "$SANDBOX_DIR/proxy" || return 1 ;;
  esac
  case "$what" in
    box|all)   echo "sandbox: building $SANDBOX_BOX_IMAGE" >&2
               docker build -t "$SANDBOX_BOX_IMAGE" "$SANDBOX_DIR/sandbox" || return 1 ;;
  esac
}

# Resolve any unset secret from its SANDBOX_<VAR>_CMD and export it into the
# CURRENT shell. Call this only inside a subshell so tokens don't leak into the
# user's interactive shell. Only the command is logged, never the value.
# zsh does not word-split unquoted parameters like sh/bash. `setopt shwordsplit`
# must run in the SAME scope as the loop (localoptions restores it on return), so
# it is inlined below rather than factored into a helper. The guard makes it a
# no-op in bash (which never runs setopt).
_sbx_load_secrets() {
  [ -n "${ZSH_VERSION:-}" ] && setopt localoptions shwordsplit
  local v cmdvar cmd val
  for v in $SANDBOX_SECRET_ENVS; do
    eval "val=\${$v:-}"
    [ -n "$val" ] && continue
    cmdvar="SANDBOX_${v}_CMD"
    eval "cmd=\${$cmdvar:-}"
    [ -z "$cmd" ] && continue
    val="$(eval "$cmd" 2>/dev/null)" || val=""
    if [ -n "$val" ]; then
      export "$v=$val"
      echo "sandbox: resolved \$$v via: $cmd" >&2
    fi
  done
}

_sbx_proxy_up() {
  _sbx_have_docker || return 1
  if [ -n "$(_sbx_proxy_cid)" ]; then
    echo "sandbox: proxy already running"
    return 0
  fi
  _sbx_ensure_image proxy || return 1
  _sbx_ensure_networks
  docker rm -f "$SANDBOX_PROXY_NAME" >/dev/null 2>&1 || true

  local cfg="$SANDBOX_DIR/proxy/config.json"

  # Resolve secrets in a subshell so tokens never persist in the user's shell,
  # then run docker with an argv array (portable across bash/zsh — no reliance
  # on word-splitting) so paths and -e flags stay intact. Secrets are forwarded
  # by name (-e VAR) so values never appear on the command line.
  (
    _sbx_load_secrets
    [ -n "${ZSH_VERSION:-}" ] && setopt shwordsplit
    local v val
    local -a args
    args=(run -d --name "$SANDBOX_PROXY_NAME"
          --network "$SANDBOX_NET" --restart unless-stopped)
    for v in $SANDBOX_SECRET_ENVS; do
      eval "val=\${$v:-}"
      if [ -n "$val" ]; then
        args+=(-e "$v")
      else
        echo "sandbox: warning: \$$v is not set; rules using it won't inject" >&2
      fi
    done
    [ -f "$cfg" ] && args+=(-v "$cfg:/etc/sandbox-proxy/config.json:ro")
    args+=(-v "$SANDBOX_CA_VOL:/ca" "$SANDBOX_PROXY_IMAGE")
    docker "${args[@]}" >/dev/null
  ) || return 1
  # give the proxy egress via a second, non-internal network
  docker network connect "$SANDBOX_EGRESS" "$SANDBOX_PROXY_NAME" >/dev/null 2>&1 || true

  # Wait until the proxy has generated its CA into the shared volume, so
  # sandboxes started right after don't race ahead of it (max ~5s).
  local i=0
  while [ "$i" -lt 50 ]; do
    docker exec "$SANDBOX_PROXY_NAME" test -f /ca/ca.crt >/dev/null 2>&1 && break
    i=$((i + 1)); sleep 0.1
  done
  if ! docker exec "$SANDBOX_PROXY_NAME" test -f /ca/ca.crt >/dev/null 2>&1; then
    echo "sandbox: warning: proxy CA not ready — check 'sandbox proxy logs'" >&2
  fi
  _sbx_export_cacert
  echo "sandbox: proxy up ($SANDBOX_PROXY_NAME)"
}

# Copy ONLY the public ca.crt out of the proxy container to a host file, so
# sandboxes can trust it without ever mounting the CA volume (which holds ca.key).
_sbx_export_cacert() {
  mkdir -p "$(dirname "$SANDBOX_CACERT_FILE")" 2>/dev/null || true
  docker cp -q "$SANDBOX_PROXY_NAME:/ca/ca.crt" "$SANDBOX_CACERT_FILE" 2>/dev/null \
    || echo "sandbox: warning: could not export ca.crt to $SANDBOX_CACERT_FILE" >&2
}

_sbx_proxy_down() {
  _sbx_have_docker || return 1
  docker rm -f "$SANDBOX_PROXY_NAME" >/dev/null 2>&1 && echo "sandbox: proxy down" || echo "sandbox: proxy not running"
}

_sbx_proxy_reload() {
  _sbx_have_docker || return 1
  echo "sandbox: reloading proxy with current environment..."
  docker rm -f "$SANDBOX_PROXY_NAME" >/dev/null 2>&1 || true
  _sbx_proxy_up
}

_sbx_proxy_status() {
  _sbx_have_docker || return 1
  if [ -z "$(_sbx_proxy_cid)" ]; then
    echo "proxy: STOPPED"
    return 0
  fi
  echo "proxy: RUNNING ($SANDBOX_PROXY_NAME)"
  docker ps --filter "name=^/${SANDBOX_PROXY_NAME}$" \
    --format '  image={{.Image}}  status={{.Status}}' 2>/dev/null
  local nets
  nets="$(docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$SANDBOX_PROXY_NAME" 2>/dev/null)"
  echo "  networks: $nets"
  echo "  config:   $SANDBOX_DIR/proxy/config.json"
  echo "  (edit config or export new tokens, then: sandbox proxy reload)"
}

_sbx_proxy() {
  case "${1:-status}" in
    up)     _sbx_proxy_up ;;
    down|stop) _sbx_proxy_down ;;
    reload|restart) _sbx_proxy_reload ;;
    status) _sbx_proxy_status ;;
    logs)   _sbx_have_docker && docker logs -f "$SANDBOX_PROXY_NAME" ;;
    *) echo "sandbox proxy: unknown subcommand '$1' (up|down|reload|status|logs)" >&2; return 1 ;;
  esac
}

_sbx_run() {
  _sbx_have_docker || return 1
  if [ -z "$(_sbx_proxy_cid)" ]; then
    echo "sandbox: proxy not running — starting it..." >&2
    _sbx_proxy_up || return 1
  fi
  _sbx_ensure_image box || return 1
  # Ensure the exported ca.crt exists (e.g. proxy started in a prior shell).
  [ -f "$SANDBOX_CACERT_FILE" ] || _sbx_export_cacert
  # Unique name per sandbox so many can run at once against the one proxy.
  local base tag wd
  base="$(basename "$PWD")"
  tag="${RANDOM:-$$}"
  wd="/workspace/$base"   # ~/x  ->  /workspace/x

  # Build argv as an array (portable across bash/zsh; no word-split surprises).
  # Mount ONLY ca.crt (read-only) — never the CA volume, so ca.key stays in the proxy.
  # create+start (not run) so the egress network can be attached before the
  # workload boots when SANDBOX_OPEN_NET=1 — docker run takes only one --network.
  local -a args
  args=(create --rm -it
        --name "sandbox-$base-$tag"
        --label sandbox.role=box
        --network "$SANDBOX_NET"
        -e "TERM=${TERM:-xterm-256color}" -e COLORTERM
        -v "$SANDBOX_CACERT_FILE:/ca/ca.crt:ro"
        -v "$PWD:$wd" -w "$wd")

  # Extra persistent volumes (tool configs, caches, ...) and env vars. Each
  # entry is a raw docker -v / -e spec (see SANDBOX_VOLUMES / SANDBOX_ENVS).
  if [ -n "${SANDBOX_VOLUMES:-}${SANDBOX_ENVS:-}" ]; then
    [ -n "${ZSH_VERSION:-}" ] && setopt localoptions shwordsplit
    local spec
    for spec in $SANDBOX_VOLUMES; do
      args+=(-v "$spec")
    done
    for spec in $SANDBOX_ENVS; do
      args+=(-e "$spec")
    done
  fi

  args+=("$SANDBOX_BOX_IMAGE" "$@")
  local cid
  cid="$(docker "${args[@]}")" || return 1
  if [ "${SANDBOX_OPEN_NET}" = "1" ]; then
    docker network connect "$SANDBOX_EGRESS" "$cid" >/dev/null 2>&1 \
      || echo "sandbox: warning: could not attach $SANDBOX_EGRESS (open net disabled for this run)" >&2
  fi
  docker start -ai "$cid"
}

_sbx_ps() {
  _sbx_have_docker || return 1
  docker ps --filter "label=sandbox.role=box" \
    --format 'table {{.Names}}\t{{.Status}}\t{{.Command}}' 2>/dev/null
}

_sbx_help() {
  cat <<'EOF'
sandbox — run code in an isolated container that reaches the network only
          through a credential-injecting proxy.

  sandbox proxy up           build (if needed) + start the shared proxy
  sandbox proxy status       show proxy state and networks
  sandbox proxy reload       restart the proxy, picking up current env/tokens
  sandbox proxy down         stop + remove the proxy
  sandbox proxy logs         follow the proxy log
  sandbox run [cmd...]       ensure proxy is up, run a sandbox in $PWD
  sandbox build [proxy|box|all]   force a rebuild of images
  sandbox ps                 list running sandboxes

Secrets are forwarded into the proxy from these env vars: $SANDBOX_SECRET_ENVS

SANDBOX_OPEN_NET=1 (default) also gives sandboxes direct egress for traffic
that doesn't use the proxy (raw TCP, ssh, databases). Set SANDBOX_OPEN_NET=0
to make the proxy the only network path.
EOF
}

# --- dispatcher ---------------------------------------------------------------
sandbox() {
  # Self-reload: re-source this file on every call so edits take effect without
  # having to remember to `source sandbox.sh`. Sourcing only (re)defines
  # functions and defaults — it calls nothing, so no recursion. Note the
  # `: "${VAR:=...}"` defaults keep values already set in this shell, so an
  # edited *default* still needs a new shell (or an export) to apply.
  [ -f "$SANDBOX_DIR/sandbox.sh" ] && . "$SANDBOX_DIR/sandbox.sh"
  local cmd="${1:-help}"
  [ $# -gt 0 ] && shift
  case "$cmd" in
    proxy) _sbx_proxy "$@" ;;
    run)   _sbx_run "$@" ;;
    build) _sbx_build "$@" ;;
    ps)    _sbx_ps ;;
    ""|help|-h|--help) _sbx_help ;;
    *) echo "sandbox: unknown command '$cmd'" >&2; _sbx_help; return 1 ;;
  esac
}
