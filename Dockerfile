# check=error=true
FROM golang:1.26.5-alpine@sha256:99e12cfb19b753915f9b9fdc5a99f1869a24a69d3a0955832d5702e7fa68f1be AS builder
# GOTOOLCHAIN=auto: a Renovate dep bump requiring a newer Go downloads that toolchain
# instead of failing the build (org convention, go.md/ci-cd.md); still reproducible
# because go.mod pins the toolchain version. `local` would hard-fail such a build.
ENV GOTOOLCHAIN=auto

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY *.go ./
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tautulli-remap .

FROM gcr.io/distroless/static-debian13:nonroot@sha256:d29e660cc75a5b6b1334e03c5c81ccf9bc0884a002c6000dbf0fb96034814478

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
