package orchestrator

import (
	"context"
	"log/slog"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/httpx"
	"github.com/cplieger/tautulli-remap/internal/config"
	"github.com/cplieger/tautulli-remap/internal/remap"
	"github.com/cplieger/tautulli-remap/internal/tautulli"
	"golang.org/x/sync/errgroup"
)

const (
	maxTautulliRecords     = 500_000
	maxConsecutiveFailures = 10
	staleCheckParallelism  = 8
)

// CircuitBreaker tracks consecutive failures and trips when the threshold is reached.
type CircuitBreaker struct {
	threshold   int
	consecutive int
}

// NewCircuitBreaker creates a breaker that trips after threshold consecutive failures.
func NewCircuitBreaker(threshold int) *CircuitBreaker {
	return &CircuitBreaker{threshold: threshold}
}

// Record records a success (ok=true) or failure (ok=false). Returns true if the breaker has tripped.
func (cb *CircuitBreaker) Record(ok bool) bool {
	if ok {
		cb.consecutive = 0
		return false
	}
	cb.consecutive++
	return cb.consecutive >= cb.threshold
}

// defaultPaginationDelay is the default pause between history pages.
const defaultPaginationDelay = 500 * time.Millisecond

// PlexClient defines the interface for Plex API interactions, shaped by
// what the orchestrator needs.
type PlexClient interface {
	ItemExists(ctx context.Context, ratingKey string) bool
	LibrarySections(ctx context.Context) []remap.Section
	LibraryAll(ctx context.Context, sectionKey string) []remap.LibItem
}

// TautulliClient defines the interface for Tautulli API interactions, shaped
// by what the orchestrator needs.
type TautulliClient interface {
	API(ctx context.Context, cmd string, params url.Values) ([]byte, error)
	APIWithRetry(ctx context.Context, cmd string, params url.Values) ([]byte, error)
	GetHistory(ctx context.Context, params url.Values) (*tautulli.HistoryPage, error)
	UpdateMetadata(ctx context.Context, oldKey, newKey string, mediaType remap.MediaType) error
	DeleteRecentlyAdded(ctx context.Context) error
}

// Orchestrator coordinates the remap workflow.
type Orchestrator struct {
	plex     PlexClient
	tautulli TautulliClient
	cfg      *config.Config

	// PaginationDelay is the pause between history pages. Zero uses the default.
	PaginationDelay time.Duration
}

// New creates a new Orchestrator.
func New(p PlexClient, t TautulliClient, cfg *config.Config) *Orchestrator {
	return &Orchestrator{plex: p, tautulli: t, cfg: cfg}
}

// Run executes the remap workflow. Returns true on success.
func (o *Orchestrator) Run(ctx context.Context) bool {
	if o.cfg.DryRun {
		slog.Info("dry run enabled, skipping backup")
	} else {
		slog.Info("creating Tautulli backup")
		if _, err := o.tautulli.APIWithRetry(ctx, "backup_db", nil); err != nil {
			slog.Error("backup failed after retries", "error", err)
		}
	}

	// Step 1: Collect items from Tautulli history
	slog.Info("step 1: collecting items from Tautulli history")
	tautulliItems, guidDropped := o.CollectTautulliItems(ctx)
	if tautulliItems == nil {
		return false
	}
	slog.Info("step 1 done",
		"unique_items", len(tautulliItems),
		"episode_guids_dropped", guidDropped)
	if ctx.Err() != nil {
		slog.Info("run cancelled after history collection", "cause", context.Cause(ctx))
		return false
	}

	// Step 2: Find stale keys
	slog.Info("step 2: checking keys against Plex")
	stale := o.FindStaleKeys(ctx, tautulliItems)
	slog.Info("step 2 done", "stale", len(stale), "total", len(tautulliItems))
	if ctx.Err() != nil {
		slog.Info("run cancelled during stale check", "cause", context.Cause(ctx))
		return false
	}

	if len(stale) == 0 {
		slog.Info("all rating keys are valid, nothing to remap")
		slog.Info("scan complete",
			"total", len(tautulliItems),
			"stale", 0,
			"matched", 0,
			"unmatched", 0,
			"updated", 0,
			"failed", 0,
			"dry_run", o.cfg.DryRun)
		return true
	}

	// Step 3: Build Plex library index
	slog.Info("step 3: building Plex library index")
	byGUID, byTitleYear, byTitle := remap.BuildPlexIndex(ctx, o.plex, staleCheckParallelism)
	if ctx.Err() != nil {
		slog.Info("run cancelled during plex indexing", "cause", context.Cause(ctx))
		return false
	}
	if len(byGUID) == 0 && len(byTitleYear) == 0 && len(byTitle) == 0 {
		slog.Error("Plex library index is empty, cannot match stale items")
		return false
	}

	// Step 4: Match stale items
	slog.Info("step 4: matching stale items")
	matched, unmatched := remap.MatchStaleItems(stale, byGUID, byTitleYear, byTitle,
		o.cfg.FallbackTitleYear, o.cfg.FallbackTitleOnly)

	// Step 5: Apply remappings
	updated, failed := o.ApplyRemappings(ctx, matched, unmatched)

	// Step 6: Clear recently added
	if updated > 0 {
		o.ClearRecentlyAdded(ctx)
	} else {
		slog.Info("skipping clear recently added", "reason", "no_updates")
	}

	slog.Info("scan complete",
		"total", len(tautulliItems),
		"stale", len(stale),
		"matched", len(matched),
		"unmatched", len(unmatched),
		"updated", updated,
		"failed", failed,
		"dry_run", o.cfg.DryRun)

	return failed == 0 || updated > 0
}

