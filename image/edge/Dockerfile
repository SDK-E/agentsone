# syntax=docker/dockerfile:1.29.0@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183
# SPDX-License-Identifier: MIT
#
# Edge image: the `agentsone` binary that serves both the `edge` and the `control` services of
# compose.yaml. Build from the repository root:
#
#   docker buildx build -f image/edge/Dockerfile --platform linux/arm64 --load -t agentsone-edge:dev .
#
# Runtime is distroless static (nonroot): the binary is pure Go (no cgo, the SQLite driver is
# pure Go), so the image needs no libc, no shell and no package manager. That leaves nothing in
# the wall container for an intruder to use: no sh, no curl, no apt. Debian slim would add a
# shell and about 75 MB for no gain. The health check uses the small `probe` program because
# there is no shell to run one.

# The compiler runs on the build machine and cross-compiles, so arm64 builds need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/agentsone ./cmd/agentsone
COPY internal ./internal
COPY image/edge/probe ./image/edge/probe
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" -o /out/agentsone ./cmd/agentsone \
 && GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags "-s -w" -o /out/probe ./image/edge/probe
RUN mkdir -p /out/empty

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
LABEL org.opencontainers.image.title="agentsone-edge" \
      org.opencontainers.image.description="Agents One edge and control: internet filter, model gateway, budget and the human-only control page" \
      org.opencontainers.image.source="https://github.com/SDK-E/agentsone" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/agentsone /out/probe /usr/local/bin/
# Folders the services write to. Everything else in the image stays read-only (compose sets
# read_only). Docker copies these owners and modes into a new named volume on first start.
#   data:  edge state (keys, allow list, budget). Edge user only.
#   admin: the admin socket. Control shares group 65532 and may only enter and connect;
#          machine never mounts it.
COPY --from=build --chown=65532:65532 --chmod=0700 /out/empty /var/lib/agentsone-edge
#   gateway-key: the token signing key, also mounted read-only into the machine.
COPY --from=build --chown=65532:65532 --chmod=0700 /out/empty /var/lib/agentsone-edge/gateway-key
COPY --from=build --chown=65532:65532 --chmod=0750 /out/empty /run/agentsone-edge-admin
# 65532 is the distroless nonroot user. compose.yaml runs control as 65533 in the same group.
USER 65532:65532
# Default role is the edge; its admin socket is up once the edge serves. compose.yaml sets the
# right check for each role.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 \
  CMD ["/usr/local/bin/probe", "unix", "/run/agentsone-edge-admin/admin.sock"]
ENTRYPOINT ["/usr/local/bin/agentsone"]
CMD ["edge"]
