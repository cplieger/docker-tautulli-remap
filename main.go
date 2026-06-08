package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	appconfig "github.com/cplieger/tautulli-remap/internal/config"
	"github.com/cplieger/tautulli-remap/internal/orchestrator"
	"github.com/cplieger/tautulli-remap/internal/plex"
	"github.com/cplieger/tautulli-remap/internal/tautulli"
)

// lastRunFile persists the timestamp of the last successful run.
const lastRunFile = "/tmp/.last_run"

// Compile-time interface satisfaction checks.
var (
	_ orchestrator.PlexClient     = (*plex.Client)(nil)
	_ orchestrator.TautulliClient = (*tautulli.Client)(nil)
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		runProbe(healthMarkerPath)
	}

	cfg, err := appconfig.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	appconfig.LogConfig(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	marker := newHealthMarker(healthMarkerPath)
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

	if cfg.ScheduleHours > 0 {
		orch.RunScheduler(ctx, marker.Set, readLastRun, writeLastRun)
		return
	}

	ok := orch.Run(ctx)
	marker.Set(ok)
	slog.Info("shutting down", "mode", "oneshot", "success", ok)
}

// readLastRun returns the timestamp of the last successful run, or the zero
// time if no marker exists or it is unparseable.
func readLastRun() time.Time {
	b, err := os.ReadFile(lastRunFile)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeLastRun records the current time as the last successful run.
func writeLastRun() {
	ts := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(lastRunFile, []byte(ts), 0o600); err != nil {
		slog.Debug("failed to write last-run marker", "path", lastRunFile, "error", err)
	}
}
