# syntax=docker/dockerfile:1.7
# Sextant sidecar base image.
#
# Built via `make sidecar-image` from the repo root so the build context
# spans both `clients/typescript/` (the @sextant/client source) and
# `images/sidecar/entrypoint/` (the sidecar runtime). See
# `specs/components/sidecar-image.md` for the full spec.
#
# Plan: plans/bootstrap.md#M9

FROM debian:bookworm-slim

# Every externally-fetched component is pinned to an exact version.
# Override with `--build-arg <NAME>=...` when bumping; commit the spec
# update under §"Version pins" in the same change. See
# specs/components/sidecar-image.md §"Version pins" for the bump
# procedure and the source used to resolve each pin.
ARG GO_VERSION=1.26.3
# Pin Node major (the NodeSource setup script installs the latest minor
# in the requested major line — major+minor lock is the API-stability
# boundary that matters).
ARG NODE_MAJOR=22
# yq release tag (without the leading "v"; the URL adds it back).
# Source: `gh api repos/mikefarah/yq/releases/latest`.
ARG YQ_VERSION=4.53.2
# Claude Code Agent SDK (programmatic). Source: `npm view
# @anthropic-ai/claude-agent-sdk version`.
ARG CLAUDE_AGENT_SDK_VERSION=0.3.150
# Claude Code CLI (interactive). Source: `npm view
# @anthropic-ai/claude-code version`.
ARG CLAUDE_CODE_VERSION=2.1.150

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

# ---------------------------------------------------------------------------
# OS packages — single layer, no recommends, apt lists wiped.
# Toolset comes straight from `specs/components/sidecar-image.md` §"Installed
# tools". `gnupg` + `ca-certificates` are needed for NodeSource's apt repo;
# `sudo` is needed because the spec gives `agent` passwordless sudo (the
# container is the sandbox boundary, not the user).
# ---------------------------------------------------------------------------
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        coreutils \
        curl \
        fzf \
        gcc \
        git \
        gnupg \
        jq \
        less \
        make \
        pkg-config \
        procps \
        python3 \
        python3-pip \
        ripgrep \
        sudo \
        vim \
        wget \
 && rm -rf /var/lib/apt/lists/*

# httpie ships its own apt package on bookworm; pull it in a second layer
# so the failure mode (missing on some mirrors) is obvious if it occurs.
RUN apt-get update && apt-get install -y --no-install-recommends httpie \
 && rm -rf /var/lib/apt/lists/*

# yq isn't in Debian Bookworm; install from the upstream GitHub release.
# Multi-arch via `dpkg --print-architecture` (amd64 / arm64). Exact tag
# from ARG so cache-cold rebuilds match — no releases/latest/download.
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) yq_arch=amd64 ;; \
      arm64) yq_arch=arm64 ;; \
      *) echo "unsupported arch: $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${yq_arch}" \
        -o /usr/local/bin/yq; \
    chmod 0755 /usr/local/bin/yq; \
    yq --version

# gh (GitHub CLI) — install from the official apt repo so we get a recent
# version regardless of Debian's snapshot.
RUN set -eux; \
    mkdir -p -m 755 /etc/apt/keyrings; \
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /etc/apt/keyrings/githubcli-archive-keyring.gpg; \
    chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg; \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends gh; \
    rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# Node 22 via NodeSource. NodeSource maintains a setup script that adds
# the apt repo for the requested major line; we install plain `nodejs`
# (which now includes npm) afterwards.
# ---------------------------------------------------------------------------
RUN set -eux; \
    curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash -; \
    apt-get install -y --no-install-recommends nodejs; \
    rm -rf /var/lib/apt/lists/*; \
    node --version; \
    npm --version

# ---------------------------------------------------------------------------
# Go (latest stable) via official tarball. Multi-arch.
# ---------------------------------------------------------------------------
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) go_arch=amd64 ;; \
      arm64) go_arch=arm64 ;; \
      *) echo "unsupported arch: $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${go_arch}.tar.gz" \
        -o /tmp/go.tar.gz; \
    tar -C /usr/local -xzf /tmp/go.tar.gz; \
    rm /tmp/go.tar.gz; \
    /usr/local/go/bin/go version

ENV PATH=/usr/local/go/bin:/home/agent/go/bin:$PATH \
    GOPATH=/home/agent/go

# ---------------------------------------------------------------------------
# Globally-installed npm packages: the Claude Code Agent SDK (programmatic
# use) and the Claude Code CLI (interactive use). Both pinned to exact
# versions via ARG — no @latest. Bumping = ARG change + spec update in the
# same commit.
# ---------------------------------------------------------------------------
RUN npm install -g --omit=dev \
        "@anthropic-ai/claude-agent-sdk@${CLAUDE_AGENT_SDK_VERSION}" \
        "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && npm cache clean --force

# ---------------------------------------------------------------------------
# Non-root `agent` user. Passwordless sudo per the spec (the container
# itself is the trust boundary).
# ---------------------------------------------------------------------------
RUN set -eux; \
    groupadd --gid 1000 agent; \
    useradd --uid 1000 --gid agent --create-home --shell /bin/bash agent; \
    install -d -o agent -g agent /home/agent/.claude /home/agent/.config /home/agent/.cache /home/agent/.local/share /home/agent/.npm /home/agent/go; \
    echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent; \
    chmod 0440 /etc/sudoers.d/agent; \
    install -d -o agent -g agent /workspace

# ---------------------------------------------------------------------------
# Sidecar runtime: the TypeScript entrypoint plus a local copy of
# `@sextant/client`. `@sextant/client` isn't published yet, so we COPY the
# whole TS client into the image and wire it as a file: dependency in the
# sidecar's package.json. When it's published, swap the file: dep for the
# registry version and drop the client-ts COPY.
# ---------------------------------------------------------------------------
COPY --chown=agent:agent clients/typescript /opt/sextant/client-ts
COPY --chown=agent:agent images/sidecar/entrypoint /opt/sextant/sidecar

USER agent

# Install @sextant/client's runtime deps in-place inside the image. The
# file:../client-ts dep symlinks the package into the sidecar's
# node_modules, so when Node loads `@sextant/client` it resolves `nats`
# and `smol-toml` relative to /opt/sextant/client-ts/node_modules — not
# /opt/sextant/sidecar/node_modules. Run `npm ci --omit=dev` here so the
# linked package has its deps without shipping the host's node_modules
# in the build context.
WORKDIR /opt/sextant/client-ts
RUN set -eux; \
    npm ci --omit=dev --no-audit --no-fund; \
    npm cache clean --force

# Build the sidecar entrypoint. `npm install` (no lockfile — the only
# direct dep is the file:../client-ts symlink) resolves the link, then
# `npm run build` produces dist/index.js.
WORKDIR /opt/sextant/sidecar
RUN set -eux; \
    npm install --no-audit --no-fund; \
    npm run build; \
    # Drop devDependencies (typescript, @types/node) to keep the image lean.
    npm prune --omit=dev; \
    npm cache clean --force; \
    ls dist/index.js

WORKDIR /workspace

# Default CMD: drop into bash so the M9 acceptance test (`docker run --rm
# sextant-sidecar:latest /bin/bash`) lands in an interactive shell with
# the full tool set on PATH. The sidecar runtime is started explicitly by
# sextantd at spawn time via `/opt/sextant/sidecar/entrypoint.sh`.
ENTRYPOINT []
CMD ["/bin/bash"]
