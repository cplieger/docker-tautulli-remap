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

	"github.com/cplieger/health"
	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/slogx"
	appconfig "github.com/cplieger/tautulli-remap/internal/config"
	"github.com/cplieger/tautulli-remap/internal/orchestrator"
	"github.com/cplieger/tautulli-remap/internal/plex"
	"github.com/cplieger/tautulli-remap/internal/tautulli"
)

// Compile-time interface satisfaction checks.
var (
	_ orchestrator.PlexClient     = (*plex.Client)(nil)
	_ orchestrator.TautulliClient = (*tautulli.Client)(nil)
)

func main() {
	slogx.Setup(slogx.Options{Level: slog.LevelInfo})

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "health":
			// Scheduled mode arms a freshness deadline: the loop refreshes
			// the marker each pass, so a marker older than 3 intervals means
			// a wedged loop and a restart fixes it. Resident-idle mode
			// (interval 0) disables the deadline (WithMaxAge(0) is a no-op):
			// an idle resident between external triggers is healthy.
			health.RunProbe(health.DefaultPath,
				health.WithMaxAge(3*appconfig.RemapInterval()))
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
	cfg.Log()

	orch, err := buildOrchestrator(cfg)
	if err != nil {
		slog.Error("failed to build Plex client", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	marker := health.NewMarker(health.DefaultPath)
	marker.Set(false)
	defer marker.Cleanup()

	if cfg.RemapInterval > 0 {
		orch.RunScheduler(ctx, marker.Set)
		return
	}

	// Resident-idle mode: no internal timer, wait for external triggers via
	// "docker exec ... tautulli-remap trigger". Healthy while idle.
	marker.Set(true)
	slog.Info("resident-idle mode", "reason", "REMAP_INTERVAL=off, awaiting external trigger")
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
	cfg.Log()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// NOTE: no marker.Cleanup() here. In resident-idle mode the main
	// process owns /tmp/.healthy; this trigger runs as a separate `docker exec`
	// against the same file, so it only updates the marker to reflect the run's
	// outcome — deleting it would mark the resident container unhealthy.
	marker := health.NewMarker(health.DefaultPath)

	orch, err := buildOrchestrator(cfg)
	if err != nil {
		slog.Error("failed to build Plex client", "error", err)
		return 1
	}

	ok := orch.Run(ctx)
	return finishTrigger(ctx, ok, marker.Set)
}

// exitInterrupted is the trigger's exit code for a pass interrupted by
// shutdown before completing: distinct from 0 (an interrupted pass did not
// verifiably finish its work, so recording success would be a lie to the
// external scheduler) and from 1 (nothing failed either — the pass is simply
// incomplete and safe to re-run, since passes are idempotent). Exit code 2 is
// taken by the unknown-subcommand usage error.
const exitInterrupted = 3

// finishTrigger records a completed trigger run's outcome and returns the
// process exit code. It checks ctx.Err() FIRST: a graceful shutdown (parent
// context cancelled, e.g. SIGTERM landing mid-run) means the pass did not run
// to completion, so it logs an Info, leaves the health marker untouched, and
// returns exitInterrupted — the retryable "incomplete, not failed" signal for
// the external scheduler (RunScheduler.doRun treats an interrupted run the
// same way: its own third outcome, neither success nor a counted failure).
// Checking the context before Run's bool also makes the exit code
// deterministic: a signal arriving during the first UpdateMetadata can
// otherwise make Run return either true or false depending on timing. Only
// when the context is still live does Run's bool decide the result — success
// marks the resident process healthy and returns 0; failure leaves the marker
// untouched (this trigger runs as a separate `docker exec` against the
// resident process's marker, so flipping it here would misreport the resident
// container) and signals failure via exit code 1.
func finishTrigger(ctx context.Context, ok bool, setHealthy func(bool)) int {
	if ctx.Err() != nil {
		slog.Info("trigger interrupted by shutdown; pass incomplete", "cause", context.Cause(ctx))
		return exitInterrupted
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

// buildOrchestrator constructs the Plex, Tautulli, and Orchestrator
// instances from cfg. The Plex client (plexapi) owns its own hardened
// transport; Tautulli keeps the app-built one (2-minute total budget,
// refuse-all redirects so the API key never rides a hostile 3xx).
func buildOrchestrator(cfg *appconfig.Config) (*orchestrator.Orchestrator, error) {
	plexClient, err := plex.New(cfg.PlexURL, plex.Token(cfg.PlexToken))
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Timeout:       tautulli.ClientTimeout,
		CheckRedirect: httpx.RefuseAllRedirects,
	}
	tautulliClient := tautulli.New(cfg.TautulliURL, cfg.TautulliAPIKey, httpClient)
	return orchestrator.New(plexClient, tautulliClient, cfg), nil
}