// RunScheduler implements the long-running scheduled mode. The setHealthy
// callback controls the health marker.
func (o *Orchestrator) RunScheduler(ctx context.Context, setHealthy func(bool)) {
	interval := o.cfg.ScheduleInterval
	slog.Info("scheduled mode", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	setHealthy(true)

	failures := 0
	doRun := func() {
		ok := o.Run(ctx)
		if ok {
			failures = 0
			setHealthy(true)
			slog.Info("run complete",
				"next_run_at", time.Now().Add(interval).UTC().Format(time.RFC3339))
			return
		}
		failures++
		slog.Warn("run failed",
			"consecutive_failures", failures,
			"retry_in", interval)
		if failures >= 3 {
			setHealthy(false)
		}
	}

	doRun()

	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down", "mode", "scheduled", "cause", context.Cause(ctx))
			return
		case <-ticker.C:
			doRun()
		}
	}
}

func (o *Orchestrator) CollectTautulliItems(ctx context.Context) (items map[string]remap.TautulliEntry, guidDropped int) {
	items = map[string]remap.TautulliEntry{}
	start := 0
	total := -1
	processed := 0

	for {
		params := url.Values{
			"grouping":         {"0"},
			"include_activity": {"0"},
			"media_type":       {"movie,episode"},
			"order_column":     {"date"},
			"order_dir":        {"desc"},
			"start":            {strconv.Itoa(start)},
			"length":           {"1000"},
		}

		page, err := o.tautulli.GetHistory(ctx, params)
		if err != nil {
			slog.Error("failed to get history", "error", err)
			return nil, 0
		}

		if total < 0 {
			total = page.RecordsFiltered
			slog.Info("total history records", "count", total)
			if total > maxTautulliRecords {
				slog.Error("history record count exceeds sanity cap",
					"count", total, "max", maxTautulliRecords)
				return nil, 0
			}
		}

		if len(page.Rows) == 0 {
			break
		}

		for i := range page.Rows {
			if remap.ProcessHistoryRow(&page.Rows[i], items) {
				guidDropped++
			}
		}
		processed += len(page.Rows)

		start += 1000
		if start >= total {
			break
		}
		slog.Info("progress", "processed", processed, "total", total, "unique_keys", len(items))
		if err := httpx.SleepCtx(ctx, o.paginationDelay()); err != nil {
			return nil, 0
		}
	}

	return items, guidDropped
}

func (o *Orchestrator) FindStaleKeys(ctx context.Context, items map[string]remap.TautulliEntry) map[string]remap.TautulliEntry {
	var mu sync.Mutex
	stale := map[string]remap.TautulliEntry{}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(staleCheckParallelism)

	for key, item := range items {
		g.Go(func() error {
			if gctx.Err() != nil {
				return nil
			}
			if !o.plex.ItemExists(gctx, key) {
				mu.Lock()
				stale[key] = item
				mu.Unlock()
			}
			return nil
		})
	}

	_ = g.Wait()

	slog.Info("stale key check complete", "checked", len(items), "stale", len(stale))
	return stale
}

func (o *Orchestrator) ApplyRemappings(
	ctx context.Context,
	matched []remap.MatchResult,
	unmatched []remap.UnmatchResult,
) (updated, failed int) {
	if len(unmatched) > 0 {
		slog.Info("unmatched items", "count", len(unmatched))
		for _, u := range unmatched {
			slog.Warn("no match",
				"title", u.Title, "year", u.Year, "type", u.MediaType, "key", u.OldKey)
		}
	}

	if len(matched) == 0 {
		slog.Info("no matches found for stale items")
		return 0, 0
	}

	if o.cfg.DryRun {
		slog.Info("dry run: would remap items", "count", len(matched))
	} else {
		slog.Info("remapping items", "count", len(matched))
	}

	perItemLevel := slog.LevelInfo
	if o.cfg.DryRun {
		perItemLevel = slog.LevelDebug
	}

	breaker := NewCircuitBreaker(maxConsecutiveFailures)
	for i, m := range matched {
		slog.Log(ctx, perItemLevel, "remap",
			"title", m.Title, "year", m.Year, "type", m.MediaType,
			"old_key", m.OldKey, "new_key", m.NewKey,
			"method", m.Method, "dry_run", o.cfg.DryRun)
		if o.cfg.DryRun {
			continue
		}
		if ctx.Err() != nil {
			slog.Warn("remapping interrupted", "remaining", len(matched)-i)
			break
		}
		if err := o.tautulli.UpdateMetadata(ctx, m.OldKey, m.NewKey, m.MediaType); err != nil {
			slog.Error("remap failed",
				"title", m.Title, "old_key", m.OldKey, "error", err)
			failed++
			if breaker.Record(false) {
				slog.Error("aborting remap phase",
					"reason", "too_many_consecutive_failures",
					"consecutive", breaker.consecutive,
					"remaining", len(matched)-i-1)
				return updated, failed
			}
			continue
		}
		updated++
		breaker.Record(true)
	}
	return updated, failed
}

func (o *Orchestrator) ClearRecentlyAdded(ctx context.Context) {
	if o.cfg.DryRun {
		slog.Info("(dry run) would clear recently added items")
		return
	}
	slog.Info("clearing recently added items")
	if err := o.tautulli.DeleteRecentlyAdded(ctx); err != nil {
		slog.Error("failed to clear recently added", "error", err)
	}
}

// paginationDelay returns the configured pagination delay or the default.
func (o *Orchestrator) paginationDelay() time.Duration {
	if o.PaginationDelay > 0 {
		return o.PaginationDelay
	}
	return defaultPaginationDelay
}
