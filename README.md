# tautulli-remap

[![Image Size](https://ghcr-badge.egpl.dev/cplieger/tautulli-remap/size)](https://github.com/cplieger/tautulli-remap/pkgs/container/tautulli-remap)
![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-blue)
![base: Distroless](https://img.shields.io/badge/base-Distroless_nonroot-4285F4?logo=google)
[![Go Report Card](https://goreportcard.com/badge/github.com/cplieger/tautulli-remap)](https://goreportcard.com/report/github.com/cplieger/tautulli-remap)
[![Test coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/tautulli-remap/badges/coverage.json)](https://github.com/cplieger/tautulli-remap/actions/workflows/coverage.yml)
[![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/tautulli-remap/badges/mutation.json)](https://github.com/cplieger/tautulli-remap/issues?q=label%3Agremlins-tracker)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13222/badge)](https://www.bestpractices.dev/projects/13222)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cplieger/tautulli-remap/badge)](https://scorecard.dev/viewer/?uri=github.com/cplieger/tautulli-remap)
[![SBOM](https://img.shields.io/badge/SBOM-SPDX-1D4ED8)](https://github.com/cplieger/tautulli-remap/releases)

Fix broken Tautulli watch history after reorganizing your Plex libraries.

## What it does

When you reorganize your Plex libraries (move files, re-add content, change folder structure), Plex assigns new internal IDs to your media. This breaks Tautulli's watch history — it can no longer link history entries to the right items. This tool automatically finds the correct new IDs and updates Tautulli's database, preserving your watch history and statistics.

For each stale entry, it attempts to find the correct current rating key in Plex using three matching strategies:

1. **GUID match** (primary) — matches on Plex's globally unique identifier, the most reliable method.
2. **Title+year match** (fallback) — when GUID matching fails, tries matching by title and release year.
3. **Title-only with media type guard** (optional) — last resort matching by title alone, restricted to the same media type to reduce false positives.

### Why this design

- **Three run modes** — `SCHEDULE_INTERVAL (e.g. "24h")` for a built-in timer, `SCHEDULE_INTERVAL=off` for resident-idle (stays healthy, awaits `docker exec ... tautulli-remap trigger`), or `tautulli-remap trigger` for a one-shot pass that exits 0/1.
- **Dry-run by default for safety** — no changes are applied until you explicitly set `DRY_RUN=false`, so you can always preview first.
- **Three matching strategies with increasing aggressiveness** — starts with the safest (GUID), falls back to title+year, and optionally title-only, giving you control over the risk/coverage tradeoff.
- **Stdlib-only, zero external dependencies** — pure Go with no third-party modules, minimizing supply-chain risk.
- **Distroless and rootless** — runs as `nonroot` on `gcr.io/distroless/static` with no shell or package manager.

## Quick start

Images are published to both `ghcr.io/cplieger/tautulli-remap` and `docker.io/cplieger/tautulli-remap` — use whichever you prefer.

```yaml
services:
  tautulli-remap:
    image: ghcr.io/cplieger/tautulli-remap:latest
    container_name: tautulli-remap
    restart: unless-stopped
    user: "1000:1000"  # match your host user

    environment:
      TZ: "Europe/Paris"
      TAUTULLI_URL: "http://tautulli:8181"
      TAUTULLI_APIKEY: "your-tautulli-apikey"
      PLEX_URL: "http://plex:32400"
      PLEX_TOKEN: "your-plex-token"
      SCHEDULE_INTERVAL: "24h"  # Go duration; "off" = resident-idle
      FALLBACK_TITLE_YEAR: "true"
      FALLBACK_TITLE_ONLY: "false"  # risk of false matches
      DRY_RUN: "true"  # set to false to apply changes
```

## Configuration reference

| Variable              | Description                                                                                                                                       | Default                | Required |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- | -------- |
| `TZ`                  | Container timezone                                                                                                                                | `Europe/Paris`         | No       |
| `TAUTULLI_URL`        | Tautulli instance URL (Docker DNS name or LAN IP)                                                                                                 | `http://tautulli:8181` | No       |
| `TAUTULLI_APIKEY`     | Tautulli API key (Settings → Web Interface → API Key)                                                                                             | -                      | Yes      |
| `PLEX_URL`            | Plex Media Server URL (Docker DNS name or LAN IP)                                                                                                 | `http://plex:32400`    | No       |
| `PLEX_TOKEN`          | Plex authentication token (see Plex support article)                                                                                              | -                      | Yes      |
| `SCHEDULE_INTERVAL`   | Go duration between remap runs (e.g. `24h`, `6h30m`). `off`/`disabled`/`0` = resident-idle (awaits external trigger via `tautulli-remap trigger`) | `off`                  | No       |
| `FALLBACK_TITLE_YEAR` | Try title+year matching when GUID match fails                                                                                                     | `true`                 | No       |
| `FALLBACK_TITLE_ONLY` | Try title-only matching as last resort (risk of false matches)                                                                                    | `false`                | No       |
| `DRY_RUN`             | Log what would change without applying — set to false to apply                                                                                    | `true`                 | No       |

## Subcommands

| Subcommand               | Description                                                                                                                  |
| ------------------------ | ---------------------------------------------------------------------------------------------------------------------------- |
| `tautulli-remap health`  | Checks the `/tmp/.healthy` marker file. Used as the Docker `HEALTHCHECK`. Exits 0 (healthy) or 1 (unhealthy).                |
| `tautulli-remap trigger` | Executes a single remap pass immediately. Exits 0 on success, 1 on failure. Designed for `docker exec` or Ofelia `job-exec`. |

### Recommended deployment with external scheduling

Use `SCHEDULE_INTERVAL=off` (resident-idle) with an external scheduler like Ofelia:

```yaml
services:
  tautulli-remap:
    image: ghcr.io/cplieger/tautulli-remap:latest
    environment:
      SCHEDULE_INTERVAL: "off"  # resident-idle, awaits trigger
      DRY_RUN: "false"
      # ... other env vars
    labels:
      ofelia.enabled: "true"
      ofelia.job-exec.tautulli-remap.schedule: "0 0 3 * * *"
      ofelia.job-exec.tautulli-remap.command: "tautulli-remap trigger"
```

This keeps the container healthy (passing healthchecks) while delegating scheduling to Ofelia — the recommended pattern for external scheduling.

## Healthcheck

The container includes a built-in Docker healthcheck via the `/tautulli-remap health` subcommand. After each scheduled run, the main process creates or removes a marker file at `/tmp/.healthy`; the health subcommand checks for this file's existence. The container reports unhealthy when Tautulli or Plex APIs are unreachable, return errors, or the remap logic fails — it recovers automatically on the next successful run (including runs where nothing needs remapping).

## Security

**No vulnerabilities found.** All scans clean across all scanners.

| Tool                                                                | Result                           |
| ------------------------------------------------------------------- | -------------------------------- |
| [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) | No vulnerabilities in call graph |
| [golangci-lint](https://golangci-lint.run/) (gosec, gocritic)       | 0 issues                         |
| [trivy](https://trivy.dev/)                                         | 0 vulnerabilities                |
| [grype](https://github.com/anchore/grype)                           | 0 vulnerabilities                |
| [gitleaks](https://github.com/gitleaks/gitleaks)                    | No secrets detected              |
| [semgrep](https://semgrep.dev/)                                     | 1 info (false positive)          |
| [hadolint](https://github.com/hadolint/hadolint)                    | Clean                            |

No network listener; connects outbound to Tautulli and Plex
only. Set `DRY_RUN=true` on first run to preview changes safely.
API tokens are never logged. Stdlib-only (zero external deps).
Runs as `nonroot` on a distroless base image with no shell,
under the hardened compose profile
(`read_only: true`, `cap_drop: [ALL]`,
`no-new-privileges:true`, 16 MB tmpfs for `/tmp`).

**Details for advanced users:** All HTTP clients use explicit
timeouts (2 min client, 30s per-request). Response bodies capped
via `io.LimitReader` (50 MB Tautulli, 100 MB Plex). Rating keys
validated as numeric before URL interpolation (prevents path
traversal). Plex token sent via `X-Plex-Token` header, not query
string. HTTP error messages sanitized to strip query parameters
(prevents API key leakage in logs). No `unsafe`, `reflect`,
`os/exec`, or file I/O beyond the health marker.

## Dependencies

All dependencies are updated automatically via [Renovate](https://github.com/renovatebot/renovate) and pinned by digest or version for reproducibility.

| Dependency               | Source                                                           |
| ------------------------ | ---------------------------------------------------------------- |
| golang                   | [Go](https://hub.docker.com/_/golang)                            |
| gcr.io/distroless/static | [Distroless](https://github.com/GoogleContainerTools/distroless) |

## Credits

This is an original tool that builds upon [Tautulli](https://github.com/Tautulli/Tautulli).
Inspired by [SwiftPanda16's Tautulli rating key update script](https://gist.github.com/JonnyWong16/f554f407832076919dc6864a78432db2).

## Contributing

Issues and pull requests are welcome. Please open an issue first for
larger changes so the approach can be discussed before implementation.

## Disclaimer

These images are built with care and follow security best practices, but they are intended for **homelab use**. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude Opus](https://www.anthropic.com/claude) and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
