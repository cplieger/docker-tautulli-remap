#!/bin/sh
# Runtime image smoke test for tautulli-remap. Invoked by the central CI docker job:
#   sh tests/image-smoke.sh <image-ref>
#
# Starts the assembled distroless image with its default ENTRYPOINT
# (["/tautulli-remap"], no subcommand), which lands in resident-idle mode:
# internal/config parseScheduleInterval defaults SCHEDULE_INTERVAL to "off" -> 0
# (getEnv("SCHEDULE_INTERVAL", "off")), so main() takes the ScheduleInterval == 0
# branch, sets the /tmp/.healthy marker (marker.Set(true)) and idles on
# <-ctx.Done(). Critically it does this WITHOUT any network call: config.Load()
# only reads env, and buildOrchestrator only constructs the http/plex/tautulli
# clients (plex.New / tautulli.New / orchestrator.New just store fields). The app
# dials Tautulli/Plex only on an external "trigger" (or in scheduled mode), so a
# resident-idle container reaches "healthy" with no live Tautulli or Plex.
#
# Waiting for the container's own HEALTHCHECK (["/tautulli-remap","health"],
# which probes the /tmp/.healthy marker) to report "healthy" therefore proves the
# shipped image genuinely runs: the static binary executes in the real distroless
# base, the nonroot user can write the /tmp marker, and the health-subcommand
# dispatch + HEALTHCHECK contract work end to end.
#
# config.Load() requires non-empty TAUTULLI_APIKEY and PLEX_TOKEN (requireSecret
# returns MissingEnvError -> main() logs and os.Exit(1) if either is unset/empty),
# so we pass dummy values; resident-idle never authenticates with them, so no real
# credentials and no live services are needed. SCHEDULE_INTERVAL=off is already
# the unset default but is passed explicitly so this test stays correct if that
# default ever changes.
set -eu

IMG="${1:?usage: image-smoke.sh <image-ref>}"
NAME="smoke-tautulli-remap-$$"
TIMEOUT=90 # covers the HEALTHCHECK start-period (15s) + a few 30s intervals

# shellcheck disable=SC2317,SC2329  # invoked indirectly via trap
cleanup() {
  code=$?
  # Dump container logs only on failure (a passing run stays quiet).
  if [ "$code" -ne 0 ]; then
    printf '%s\n' "--- container logs (tail) ---" >&2
    docker logs "$NAME" 2>&1 | tail -40 >&2 || true
  fi
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Dummy creds satisfy config.Load()'s requireSecret (must be non-empty);
# SCHEDULE_INTERVAL=off selects resident-idle (the default when unset).
docker run -d --name "$NAME" \
  -e TAUTULLI_APIKEY=dummy-smoke-key \
  -e PLEX_TOKEN=dummy-smoke-token \
  -e SCHEDULE_INTERVAL=off \
  "$IMG" >/dev/null

i=0
status=starting
while [ "$i" -lt "$TIMEOUT" ]; do
  # Fail fast on an early exit: poll .State.Running before the health status so
  # a crash-boot is caught by its exit code (more debuggable than "unhealthy")
  # and the verdict never depends on what health a stopped container reports.
  if [ "$(docker inspect --format '{{ .State.Running }}' "$NAME" 2>/dev/null || echo missing)" != "true" ]; then
    ec=$(docker inspect --format '{{ .State.ExitCode }}' "$NAME" 2>/dev/null || echo '?')
    printf 'FAIL: tautulli-remap container exited early (exit code %s)\n' "$ec" >&2
    exit 1
  fi
  status=$(docker inspect --format '{{ if .State.Health }}{{ .State.Health.Status }}{{ else }}no-healthcheck{{ end }}' "$NAME" 2>/dev/null || echo gone)
  case "$status" in
    healthy)
      printf 'tautulli-remap image smoke: ok (healthy after %ss)\n' "$i"
      exit 0
      ;;
    unhealthy)
      printf 'FAIL: tautulli-remap reported unhealthy\n' >&2
      exit 1
      ;;
    no-healthcheck)
      printf 'FAIL: image has no HEALTHCHECK to assert against\n' >&2
      exit 1
      ;;
    gone)
      printf 'FAIL: tautulli-remap container is gone\n' >&2
      exit 1
      ;;
  esac
  i=$((i + 1))
  sleep 1
done
printf 'FAIL: tautulli-remap did not become healthy within %ss (last status: %s)\n' "$TIMEOUT" "$status" >&2
exit 1
