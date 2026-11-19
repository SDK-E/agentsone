# syntax=docker/dockerfile:1.29.0@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183
# SPDX-License-Identifier: MIT
#
# Agents One machine image: Debian 13 with agentsoned, the runners, the web
# interface, Node.js 24, the VSCodium server and the desktop shown in the
# browser (labwc + wayvnc, headless).
#
# Build from the repository root:
#   docker buildx build -f image/machine/Dockerfile --platform linux/arm64 -t agentsone-machine:dev .
#
# Build stages run on the build machine and cross-compile, so a multi-arch
# build emulates only the final package install.
#
# Run it unprivileged, with no added capabilities and no Docker socket:
#   cgroup: private, security_opt: [writable-cgroups=true, no-new-privileges=true]
# writable-cgroups lets the daemon give each agent its own cgroup limits and
# needs Docker Engine 28.0 or later; older engines refuse to start the
# container ("invalid --security-opt"). image/ci/smoke.sh runs it this way.

# --- Go programs ------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH image/machine/build-go.sh /src /out

# --- Web interface (static files, the same for every architecture) ----------
FROM --platform=$BUILDPLATFORM node:24.22.3-trixie-slim@sha256:af02d57b57719b9657a45199a79264a9437940cd55b3c7b3fc99615654f124a0 AS ui
WORKDIR /src
COPY . .
RUN if [ ! -f ui/package.json ]; then \
        echo "skip: ui/ has no package.json yet"; mkdir -p /out; \
    else \
        corepack enable \
        && COREPACK_ENABLE_DOWNLOAD_PROMPT=0 pnpm install --frozen-lockfile \
        && pnpm --filter ./ui build \
        && cp -a ui/dist /out; \
    fi

# --- Parts that do not come from Debian, checked against image/machine/pins.txt
FROM --platform=$BUILDPLATFORM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS fetch
# hadolint ignore=DL3008
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY image/machine/fetch-pinned.sh image/machine/pins.txt /fetch/
ARG TARGETARCH
RUN /fetch/fetch-pinned.sh /fetch/pins.txt node "$TARGETARCH" /tmp/node.tar.gz \
    && mkdir -p /out/usr/local /out/usr/share/doc/nodejs \
    && tar -xzf /tmp/node.tar.gz -C /out/usr/local --strip-components=1 --no-same-owner \
    && mv /out/usr/local/LICENSE /out/usr/share/doc/nodejs/LICENSE \
    && rm /out/usr/local/CHANGELOG.md /out/usr/local/README.md /tmp/node.tar.gz
RUN /fetch/fetch-pinned.sh /fetch/pins.txt vscodium-server "$TARGETARCH" /tmp/vscodium-server.tar.gz \
    && mkdir -p /out/opt/vscodium-server \
    && tar -xzf /tmp/vscodium-server.tar.gz -C /out/opt/vscodium-server --no-same-owner \
    && rm /tmp/vscodium-server.tar.gz

# --- The machine ------------------------------------------------------------
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2

LABEL org.opencontainers.image.title="Agents One machine" \
      org.opencontainers.image.description="Debian 13 machine where Agents One agents live: daemon, runners, web interface, IDE server and browser desktop." \
      org.opencontainers.image.source="https://github.com/SDK-E/agentsone" \
      org.opencontainers.image.licenses="MIT"

# Debian packages follow Debian stable (security updates included), so they
# are not pinned to versions; the base image is pinned by digest.
#   tini         PID 1: forwards signals and reaps orphaned processes
#   passwd       useradd and groupadd, for the daemon's agent user reconcile
#   ca-certificates curl git less procps   what agents and tools expect
#   labwc wayvnc wlr-randr foot chromium fonts-dejavu-core
#                headless desktop with a terminal and a web browser, shown in
#                the browser; wayvnc's encoder brings the ffmpeg libraries
# No sudo: root agents are already root; custom agents must not become root.
# The slim base leaves out manual pages, so update-alternatives notes each
# missing page link; those lines (and only those) are dropped from the log.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
# hadolint ignore=DL3008
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        tini passwd \
        ca-certificates curl git less procps \
        labwc wayvnc wlr-randr foot chromium fonts-dejavu-core \
        2>&1 | { grep -v '^update-alternatives: warning: skip creation of /usr/share/man/' || true; } \
    && rm -rf /var/lib/apt/lists/*

COPY --from=fetch /out/ /
COPY --from=go /out/ /usr/lib/agentsone/
COPY --from=ui /out/ /usr/lib/agentsone/ui/
COPY image/machine/config.toml /usr/lib/agentsone/config.toml
COPY --chmod=0755 image/machine/entrypoint.sh /usr/lib/agentsone/entrypoint
COPY --chmod=0755 image/machine/healthcheck.sh /usr/lib/agentsone/healthcheck

EXPOSE 7700
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --start-interval=2s --retries=3 \
    CMD ["/usr/lib/agentsone/healthcheck"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/lib/agentsone/entrypoint"]
CMD ["/usr/lib/agentsone/bin/agentsoned"]
