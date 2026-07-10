# 🔐 sandbox-proxy

A zero-dependency, **stdlib-only** Go forward proxy that injects your real
credentials (GitHub `GH_TOKEN`, npm token, …) into outbound requests **on the
wire**, so code running in a sandbox can use them without ever *seeing* them.

A simplified version of [Infisical's agent-vault](https://github.com/Infisical/agent-vault),
driven by a single script (`sandbox.sh`) over a dependency-free proxy binary you own.

## 🔒 What it does

Code runs in a container with **no network access except through the proxy**.
The proxy holds your tokens and injects them into outbound requests as they
leave — so the workload can *use* them but never *sees* them.

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

- 🌐 **Open by default** (`allow_all: true`) for easy setup — egress to any host,
  with credentials injected only on your configured hosts. Set `allow_all: false`
  for strict default-deny (only listed hosts reachable).
- 🔏 **HTTPS interception** via a CA it generates on first run and the sandbox
  trusts; the intercepted TLS speaks **HTTP/1.1 only** (ALPN pins `http/1.1`),
  and hosts you don't inject into can be blind-tunnelled untouched.
- 🛡️ A compromised workload can at most *use* a token against the hosts you
  allow — it can't read or exfiltrate the secret itself.

## 🚀 Usage: the `sandbox` command

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

**🔑 Where secrets come from:** for each var in `$SANDBOX_SECRET_ENVS`,
`sandbox` uses the environment value if set, otherwise runs
`SANDBOX_<VAR>_CMD` on the host. The default set:

```bash
GH_TOKEN                    # defaults to `gh auth token` — being logged into gh is enough
NPM_TOKEN
SCW_SECRET_KEY
SCW_COCKPIT_TOKEN_<REGION>  # one per Cockpit region (FR_PAR, NL_AMS, PL_WAW) — tokens are region-scoped
```

Resolution
happens in a subshell at proxy up/reload, so tokens never persist in your shell,
and they're passed to the container by name (never on the command line). To pull
another secret from a command, e.g.:

```bash
export SANDBOX_SECRET_ENVS="GH_TOKEN NPM_TOKEN AWS_TOKEN"
export SANDBOX_AWS_TOKEN_CMD="aws-vault exec me -- printenv AWS_SESSION_TOKEN"
```

**💾 Persisting tool configs / caches:** set `SANDBOX_VOLUMES` to a
whitespace-separated list of docker `-v` specs and every `sandbox run` mounts
them (named volumes are created on first use and survive across sandboxes):

```bash
export SANDBOX_VOLUMES="claude-config:/root/.claude codex-config:/root/.codex pi-config:/root/.pi"
```

Each entry is a raw `-v` spec, so host paths (`$HOME/.foo:/root/.foo`) and
read-only mounts (`somevol:/root/.x:ro`) work too. Note these are shared across
all sandboxes, so treat anything mounted there as readable by any workload.

**♻️ Changing tokens or rules:** edit `proxy/config.json` and/or update the token
source, then `sandbox proxy reload` — it re-resolves secrets (picking up a
rotated `gh` token) and restarts. The CA is stored in a persistent Docker
volume, so it survives reloads and sandboxes keep trusting it.

The sandbox network is created `--internal`, so a sandbox physically cannot
reach the internet except through the proxy. Confirm:

```bash
sandbox run sh -c 'env | grep -i token'    # -> nothing (no token inside the sandbox)
sandbox run curl -sI https://example.com   # reachable via the proxy (allow_all default);
                                           # with allow_all:false an unlisted host -> 403
```

Override defaults (network/image/volume names, which env vars are forwarded as
secrets) by exporting `SANDBOX_*` vars before sourcing — see the top of
`sandbox.sh`.

**📦 Installing packages at runtime:** the image sets both upper- and lower-case
proxy vars, so `apt`, `curl`, `wget`, `go`, `npm`, `bun` all route through the
proxy — `apt update && apt install <pkg>` works (needs `allow_all`, or the Ubuntu
archive hosts allow-listed). These installs live in the `--rm` container and
vanish on exit; for anything you want every time, add it to `sandbox/Dockerfile`
and `sandbox build box`. Your host `TERM`/`COLORTERM` are forwarded too, so
full-color TUIs work.

**🔌 Reaching raw-TCP / LAN services:** the proxy only speaks HTTP/HTTPS, but its
CONNECT/blind-tunnel is a generic TCP splice — so any TCP service is reachable
via the bundled `forward` (alias `tunnel`) helper, which exposes it on
`127.0.0.1` through the proxy:

```bash
forward 192.168.1.39 5432          # 127.0.0.1:5432 -> 192.168.1.39:5432
forward 192.168.1.39 5432 6379 &   # several ports, backgrounded
```

Point your client at `127.0.0.1:<port>`. Keep `allow_all` on and don't add a rule
for the host — a rule would MITM it and break the raw stream. The proxy must be
able to reach the target on the LAN.

## ⚙️ Configuration

`proxy/config.json` maps **secrets** (how to build an auth header, with the value
read from the proxy's environment) to **rules** (which host gets which secret):

```json
{
  "allow_all": true,
  "secrets": {
    "github":     { "type": "basic",  "env": "GH_TOKEN", "username": "x-access-token" },
    "github-api": { "type": "bearer", "env": "GH_TOKEN" },
    "npm":        { "type": "bearer", "env": "NPM_TOKEN" },
    "scaleway":   { "type": "header", "env": "SCW_SECRET_KEY", "header": "X-Auth-Token" }
  },
  "rules": [
    { "host": "github.com",              "inject": "github" },
    { "host": "api.github.com",          "inject": "github-api" },
    { "host": "*.githubusercontent.com" },
    { "host": "registry.npmjs.org",      "inject": "npm" },
    { "host": "api.scaleway.com",        "inject": "scaleway" }
  ]
}
```

- **secrets** — `type` is `bearer`, `basic`, or `header`; `env` names the host
  env var holding the token (never the value itself). `username` is for basic
  auth (GitHub uses the token as the *password* with any username). `header`
  sets the token in a custom header named by `header` — e.g. Scaleway's
  `X-Auth-Token`.
- **rules** — matched by `host`, covering all methods and paths. A `*.` prefix
  is a suffix wildcard: `*.githubusercontent.com` matches `objects.` / `raw.` /
  any subdomain (and the bare domain) — handy for CDN hosts behind
  gh-release/git-lfs/npm-tarball downloads. `inject` names a secret to add on
  every request; omit it to allow a host with no credential added.
- **`allow_all`** — **`true` by default** for simplicity: egress to any host,
  with injection still scoped to listed hosts (others are blind-tunnelled,
  untouched). Set it to `false` for strict default-deny — only listed hosts are
  reachable. ⚠️ With `allow_all` on, the proxy is a credential *broker*, not a
  firewall: a compromised workload can send data anywhere. Flip it to `false` for
  untrusted code.

Run `sandbox proxy reload` to re-read the config.

## 📦 Run without Docker (portable binary)

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

### 🧪 Tests

```bash
cd proxy/src && go test ./...
```

Covers the policy engine (host matching, `decide`, credential injection incl.
placeholder overwrite), config loading, header/host helpers, WebSocket-upgrade
detection, and end-to-end forward-proxy **and** CONNECT/MITM flows (injection,
default-deny, `allow_all`) via `httptest`. `_test.go` files are excluded from the
built binary.

## 🔧 Environment variables

| Var | Default | Meaning |
|-----|---------|---------|
| `PROXY_LISTEN` | `:3128` | listen address |
| `PROXY_CONFIG` | `config.json` | rules file |
| `PROXY_CA_DIR` | `ca` | where `ca.crt` / `ca.key` are stored/generated |
| `HTTP(S)_PROXY` | — | upstream proxy for the proxy's own egress (optional) |

## 🛡️ Security notes

- **`ca.key` stays in the proxy.** It lives (mode `0600`) in the proxy-only CA
  volume; sandboxes bind-mount **only** the public `ca.crt` read-only, never the
  volume — so a workload can't read the key and mint trusted certs. Keep the CA
  volume private on the host.
- **Lock down the network**, not just the env. The `--internal` sandbox network
  (`sandbox-net`) is what actually forces traffic through the proxy; without it a
  workload could ignore `HTTPS_PROXY` and dial out directly. `sandbox.sh` creates
  it internal by default.
- **Scope tightly.** Prefer specific injection hosts over broad `allow_all` so a
  leaked-but-injected token is useful only for what you intended.
