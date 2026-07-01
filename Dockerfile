# check=error=true
FROM golang:1.26.4-alpine@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS builder
ENV GOTOOLCHAIN=local

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY *.go ./
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tautulli-remap .

FROM gcr.io/distroless/static-debian13:nonroot@sha256:963fa6c544fe5ce420f1f54fb88b6fb01479f054c8056d0f74cc2c6000df5240

COPY --chmod=755 --from=builder /tautulli-remap /tautulli-remap
USER nonroot:nonroot
# The "health" subcommand probes the /tmp/.healthy marker the main
# process maintains. In scheduled mode it is created at startup, refreshed after
# each run, and removed after 3 consecutive failures; in resident-idle mode it
# reflects process liveness (created at startup, present while the process is
# alive). Exits 0 if the marker is present, else 1. See the README
# "Healthcheck" section.
HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=15s \
    CMD ["/tautulli-remap", "health"]
ENTRYPOINT ["/tautulli-remap"]
