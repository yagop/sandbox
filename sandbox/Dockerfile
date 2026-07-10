# Sandbox workload image: a general Ubuntu dev environment that reaches the
# network ONLY through the proxy, and trusts the proxy's CA.
FROM ubuntu:26.04

# ARG (not ENV): keeps apt non-interactive during the build without leaking
# DEBIAN_FRONTEND into the running container's environment.
ARG DEBIAN_FRONTEND=noninteractive
# pipefail so a broken download in a pipe fails the build instead of sailing on.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Ubuntu container images are "minimized": man pages and docs are excluded by
# dpkg. Drop those excludes BEFORE installing so `man` actually has content.
RUN rm -f /etc/dpkg/dpkg.cfg.d/excludes

RUN apt-get update && apt-get install -y \
        ca-certificates \
        curl \
        git \
        golang-go \
        man-db \
        manpages \
        file \
        vim \
        ripgrep \
        jq \
        socat \
        locales \
        ncurses-term \
        zsh \
        unzip \
        xz-utils \
        libatomic1 \
        libstdc++6

# Generate a UTF-8 locale so tools don't warn / mangle non-ASCII output.
RUN locale-gen en_US.UTF-8
ENV LANG=en_US.UTF-8 \
    LANGUAGE=en_US:en \
    LC_ALL=en_US.UTF-8

# node — latest release straight from nodejs.org (filename carries the version,
# so discover it from the directory index).
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
        amd64) node_arch=x64 ;; \
        arm64) node_arch=arm64 ;; \
        *) echo "unsupported arch for node" >&2; exit 1 ;; \
    esac; \
    base=https://nodejs.org/download/release/latest; \
    file="$(curl -fsSL "$base/" | grep -oE "node-v[0-9.]+-linux-${node_arch}\.tar\.xz" | head -1)"; \
    test -n "$file"; \
    curl -fsSL "$base/$file" -o /tmp/node.tar.xz; \
    tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1; \
    rm /tmp/node.tar.xz; \
    node --version && npm --version

# bun — latest release, resolved via the GitHub API (jq installed above).
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
        amd64) bun_arch=x64 ;; \
        arm64) bun_arch=aarch64 ;; \
        *) echo "unsupported arch for bun" >&2; exit 1 ;; \
    esac; \
    url="$(curl -fsSL https://api.github.com/repos/oven-sh/bun/releases/latest \
        | jq -r --arg n "bun-linux-${bun_arch}.zip" '.assets[] | select(.name==$n) | .browser_download_url')"; \
    test -n "$url"; \
    curl -fsSL "$url" -o /tmp/bun.zip; \
    unzip -q /tmp/bun.zip -d /tmp/bun; \
    install -m 0755 "/tmp/bun/bun-linux-${bun_arch}/bun" /usr/local/bin/bun; \
    rm -rf /tmp/bun /tmp/bun.zip; \
    bun --version

# gh (GitHub CLI) — latest release, resolved via the GitHub API.
RUN set -eu; \
    arch="$(dpkg --print-architecture)"; \
    url="$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest \
        | jq -r --arg suf "_linux_${arch}.tar.gz" '.assets[] | select(.name|endswith($suf)) | .browser_download_url')"; \
    test -n "$url"; \
    curl -fsSL "$url" -o /tmp/gh.tgz; \
    tar -xzf /tmp/gh.tgz -C /usr/local --strip-components=1; \
    rm /tmp/gh.tgz; \
    gh --version

# flyctl (fly.io CLI) — latest release, resolved via the GitHub API.
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
        amd64) fly_arch=x86_64 ;; \
        arm64) fly_arch=arm64 ;; \
        *) echo "unsupported arch for flyctl" >&2; exit 1 ;; \
    esac; \
    url="$(curl -fsSL https://api.github.com/repos/superfly/flyctl/releases/latest \
        | jq -r --arg suf "_Linux_${fly_arch}.tar.gz" '.assets[] | select(.name|endswith($suf)) | .browser_download_url')"; \
    test -n "$url"; \
    curl -fsSL "$url" -o /tmp/fly.tgz; \
    tar -xzf /tmp/fly.tgz -C /tmp flyctl; \
    install -m 0755 /tmp/flyctl /usr/local/bin/flyctl; \
    ln -s flyctl /usr/local/bin/fly; \
    rm /tmp/fly.tgz /tmp/flyctl; \
    flyctl version

# Trust the proxy CA so TLS interception is transparent to git/curl/go, and to
# node/bun (which use their own bundled trust store, hence NODE_EXTRA_CA_CERTS).
# The CA is bind-mounted at /ca; entrypoint installs it into the system store.
COPY entrypoint.sh /usr/local/bin/sandbox-entrypoint.sh
COPY forward.sh /usr/local/bin/forward
RUN chmod +x /usr/local/bin/sandbox-entrypoint.sh /usr/local/bin/forward \
    && ln -s forward /usr/local/bin/tunnel

# agent aliases for the interactive sandbox shell (~ expands at use time).
RUN printf '%s\n' \
      "alias claude='IS_SANDBOX=1 bun x @anthropic-ai/claude-code --effort xhigh --dangerously-skip-permissions --append-system-prompt ~/.claude/USER.md'" \
      "alias codex='bun x @openai/codex --yolo'" \
      "alias pi='bun x @earendil-works/pi-coding-agent'" \
      >> /etc/zsh/zshrc

# The proxy is reachable by its container name on the shared network. Set both
# UPPER and lower case: apt/wget/many tools only read the lowercase variants.
ENV HTTP_PROXY=http://sandbox-proxy:3128 \
    HTTPS_PROXY=http://sandbox-proxy:3128 \
    NO_PROXY=localhost,127.0.0.1 \
    http_proxy=http://sandbox-proxy:3128 \
    https_proxy=http://sandbox-proxy:3128 \
    no_proxy=localhost,127.0.0.1 \
    NODE_EXTRA_CA_CERTS=/ca/ca.crt \
    NODE_USE_ENV_PROXY=1
# NOTE: the gh placeholder token is set in entrypoint.sh, not here, to avoid
# Docker's SecretsUsedInArgOrEnv warning (it flags any ENV named *TOKEN*).

ENTRYPOINT ["sandbox-entrypoint.sh"]
CMD ["zsh"]
