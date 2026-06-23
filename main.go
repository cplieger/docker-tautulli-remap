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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "health":
			health.RunProbe(health.DefaultPath)
		case "trigger":
			runTrigger()
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

	httpClient := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	plexClient := plex.New(cfg.PlexURL, cfg.PlexToken, httpClient)
	tautulliClient := tautulli.New(cfg.TautulliURL, cfg.TautulliAPIKey, httpClient)
	orch := orchestrator.New(plexClient, tautulliClient, cfg)

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

	httpClient := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	plexClient := plex.New(cfg.PlexURL, cfg.PlexToken, httpClient)
	tautulliClient := tautulli.New(cfg.TautulliURL, cfg.TautulliAPIKey, httpClient)
	orch := orchestrator.New(plexClient, tautulliClient, cfg)

	ok := orch.Run(ctx)
	marker.Set(ok)
	slog.Info("shutting down", "mode", "trigger", "success", ok)
	if !ok {
		return 1
	}
	return 0
}
