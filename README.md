# sandbox-proxy

A zero-dependency, **stdlib-only** Go forward proxy that injects your real
credentials (GitHub `GH_TOKEN`, npm token, …) into outbound requests **on the
wire**, so code running in a sandbox can use them without ever *seeing* them.

A simplified version of [Infisical's agent-vault](https://github.com/Infisical/agent-vault),
driven by a single script (`sandbox.sh`) over a dependency-free proxy binary you own.

## What it does

- **Default-deny allow list** — a request is blocked unless a rule matches its
  `host`.
- **Credential injection** — for matched rules, adds `Authorization: Bearer …`
  or HTTP Basic auth pulled from the *proxy's* environment. The sandbox sends a
  request with no token; the token is added as it leaves.
- **HTTPS interception** — terminates TLS using a CA it generates on first run,
  so it can rewrite HTTPS requests. The sandbox trusts that CA.
- **Proxy chaining** — honors `HTTP(S)_PROXY` for its own egress, so it works
  behind corporate proxies or nested sandboxes.

Because the token lives only in the proxy, a fully compromised workload can at
most *use* it against the exact hosts/paths you allow — it can't read or
exfiltrate the secret itself.

```
sandbox  (git / gh / npm — no token; HTTPS_PROXY, trusts proxy CA)
   │
   │  request without credentials
   ▼
sandbox-proxy  (holds the real token; default-deny allow-list)
   │
   │  + Authorization injected on the wire
   ▼
upstream  (github.com, api.github.com, registry.npmjs.org, …)
```

The sandbox sits on an internal network with no direct egress, so the proxy is
the only way out — and the real token never enters the sandbox.

## Files

```
.
├── sandbox.sh              # source this -> the `sandbox` command
├── proxy/                  # the credential-injecting proxy
│   ├── config.json         #   rules + secret definitions
│   ├── entrypoint.sh       #   chowns /ca, drops root -> proxy user
│   ├── Dockerfile          #   builds sandbox-proxy:latest
│   └── src/                #   Go module (stdlib only)
│       ├── main.go         #     startup
│       ├── proxy.go        #     HTTP/CONNECT handling, MITM serving, forwarding
│       ├── serve.go        #     one-shot listener, header/stream helpers
│       ├── websocket.go    #     WebSocket upgrade forwarding (MITM hosts)
│       ├── tunnel.go       #     allow_all blind (non-intercepting) tunnel
│       ├── policy.go       #     allow-list matching + injection
│       ├── config.go       #     config types + loading
│       ├── ca.go           #     CA + on-the-fly leaf certs
│       └── helpers.go      #     small utilities
└── sandbox/                # the workload container
    ├── entrypoint.sh       #   installs the CA, points git at the proxy
    └── Dockerfile          #   builds sandbox-box:latest
```

| File | Purpose |
|------|---------|
| `sandbox.sh` | Sourceable control script: `sandbox proxy up/status/reload`, `sandbox run`. |
| `proxy/src/*.go` | The proxy, stdlib only, split by concern (see tree above). |
| `proxy/config.json` | Rules + secret definitions. |
| `proxy/Dockerfile` | Proxy image (builds `src/`, ships a static binary on alpine). |
| `sandbox/Dockerfile` | Workload image: Ubuntu 26.04, zsh, go, node + bun + gh (upstream), git, jq, ripgrep, vim, file, man. |
| `sandbox/entrypoint.sh` | Installs the CA and points git at the proxy. |

## Configuration

`config.json`:

```json
{
  "secrets": {
    "github":     { "type": "basic",  "env": "GH_TOKEN", "username": "x-access-token" },
    "github-api": { "type": "bearer", "env": "GH_TOKEN" },
    "npm":        { "type": "bearer", "env": "NPM_TOKEN" }
  },
  "rules": [
    { "host": "github.com",         "inject": "github" },
    { "host": "api.github.com",     "inject": "github-api" },
    { "host": "registry.npmjs.org", "inject": "npm" }
  ]
}
```

- **secrets** — `type` is `bearer` or `basic`; `env` names the host env var that
  holds the token (never written in the file). `username` is for basic auth
  (GitHub uses the token as the *password* with any username; `x-access-token`
  is the convention).
- **rules** — matched by `host` (exact) only. `inject` names a secret to add on
  every request to that host; omit it to allow the host with no credential
  added. All methods and paths for a listed host are covered.

Edit and restart to reload.

### Allowing all hosts (`allow_all`)

Set `"allow_all": true` to open egress to every host while keeping credential
injection scoped to the hosts you configured:

```json
{ "allow_all": true, "secrets": { ... }, "rules": [ ... ] }
```

Behaviour with `allow_all` on:

- **Hosts that have rules** are still TLS-intercepted and *strictly gated* — a
  request that matches no rule for that host is denied (403), even under
  `allow_all`. This is what keeps injection targets locked down.
- **Every other host** is **blind-tunneled**: the proxy splices the encrypted
  stream straight through without terminating TLS, so it never sees the
  plaintext and never injects anything. (Clients don't even need the proxy CA
  for these hosts — the real upstream certificate is presented.)

⚠️ **This turns off egress containment.** With `allow_all` the proxy is a
credential *broker*, not a firewall: a compromised workload can send data to any
host it likes. Only use it when the workload is trusted enough that free egress
is acceptable and you just want scoped token injection. Leave it `false` for
untrusted code.

## Usage: the `sandbox` command (recommended)

Source the control script once; it gives you a `sandbox` function that manages
**one shared proxy** and **any number of sandbox containers**.

```bash
source sandbox.sh

# GH_TOKEN is taken from `gh auth token` automatically if it isn't already set.
export NPM_TOKEN=npm_xxx           # optional; export any secret your rules need

sandbox proxy up                   # build (if needed) + start the shared proxy
sandbox proxy status               # is it running? on which networks?

# in any project directory:
cd ~/code/my-app
sandbox run                        # ensures proxy is up, opens a shell in $PWD
# ...or run a command directly:
sandbox run npm ci
sandbox run git clone https://github.com/you/private.git
```

Inside a sandbox there is **no token in the environment**, yet git/npm are
authenticated — the proxy injects credentials on the way out. Run as many
sandboxes as you like at once; they all share the single proxy:

```bash
(cd ~/code/app-a && sandbox run npm test) &
(cd ~/code/app-b && sandbox run npm test) &
sandbox ps                         # list running sandboxes
```

Commands:

| Command | Does |
|---------|------|
| `sandbox proxy up` | Build if needed, start the shared proxy. |
| `sandbox proxy status` | Show whether it's running and its networks. |
| `sandbox proxy reload` | Restart the proxy, **picking up current env/tokens** and config edits. |
| `sandbox proxy down` / `logs` | Stop+remove / follow logs. |
| `sandbox run [cmd...]` | Ensure the proxy is up, run a sandbox in `$PWD` (shell if no cmd). |
| `sandbox build [proxy\|box\|all]` | Force-rebuild images. |
| `sandbox ps` | List running sandboxes. |

**Where secrets come from:** for each var in `$SANDBOX_SECRET_ENVS` (default
`GH_TOKEN NPM_TOKEN`), `sandbox` uses the environment value if set, otherwise
runs `SANDBOX_<VAR>_CMD` on the host. `GH_TOKEN` defaults to `gh auth token`, so
just being logged into `gh` is enough — no need to export anything. Resolution
happens in a subshell at proxy up/reload, so tokens never persist in your shell,
and they're passed to the container by name (never on the command line). To pull
another secret from a command, e.g.:

```bash
export SANDBOX_SECRET_ENVS="GH_TOKEN NPM_TOKEN AWS_TOKEN"
export SANDBOX_AWS_TOKEN_CMD="aws-vault exec me -- printenv AWS_SESSION_TOKEN"
```

**Persisting tool configs / caches:** set `SANDBOX_VOLUMES` to a
whitespace-separated list of docker `-v` specs and every `sandbox run` mounts
them (named volumes are created on first use and survive across sandboxes):

```bash
export SANDBOX_VOLUMES="claude-config:/root/.claude codex-config:/root/.codex pi-config:/root/.pi"
```

Each entry is a raw `-v` spec, so host paths (`$HOME/.foo:/root/.foo`) and
read-only mounts (`somevol:/root/.x:ro`) work too. Note these are shared across
all sandboxes, so treat anything mounted there as readable by any workload.

**Changing tokens or rules:** edit `proxy/config.json` and/or update the token
source, then `sandbox proxy reload` — it re-resolves secrets (picking up a
rotated `gh` token) and restarts. The CA is stored in a persistent Docker
volume, so it survives reloads and sandboxes keep trusting it.

The sandbox network is created `--internal`, so a sandbox physically cannot
reach the internet except through the proxy. Confirm:

```bash
sandbox run sh -c 'env | grep -i token'    # -> nothing
sandbox run curl -s https://example.com    # -> 403 unless allow-listed / allow_all
```

Override defaults (network/image/volume names, which env vars are forwarded as
secrets) by exporting `SANDBOX_*` vars before sourcing — see the top of
`sandbox.sh`.

**Installing packages at runtime:** the image sets both upper- and lower-case
proxy vars, so `apt`, `curl`, `wget`, `go`, `npm`, `bun` all route through the
proxy — `apt update && apt install <pkg>` works (needs `allow_all`, or the Ubuntu
archive hosts allow-listed). These installs live in the `--rm` container and
vanish on exit; for anything you want every time, add it to `sandbox/Dockerfile`
and `sandbox build box`. Your host `TERM`/`COLORTERM` are forwarded too, so
full-color TUIs work.

## Run without Docker (portable binary)

The proxy is just a binary — no runtime deps.

```bash
cd proxy/src
go build -o sandbox-proxy .                 # or: GOOS=linux GOARCH=amd64 go build ...

GH_TOKEN=ghp_xxx NPM_TOKEN=npm_xxx ./sandbox-proxy   # listens on :3128, writes ca/ca.crt

# point any client at it:
export HTTPS_PROXY=http://127.0.0.1:3128
curl --cacert ca/ca.crt https://api.github.com/user   # authenticated, token never left the proxy
git -c http.proxy=$HTTPS_PROXY clone https://github.com/your/private.git
```

Cross-compile for any target with `GOOS`/`GOARCH` — the output is a single
static file you can drop anywhere.

### Tests

```bash
cd proxy/src && go test ./...
```

Covers the policy engine (host matching, `decide`, credential injection incl.
placeholder overwrite), config loading, header/host helpers, WebSocket-upgrade
detection, and end-to-end forward-proxy **and** CONNECT/MITM flows (injection,
default-deny, `allow_all`) via `httptest`. `_test.go` files are excluded from the
built binary.

## Environment variables

| Var | Default | Meaning |
|-----|---------|---------|
| `PROXY_LISTEN` | `:3128` | listen address |
| `PROXY_CONFIG` | `config.json` | rules file |
| `PROXY_CA_DIR` | `ca` | where `ca.crt` / `ca.key` are stored/generated |
| `HTTP(S)_PROXY` | — | upstream proxy for the proxy's own egress (optional) |

## Security notes

- **Guard `ca.key`** — anyone with it can mint trusted certs for the sandbox.
  It's created `0600`; keep the volume private.
- **Lock down the network**, not just the env. The `--internal` sandbox network
  (`sandbox-net`) is what actually forces traffic through the proxy; without it a
  workload could ignore `HTTPS_PROXY` and dial out directly. `sandbox.sh` creates
  it internal by default.
- **Scope tightly.** Prefer `api.github.com` + specific paths over broad wildcards
  so a leaked-but-injected token is useful only for what you intended.
