// Package main is the entry point for tautulli-remap, a tool that repairs
// Tautulli watch history after Plex library reorganizations by finding and
// updating stale rating keys.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embed the IANA tz database so TZ (default Europe/Paris) is honored even
	// though the distroless static base ships no /usr/share/zoneinfo; without
	// it time.Local silently falls back to UTC.
	_ "time/tzdata"

	"github.com/cplieger/health"
	appconfig "github.com/cplieger/tautulli-remap/internal/config"
	"github.com/cplieger/tautulli-remap/internal/orchestrator"
	"github.com/cplieger/tautulli-remap/internal/plex"
	"github.com/cplieger/tautulli-remap/internal/tautulli"
)

// Compile-time interface satisfaction checks.
var (
	_ orchestrator.PlexClient     = (*plex.Client)(nil)
	_ orchestrator.TautulliClient = (*tautulli.Client)(nil)
	_ health.Signal               = (*health.Marker)(nil)
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "health":
			health.RunProbe(health.DefaultPath)
		case "trigger":
			runTrigger()
		default:
			slog.Error("unknown subcommand", "arg", os.Args[1], "valid", "health, trigger")
			os.Exit(2)
		}
	}

	cfg, err := appconfig.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	appconfig.LogConfig(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	marker := health.NewMarker(health.DefaultPath)
	marker.Set(false)
	defer marker.Cleanup()

	orch := buildOrchestrator(cfg)

	if cfg.ScheduleInterval > 0 {
		orch.RunScheduler(ctx, marker.Set)
		return
	}

	// Resident-idle mode: no internal timer, wait for external triggers via
	// "docker exec ... tautulli-remap trigger". Healthy while idle.
	marker.Set(true)
	slog.Info("resident-idle mode", "reason", "SCHEDULE_INTERVAL=off, awaiting external trigger")
	<-ctx.Done()
	slog.Info("shutting down", "mode", "resident-idle", "cause", context.Cause(ctx))
}

// runTrigger executes a single remap pass and exits. This is the target for
// external schedulers (Ofelia job-exec, cron, etc.). The os.Exit lives here,
// free of pending defers; doTrigger holds the defers and returns a code.
func runTrigger() {
	os.Exit(doTrigger())
}

// doTrigger runs one remap pass and returns the process exit code.
func doTrigger() int {
	cfg, err := appconfig.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		return 1
	}
	appconfig.LogConfig(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// NOTE: no marker.Cleanup() here. In the homelab the resident-idle main
	// process owns /tmp/.healthy; this trigger runs as a separate `docker exec`
	// against the same file, so it only updates the marker to reflect the run's
	// outcome — deleting it would mark the resident container unhealthy.
	marker := health.NewMarker(health.DefaultPath)

	orch := buildOrchestrator(cfg)

	ok := orch.Run(ctx)
	return finishTrigger(ctx, ok, marker.Set)
}

// finishTrigger records a completed trigger run's outcome and returns the
// process exit code. It checks ctx.Err() FIRST: a graceful shutdown (parent
// context cancelled, e.g. SIGTERM landing mid-run) is not a failure, so it logs
// an Info and returns 0 without touching the health marker — mirroring
// RunScheduler.doRun, which does not count a shutdown-interrupted run toward its
// failure threshold. Checking the context before Run's bool also makes the exit
// code deterministic: a signal arriving during the first UpdateMetadata can
// otherwise make Run return either true or false depending on timing. Only when
// the context is still live does Run's bool decide the result — success marks
// the resident process healthy and returns 0; failure leaves the marker
// untouched (this trigger runs as a separate `docker exec` against the resident
// process's marker, so flipping it here would misreport the resident container)
// and signals failure via exit code 1.
func finishTrigger(ctx context.Context, ok bool, setHealthy func(bool)) int {
	if ctx.Err() != nil {
		slog.Info("trigger interrupted by shutdown", "cause", context.Cause(ctx))
		return 0
	}
	if ok {
		setHealthy(true)
	} else {
		slog.Warn("trigger run failed; health marker left unchanged, failure signalled via exit code")
	}
	slog.Info("shutting down", "mode", "trigger", "success", ok)
	if !ok {
		return 1
	}
	return 0
}

// buildOrchestrator constructs the shared HTTP client and the Plex,
// Tautulli, and Orchestrator instances from cfg.
func buildOrchestrator(cfg *appconfig.Config) *orchestrator.Orchestrator {
	httpClient := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	plexClient := plex.New(cfg.PlexURL, cfg.PlexToken, httpClient)
	tautulliClient := tautulli.New(cfg.TautulliURL, cfg.TautulliAPIKey, httpClient)
	return orchestrator.New(plexClient, tautulliClient, cfg)
}
