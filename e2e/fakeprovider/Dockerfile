# syntax=docker/dockerfile:1.29.0@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183
# SPDX-License-Identifier: MIT
#
# The e2e fake model provider. Test only: e2e/compose.e2e.yaml builds it from the repository
# root; it is never pushed and never part of a release. Same Go and distroless bases as the edge
# image, so the build reuses their layers.
FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY e2e/fakeprovider ./e2e/fakeprovider
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w" -o /out/fakeprovider ./e2e/fakeprovider

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/fakeprovider /usr/local/bin/fakeprovider
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fakeprovider"]
CMD ["serve", ":8080"]
