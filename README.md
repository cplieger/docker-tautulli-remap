# docker-tautulli-remap

![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)
[![GitHub release](https://img.shields.io/github/v/release/cplieger/docker-tautulli-remap)](https://github.com/cplieger/docker-tautulli-remap/releases)
[![Image Size](https://ghcr-badge.egpl.dev/cplieger/tautulli-remap/size)](https://github.com/cplieger/docker-tautulli-remap/pkgs/container/tautulli-remap)
![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-blue)
![base: Distroless](https://img.shields.io/badge/base-Distroless_nonroot-4285F4?logo=google)

Tautulli rating key remapper for Plex library changes

## Overview

Periodically queries Tautulli's API for history entries with stale
rating keys (entries pointing to items that no longer exist in Plex).
For each stale entry, it attempts to find the correct current rating
key in Plex using three matching strategies: GUID match (primary),
title+year match (fallback), and title-only with media type guard
(optional). Supports dry-run mode for safe testing.

**Example use case:** After reorganizing Plex libraries, moving media
between folders, or re-adding content, Tautulli's history entries
break because Plex assigns new rating keys. This tool automatically
finds the correct new keys and updates Tautulli's database, preserving
your watch history and statistics.

This is a distroless, rootless container — it runs as `nonroot` on
`gcr.io/distroless/static` with no shell or package manager. It has
zero external Go dependencies (stdlib-only).


## Container Registries

This image is published to both GHCR and Docker Hub:

| Registry | Image |
|----------|-------|
| GHCR | `ghcr.io/cplieger/tautulli-remap` |
| Docker Hub | `docker.io/cplieger/tautulli-remap` |

```bash
# Pull from GHCR
docker pull ghcr.io/cplieger/tautulli-remap:latest

# Pull from Docker Hub
docker pull cplieger/tautulli-remap:latest
```

Both registries receive identical images and tags. Use whichever you prefer.

## Quick Start

```yaml
services:
  tautulli-remap:
    image: ghcr.io/cplieger/tautulli-remap:latest
    container_name: tautulli-remap
    restart: unless-stopped
    user: "1000:1000"  # match your host user
    mem_limit: 128m

    environment:
      TZ: "Europe/Paris"
      TAUTULLI_URL: "http://tautulli:8181"
      TAUTULLI_APIKEY: "your-tautulli-apikey"
      PLEX_URL: "http://plex:32400"
      PLEX_TOKEN: "your-plex-token"
      SCHEDULE_HOURS: "24"  # 0 = run once and exit
      FALLBACK_TITLE_YEAR: "true"
      FALLBACK_TITLE_ONLY: "false"  # risk of false matches
      DRY_RUN: "true"  # set to false to apply changes

    healthcheck:
      test:
        - CMD
        - /tautulli-remap
        - health
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 15s
```

## Deployment

1. Set `TAUTULLI_URL` and `PLEX_URL` to your instances (Docker DNS
   names like `http://tautulli:8181` work if on the same network).
2. Set `TAUTULLI_APIKEY` (found in Tautulli → Settings → Web
   Interface → API Key) and `PLEX_TOKEN`
   (see [Plex support](https://support.plex.tv/articles/204059436)).
3. Start with `DRY_RUN: "true"` (the default) to see what would
   change without applying anything. Check the container logs.
4. Once satisfied, set `DRY_RUN: "false"` to apply the remaps.
5. `FALLBACK_TITLE_YEAR` and `FALLBACK_TITLE_ONLY` control how
   aggressively the tool matches when GUID matching fails.
   Title-only matching risks false positives on common titles.

For additional configuration options not covered by this image's environment variables, refer to the [Tautulli documentation](https://github.com/Tautulli/Tautulli/wiki/Tautulli-API-Reference).

## Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `TZ` | Container timezone | `Europe/Paris` | No |
| `TAUTULLI_URL` | Tautulli instance URL (Docker DNS name or LAN IP) | `http://tautulli:8181` | No |
| `TAUTULLI_APIKEY` | Tautulli API key (Settings → Web Interface → API Key) | - | Yes |
| `PLEX_URL` | Plex Media Server URL (Docker DNS name or LAN IP) | `http://plex:32400` | No |
| `PLEX_TOKEN` | Plex authentication token (see Plex support article) | - | Yes |
| `SCHEDULE_HOURS` | Hours between remap runs (0 = run once and exit) | `24` | No |
| `FALLBACK_TITLE_YEAR` | Try title+year matching when GUID match fails | `true` | No |
| `FALLBACK_TITLE_ONLY` | Try title-only matching as last resort (risk of false matches) | `false` | No |
| `DRY_RUN` | Log what would change without applying — set to false to apply | `true` | No |


## Docker Healthcheck

The container includes a built-in Docker healthcheck. After each scheduled
run, the main process creates or removes a marker file at `/tmp/.healthy`.
The `health` subcommand checks for this file's existence.

**When it becomes unhealthy:**
- Tautulli API is unreachable (wrong URL, container down, network issue)
- Plex API is unreachable (wrong URL, invalid token, container down)
- API returns errors (invalid API key, rate limiting)

**When it recovers:**
- The next successful scheduled run (where both APIs respond and the remap logic completes) recreates the marker file. No restart required.
- A run where "nothing to remap" is found still counts as successful.

**On first boot:** The container runs immediately, then schedules
subsequent runs. If Tautulli or Plex aren't ready yet (e.g. still
starting), the first run fails and the container reports unhealthy
until the next scheduled run succeeds. Make use of depends_on to solve this.

To check health manually:
```bash
docker inspect --format='{{json .State.Health.Log}}' tautulli-remap | python3 -m json.tool
```

| Type | Command | Meaning |
|------|---------|---------|
| Docker | `/tautulli-remap health` | Exit 0 = last run completed successfully |


## Dependencies

All dependencies are updated automatically via [Renovate](https://github.com/renovatebot/renovate) and pinned by digest or version for reproducibility.

| Dependency | Version | Source |
|------------|---------|--------|
| golang | `1.26-alpine` | [Go](https://hub.docker.com/_/golang) |
| gcr.io/distroless/static-debian13 | `nonroot` | [Distroless](https://github.com/GoogleContainerTools/distroless) |

## Design Principles

- **Always up to date**: Base images, packages, and libraries are updated automatically via Renovate. Unlike many community Docker images that ship outdated or abandoned dependencies, these images receive continuous updates.
- **Minimal attack surface**: When possible, pure Go apps use `gcr.io/distroless/static:nonroot` (no shell, no package manager, runs as non-root). Apps requiring system packages use Alpine with the minimum necessary privileges.
- **Digest-pinned**: Every `FROM` instruction pins a SHA256 digest. All GitHub Actions are digest-pinned.
- **Multi-platform**: Built for `linux/amd64` and `linux/arm64`.
- **Healthchecks**: Every container includes a Docker healthcheck.
- **Provenance**: Build provenance is attested via GitHub Actions, verifiable with `gh attestation verify`.

## Contributing

Issues, suggestions, and pull requests are welcome.

## Credits

This is an original tool that builds upon [Tautulli](https://github.com/Tautulli/Tautulli).
Inspired by [SwiftPanda16's Tautulli rating key update script](https://gist.github.com/JonnyWong16/f554f407832076919dc6864a78432db2).

## Disclaimer

These images are built with care and follow security best practices, but they are intended for **homelab use**. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude Opus](https://www.anthropic.com/claude) and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
