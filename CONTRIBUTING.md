# Contributing to tautulli-remap

Notes specific to this repo. For org-wide defaults (PR flow, commit
conventions, code of conduct) see the links at the bottom; this file covers
the architecture and local workflow a contributor needs to land a change.

## What this is

A stdlib-only Go service that repairs Tautulli watch history after a Plex
library reorganization assigns new internal rating keys. It runs once
(`SCHEDULE_HOURS=0`) or on a schedule, has no inbound listener, and ships as a
distroless nonroot image. See `README.md` for the user-facing configuration
reference.

## Package layout

`main.go` is wiring only — it loads config, builds an `*http.Client`,
constructs the Plex and Tautulli clients, and hands them to the orchestrator.
The `health` subcommand (`tautulli-remap health`) is dispatched at the top of
`main()` before anything else. All real logic lives under `internal/`:

- `internal/config` — environment parsing into a `Config` struct. Required
  vars (`TAUTULLI_APIKEY`, `PLEX_TOKEN`) return a `MissingEnvError`; booleans
  go through `GetEnvBool` (tolerant of `true/1/yes/on`).
- `internal/orchestrator` — coordinates the six-step run: collect Tautulli
  history, find stale keys, build the Plex index, match, apply remappings,
  clear recently-added. Owns the scheduler loop and the circuit breaker.
- `internal/remap` — pure matching logic and domain types. No I/O. GUID
  normalization, the three-strategy match chain, the Plex index builder, and
  all the `MediaType` / `MatchMethod` / `RatingKey` types live here.
- `internal/plex`, `internal/tautulli` — HTTP API clients for each upstream.
- `internal/httputil` — small shared HTTP helpers (error sanitization,
  context-aware sleep).

## Architecture notes that are easy to miss

**Interfaces are defined by the consumer, not the implementation.** The
`PlexClient` and `TautulliClient` interfaces live in
`internal/orchestrator`, shaped by exactly what the orchestrator calls — not
in the `plex`/`tautulli` packages. `main.go` asserts the concrete clients
satisfy them at compile time:

```go
var (
    _ orchestrator.PlexClient     = (*plex.Client)(nil)
    _ orchestrator.TautulliClient = (*tautulli.Client)(nil)
)
```

When you add a method the orchestrator needs, add it to the consumer interface
and the assertions catch a missing implementation at build time.

**The match chain is ordered by increasing aggressiveness.** `remap.MatchOne`
tries GUID, then title+year (gated by `FallbackTitleYear`), then title-only
(gated by `FallbackTitleOnly`, and guarded to the same media type). Every
strategy refuses to return the old key. Keep this ordering and the
old-key guard when extending it — they are the safety net against false
matches.

**Dry-run is the default and must stay safe.** `DRY_RUN` defaults to `true`.
In dry-run the orchestrator skips the backup, logs each would-be remap at
debug level, and never calls `UpdateMetadata`. Any new write path must respect
`cfg.DryRun` the same way.

**Re-runs must be idempotent.** The scheduler can fire the same run
repeatedly; remapping only stale keys and skipping the recently-added clear
when nothing was updated keeps it safe to repeat.

**GUID normalization is canonical and shared.** Index keys and lookup keys
both go through `remap.NormalizeGUID` / `remap.NormalizeTitle`, so the
"index key == lookup key" invariant holds by construction. Don't normalize ad
hoc at a call site.

## Local development

The repo is pure Go with no Makefile — use the standard toolchain from the
repo root. Go version is pinned in `go.mod`.

```sh
go build ./...                  # compile
go test ./...                   # all tests
go test -cover ./...            # with coverage
golangci-lint run               # lint (config: .golangci.yaml, v2)
golangci-lint fmt               # apply gofumpt + gci formatting
govulncheck ./...               # vulnerability scan
```

`golangci-lint run` also reports unformatted files as issues, so running it is
enough to catch formatting drift; `golangci-lint fmt` fixes it.

## Conventions and gotchas

- **Stdlib-only.** Production code depends on the standard library plus two
  first-party shared libs (`cplieger/health`, `golang.org/x/sync`). Don't add
  third-party runtime dependencies without discussing it first — zero external
  deps is a deliberate supply-chain choice.
- **Structured logging is key-value only.** `sloglint` runs with `kv-only`,
  so every `slog` call must use `slog.Info("msg", "key", val)` form — no
  attribute helpers. Match the existing log shape.
- **Lint is strict.** `gocyclo` trips at complexity 18; `gosec`, `gocritic`,
  `revive`, and friends are enabled. `gofumpt` runs with `extra-rules`
  (grouped same-type params, no naked returns).
- **Rating keys are validated before URL interpolation.** They must be numeric
  (`RatingKey.IsValid`) before being placed into a request path — this guards
  against path traversal. Preserve that check on any new request builder.
- **Secrets never reach logs.** Plex tokens go in the `X-Plex-Token` header,
  and HTTP error messages strip query params. Don't log tokens or raw URLs.

## Testing

Tests sit beside the code they cover. The suite is table-driven plus
property-based via [rapid](https://github.com/flyingmutant/rapid); property
tests assert invariants like GUID-normalization idempotency, panic-freedom,
and rating-key coercion round-trips. API clients are exercised against
`httptest` mock servers covering retry, pagination, and context cancellation.
`main()` itself is intentionally untested (thin signal/scheduler wrapper
validated by the Docker healthcheck). Add tests for new logic and keep the
property tests passing.

## Commits and PRs

This repo uses [Conventional Commits](https://www.conventionalcommits.org/)
parsed by git-cliff to generate release notes — write the subject as the
changelog line a user would read (`feat: add title-only match guard`,
`fix: avoid backup in dry-run`). Open a PR against `main`; for larger changes,
open an issue first to discuss the approach.

## Conduct & security

By participating you agree to the
[Code of Conduct](https://github.com/cplieger/.github/blob/main/CODE_OF_CONDUCT.md).
Report security issues through the
[security policy](https://github.com/cplieger/.github/blob/main/SECURITY.md),
never in a public issue.
