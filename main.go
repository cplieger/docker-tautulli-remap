package main

// Inspired by SwiftPanda16's Tautulli metadata update script:
// https://gist.github.com/JonnyWong16/f554f407832076919dc6864a78432db2
//
// That script matches by GUID, which misses items where the GUID changed
// after a library rebuild. This version combines both approaches:
// 1. GUID-based matching (IMDB/TMDB/TVDB/Plex) as primary strategy
// 2. Title+year fallback for items without usable GUIDs
// 3. Title-only fallback as last resort
// It also checks if rating keys still exist in Plex to avoid unnecessary work.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// --- Constants ---

const resultSuccess = "success"

// Match method identifiers used in matchStaleItems results.
const (
	methodGUID      = "guid"
	methodTitleYear = "title+year"
	mediaMovie      = "movie"
	mediaShow       = "show"
)

// healthFile is touched on startup and removed on shutdown.
// The "health" subcommand checks its existence for Docker healthchecks
// without requiring an HTTP server or open port.
const healthFile = "/tmp/.healthy"

// Response body size limits to prevent OOM on unexpected payloads.
const (
	maxTautulliBody = 50 << 20  // 50 MB — Tautulli history can be large
	maxPlexBody     = 100 << 20 // 100 MB — large Plex libraries
	maxPlexSections = 10 << 20  // 10 MB — library sections list
)

// Delay durations — package-level vars so tests can override for speed.
var (
	retryDelayUnit  = 5 * time.Second        // multiplied by attempt number in tautulliAPIWithRetry
	paginationDelay = 500 * time.Millisecond // pause between history pages
)

// --- Configuration ---

type config struct {
	TautulliURL       string
	TautulliAPIKey    string
	PlexURL           string
	PlexToken         string
	ScheduleHours     int
	DryRun            bool
	FallbackTitleYear bool
	FallbackTitleOnly bool
}

// --- Data types ---

type historyResponse struct {
	Response struct {
		Result  string `json:"result"`
		Message string `json:"message"`
		Data    struct {
			Data            []historyItem `json:"data"`
			RecordsFiltered int           `json:"recordsFiltered"`
		} `json:"data"`
	} `json:"response"`
}

type historyItem struct {
	RatingKey            any    `json:"rating_key"`
	Year                 any    `json:"year"`
	GrandparentRatingKey any    `json:"grandparent_rating_key"`
	Title                string `json:"title"`
	GrandparentTitle     string `json:"grandparent_title"`
	MediaType            string `json:"media_type"`
	GUID                 string `json:"guid"`
}

// tautulliEntry represents a unique item from Tautulli history.
type tautulliEntry struct {
	RatingKey string
	Title     string
	Year      string
	MediaType string // "movie" or "show"
	GUID      string // normalized GUID (e.g. "imdb://tt1234567")
}

type plexEntry struct {
	RatingKey string
	Title     string
	Year      string
	Type      string   // "movie" or "show"
	GUIDs     []string // all normalized GUIDs
}

type matchResult struct {
	Title, Year, OldKey, NewKey, MediaType, Method string
}

type unmatchResult struct {
	Title, Year, OldKey, MediaType string
}

// --- GUID normalization ---

// guidMapping maps a source prefix to its canonical scheme.
type guidMapping struct {
	source    string
	canonical string
	stripPath bool // strip everything after first "/" in the ID
}

// guidMappings defines the known GUID prefix transformations.
// Longer prefixes come first to avoid partial matches
// (e.g. "themoviedb://" before "tmdb://").
var guidMappings = [...]guidMapping{
	{"themoviedb://", "tmdb://", false},
	{"thetvdb://", "tvdb://", true},
	{"imdb://", "imdb://", false},
	{"tmdb://", "tmdb://", false},
	{"tvdb://", "tvdb://", false},
	{"mbid://", "mbid://", false},
	{"plex://", "plex://", false},
}

// --- Main ---

func main() {
	// CLI health probe for Docker healthcheck (distroless has no curl/wget).
	// Checks for a marker file instead of making an HTTP request — no port needed.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		if _, err := os.Stat(healthFile); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg := loadConfig()
	logConfig(&cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Remove stale health file from a previous run that may have crashed
	// before its defer ran. Without this, the health probe would report
	// healthy before the first run completes.
	setHealthy(false)

	// Health file removed on exit; created/removed based on run() results.
	defer setHealthy(false)

	if cfg.ScheduleHours > 0 {
		slog.Info("scheduled mode", "interval_hours", cfg.ScheduleHours)
		ticker := time.NewTicker(time.Duration(cfg.ScheduleHours) * time.Hour)
		defer ticker.Stop()

		setHealthy(run(ctx, &cfg))
		for {
			select {
			case <-ctx.Done():
				slog.Info("shutting down", "cause", context.Cause(ctx))
				return
			case <-ticker.C:
				setHealthy(run(ctx, &cfg))
			}
		}
	}

	setHealthy(run(ctx, &cfg))
}

// --- Health ---

// setHealthy creates or removes the health marker file.
func setHealthy(ok bool) {
	if ok {
		if f, err := os.Create(healthFile); err == nil {
			f.Close()
		}
	} else {
		os.Remove(healthFile)
	}
}

// --- Environment ---

func loadConfig() config {
	hours, err := strconv.Atoi(getEnv("SCHEDULE_HOURS", "0"))
	if err != nil {
		hours = 0
	}
	return config{
		TautulliURL:       getEnv("TAUTULLI_URL", "http://tautulli:8181"),
		TautulliAPIKey:    requireEnv("TAUTULLI_APIKEY"),
		PlexURL:           getEnv("PLEX_URL", "http://plex:32400"),
		PlexToken:         requireEnv("PLEX_TOKEN"),
		ScheduleHours:     hours,
		DryRun:            !strings.EqualFold(getEnv("DRY_RUN", "true"), "false"),
		FallbackTitleYear: strings.EqualFold(getEnv("FALLBACK_TITLE_YEAR", "true"), "true"),
		FallbackTitleOnly: strings.EqualFold(getEnv("FALLBACK_TITLE_ONLY", "false"), "true"),
	}
}

// logConfig logs the active configuration at startup (secrets redacted).
func logConfig(cfg *config) {
	slog.Info("configuration loaded",
		"tautulli_url", cfg.TautulliURL,
		"tautulli_apikey", "configured",
		"plex_url", cfg.PlexURL,
		"plex_token", "configured",
		"dry_run", cfg.DryRun,
		"fallback_title_year", cfg.FallbackTitleYear,
		"fallback_title_only", cfg.FallbackTitleOnly,
		"schedule_hours", cfg.ScheduleHours,
	)
}

// normalizeGUID extracts a canonical ID from a Plex GUID string.
// Returns empty string for unsupported formats (local://, com.plexapp.agents.none://).
func normalizeGUID(guid string) string {
	for _, m := range guidMappings {
		if !strings.Contains(guid, m.source) {
			continue
		}
		id := extractAfter(guid, m.source)
		if m.stripPath {
			if i := strings.Index(id, "/"); i >= 0 {
				id = id[:i]
			}
		}
		return m.canonical + id
	}
	return ""
}

// extractAfter returns the part after the prefix, trimming query params.
func extractAfter(s, prefix string) string {
	_, after, found := strings.Cut(s, prefix)
	if !found {
		return ""
	}
	if i := strings.Index(after, "?"); i >= 0 {
		after = after[:i]
	}
	return after
}

// --- Core logic ---

func run(ctx context.Context, cfg *config) bool {
	client := &http.Client{Timeout: 2 * time.Minute}

	if cfg.DryRun {
		slog.Info("dry run enabled, skipping backup")
	} else {
		slog.Info("creating Tautulli backup")
		if _, err := tautulliAPI(ctx, client, cfg, "backup_db", nil); err != nil {
			slog.Error("backup failed", "error", err)
		}
	}

	// Step 1: Collect items from Tautulli history
	slog.Info("step 1: collecting items from Tautulli history")
	tautulliItems := collectTautulliItems(ctx, client, cfg)
	if tautulliItems == nil {
		return false
	}
	slog.Info("step 1 done", "unique_items", len(tautulliItems))

	// Step 2: Find stale keys (no longer exist in Plex)
	slog.Info("step 2: checking keys against Plex")
	stale := findStaleKeys(ctx, client, cfg, tautulliItems)
	slog.Info("step 2 done", "stale", len(stale), "total", len(tautulliItems))

	if len(stale) == 0 {
		slog.Info("all rating keys are valid, nothing to remap")
		return true
	}

	// Step 3: Build Plex library index (GUIDs + title/year)
	slog.Info("step 3: building Plex library index")
	plexByGUID, plexByTitleYear, plexByTitle := buildPlexIndex(ctx, client, cfg)

	// Step 4: Match stale items using GUID first, then title+year, then title
	slog.Info("step 4: matching stale items")
	matched, unmatched := matchStaleItems(cfg, stale, plexByGUID, plexByTitleYear, plexByTitle)

	// Step 5: Apply remappings
	applyRemappings(ctx, client, cfg, matched, unmatched)

	// Step 6: Clear recently added
	clearRecentlyAdded(ctx, client, cfg)

	slog.Info("done")
	return true
}

func collectTautulliItems(ctx context.Context, client *http.Client, cfg *config) map[string]tautulliEntry {
	items := map[string]tautulliEntry{} // ratingKey -> entry
	start := 0
	total := -1

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

		body, err := tautulliAPIWithRetry(ctx, client, cfg, "get_history", params)
		if err != nil {
			slog.Error("failed to get history", "error", err)
			return nil
		}

		var resp historyResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			slog.Error("failed to parse history", "error", err)
			return nil
		}

		if resp.Response.Result != resultSuccess {
			slog.Error("API error", "message", resp.Response.Message)
			return nil
		}

		if total < 0 {
			total = resp.Response.Data.RecordsFiltered
			slog.Info("total history records", "count", total)
		}

		rows := resp.Response.Data.Data
		if len(rows) == 0 {
			break
		}

		for i := range rows {
			processHistoryRow(&rows[i], items)
		}

		start += 1000
		if start >= total {
			break
		}
		slog.Info("progress", "processed", start, "total", total, "unique_keys", len(items))
		// Pace requests to avoid overwhelming Tautulli
		timer := time.NewTimer(paginationDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}

	return items
}

// processHistoryRow extracts a tautulliEntry from a single history row
// and inserts it into items (keyed by rating key). Skips rows with
// unparseable or zero rating keys.
func processHistoryRow(row *historyItem, items map[string]tautulliEntry) {
	year := strconv.Itoa(toInt(row.Year))
	guid := normalizeGUID(row.GUID)

	switch row.MediaType {
	case mediaMovie:
		ratingKey := toInt(row.RatingKey)
		if ratingKey <= 0 {
			return
		}
		key := strconv.Itoa(ratingKey)
		if _, ok := items[key]; !ok {
			items[key] = tautulliEntry{
				RatingKey: key, Title: row.Title,
				Year: year, MediaType: mediaMovie, GUID: guid,
			}
		}
	case "episode":
		grandparentRatingKey := toInt(row.GrandparentRatingKey)
		if grandparentRatingKey <= 0 {
			return
		}
		key := strconv.Itoa(grandparentRatingKey)
		if _, ok := items[key]; !ok {
			title := row.GrandparentTitle
			if title == "" {
				title = row.Title
			}
			showGUID := guid
			// Episode-level plex:// GUIDs don't help match shows
			if strings.Contains(showGUID, "plex://episode/") {
				showGUID = ""
			}
			items[key] = tautulliEntry{
				RatingKey: key, Title: title,
				Year: year, MediaType: mediaShow, GUID: showGUID,
			}
		}
	}
}

func findStaleKeys(ctx context.Context, client *http.Client, cfg *config, items map[string]tautulliEntry) map[string]tautulliEntry {
	stale := map[string]tautulliEntry{}
	checked := 0

	for key, item := range items {
		if ctx.Err() != nil {
			slog.Warn("stale key check interrupted", "checked", checked, "total", len(items))
			return stale
		}
		checked++
		if !plexItemExists(ctx, client, cfg, key) {
			stale[key] = item
		}
		if checked%200 == 0 {
			slog.Info("progress", "checked", checked, "total", len(items), "stale", len(stale))
		}
	}

	return stale
}

func buildPlexIndex(ctx context.Context, client *http.Client, cfg *config) (
	byGUID map[string]plexEntry,
	byTitleYear map[string]plexEntry,
	byTitle map[string]plexEntry,
) {
	byGUID = map[string]plexEntry{}
	byTitleYear = map[string]plexEntry{}
	byTitle = map[string]plexEntry{}

	sections := plexLibrarySections(ctx, client, cfg)
	for _, sec := range sections {
		if ctx.Err() != nil {
			break
		}
		if sec.Type != mediaMovie && sec.Type != mediaShow {
			continue
		}
		slog.Info("scanning library", "title", sec.Title)
		libItems := plexLibraryAll(ctx, client, cfg, sec.Key)
		for _, li := range libItems {
			rk := strconv.Itoa(li.RatingKey)
			y := strconv.Itoa(li.Year)
			entry := plexEntry{
				RatingKey: rk, Title: li.Title,
				Year: y, Type: sec.Type, GUIDs: li.GUIDs,
			}

			for _, g := range li.GUIDs {
				byGUID[g] = entry
			}

			t := strings.ToLower(strings.TrimSpace(li.Title))
			byTitleYear[t+"|"+y] = entry
			byTitle[t] = entry
		}
	}

	return byGUID, byTitleYear, byTitle
}

// --- Matching ---

func matchStaleItems(
	cfg *config,
	stale map[string]tautulliEntry,
	byGUID, byTitleYear, byTitle map[string]plexEntry,
) ([]matchResult, []unmatchResult) {
	var matched []matchResult
	var unmatched []unmatchResult

	for oldKey, item := range stale {
		var newKey, method string

		// Strategy 1: Match by GUID
		if item.GUID != "" {
			if pe, ok := byGUID[item.GUID]; ok && pe.RatingKey != oldKey {
				newKey = pe.RatingKey
				method = methodGUID
			}
		}

		// Strategy 2: Match by title+year
		if newKey == "" && cfg.FallbackTitleYear {
			t := strings.ToLower(strings.TrimSpace(item.Title))
			if t != "" {
				if pe, ok := byTitleYear[t+"|"+item.Year]; ok && pe.RatingKey != oldKey {
					newKey = pe.RatingKey
					method = methodTitleYear
				}
			}
		}

		// Strategy 3: Match by title only (require same media type to avoid movie/show collisions)
		if newKey == "" && cfg.FallbackTitleOnly {
			t := strings.ToLower(strings.TrimSpace(item.Title))
			if t != "" {
				if pe, ok := byTitle[t]; ok && pe.RatingKey != oldKey && pe.Type == item.MediaType {
					newKey = pe.RatingKey
					method = fmt.Sprintf("title only (%s -> %s)", item.Year, pe.Year)
				}
			}
		}

		if newKey != "" {
			matched = append(matched, matchResult{
				Title: item.Title, Year: item.Year,
				OldKey: oldKey, NewKey: newKey,
				MediaType: item.MediaType, Method: method,
			})
		} else {
			unmatched = append(unmatched, unmatchResult{
				Title: item.Title, Year: item.Year,
				OldKey: oldKey, MediaType: item.MediaType,
			})
		}
	}

	return matched, unmatched
}

// --- Remapping ---

func applyRemappings(ctx context.Context, client *http.Client, cfg *config, matched []matchResult, unmatched []unmatchResult) {
	if len(matched) > 0 {
		slog.Info("remapping", "count", len(matched), "dry_run", cfg.DryRun)
		for _, m := range matched {
			slog.Info("remap",
				"title", m.Title, "year", m.Year, "type", m.MediaType,
				"old_key", m.OldKey, "new_key", m.NewKey, "method", m.Method)
			if !cfg.DryRun {
				if ctx.Err() != nil {
					slog.Warn("remapping interrupted", "remaining", len(matched))
					break
				}
				params := url.Values{
					"old_rating_key": {m.OldKey},
					"new_rating_key": {m.NewKey},
					"media_type":     {m.MediaType},
				}
				body, err := tautulliAPI(ctx, client, cfg, "update_metadata_details", params)
				if err != nil {
					slog.Error("remap failed", "title", m.Title, "error", err)
					continue
				}
				var resp struct {
					Response struct {
						Result  string `json:"result"`
						Message string `json:"message"`
					} `json:"response"`
				}
				if err := json.Unmarshal(body, &resp); err == nil && resp.Response.Result != resultSuccess {
					slog.Error("remap API error", "title", m.Title, "message", resp.Response.Message)
				}
			}
		}
	} else {
		slog.Info("no matches found for stale items")
	}

	if len(unmatched) > 0 {
		slog.Info("unmatched items", "count", len(unmatched))
		for _, u := range unmatched {
			slog.Warn("no match",
				"title", u.Title, "year", u.Year, "type", u.MediaType, "key", u.OldKey)
		}
	}
}

func clearRecentlyAdded(ctx context.Context, client *http.Client, cfg *config) {
	if cfg.DryRun {
		slog.Info("(dry run) would clear recently added items")
		return
	}
	slog.Info("clearing recently added items")
	body, err := tautulliAPI(ctx, client, cfg, "delete_recently_added", nil)
	if err != nil {
		slog.Error("failed to clear recently added", "error", err)
		return
	}
	var resp struct {
		Response struct {
			Result  string `json:"result"`
			Message string `json:"message"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && resp.Response.Result != resultSuccess {
		slog.Error("clear recently added failed", "message", resp.Response.Message)
	}
}

// --- Tautulli helpers ---

func tautulliAPIWithRetry(ctx context.Context, client *http.Client, cfg *config, cmd string, extra url.Values) ([]byte, error) {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			delay := time.Duration(attempt) * retryDelayUnit
			slog.Warn("retrying Tautulli API", "cmd", cmd, "attempt", attempt+1, "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, context.Cause(ctx)
			case <-timer.C:
			}
		}
		body, err := tautulliAPI(ctx, client, cfg, cmd, extra)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func tautulliAPI(ctx context.Context, client *http.Client, cfg *config, cmd string, extra url.Values) ([]byte, error) {
	params := url.Values{"cmd": {cmd}, "apikey": {cfg.TautulliAPIKey}}
	maps.Copy(params, extra)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		cfg.TautulliURL+"/api/v2?"+params.Encode(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("tautulli %s: %w", cmd, sanitizeErr(err))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tautulli %s: %w", cmd, sanitizeErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		drainBody(resp.Body)
		return nil, fmt.Errorf("tautulli %s: HTTP %d", cmd, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxTautulliBody))
}

// drainBody reads and discards up to 4 KB to enable HTTP connection reuse.
func drainBody(body io.ReadCloser) {
	if _, err := io.CopyN(io.Discard, body, 4<<10); err != nil && !errors.Is(err, io.EOF) {
		slog.Warn("failed to drain response body", "error", err)
	}
}

// --- Plex helpers ---

type plexSection struct {
	Key   string
	Title string
	Type  string
}

type plexLibItem struct {
	Title     string
	GUIDs     []string // normalized GUIDs
	RatingKey int
	Year      int
}

func plexItemExists(ctx context.Context, client *http.Client, cfg *config, ratingKey string) bool {
	// Validate ratingKey is numeric to prevent path injection
	if _, err := strconv.Atoi(ratingKey); err != nil {
		slog.Warn("invalid rating key (non-numeric)", "key", ratingKey)
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		cfg.PlexURL+"/library/metadata/"+ratingKey, http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("X-Plex-Token", cfg.PlexToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	drainBody(resp.Body)
	return resp.StatusCode == http.StatusOK
}

func plexLibrarySections(ctx context.Context, client *http.Client, cfg *config) []plexSection {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		cfg.PlexURL+"/library/sections", http.NoBody)
	if err != nil {
		slog.Error("failed to create Plex sections request", "error", err)
		return nil
	}
	req.Header.Set("X-Plex-Token", cfg.PlexToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("failed to get Plex sections", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		drainBody(resp.Body)
		slog.Error("Plex sections returned non-200", "status", resp.StatusCode)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlexSections))
	if err != nil {
		slog.Error("failed to read Plex sections response", "error", err)
		return nil
	}

	var result struct {
		MediaContainer struct {
			Directory []struct {
				Key   string `json:"key"`
				Title string `json:"title"`
				Type  string `json:"type"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		slog.Error("failed to parse Plex sections", "error", err)
		return nil
	}

	var sections []plexSection
	for _, d := range result.MediaContainer.Directory {
		sections = append(sections, plexSection{Key: d.Key, Title: d.Title, Type: d.Type})
	}
	return sections
}

func plexLibraryAll(ctx context.Context, client *http.Client, cfg *config, sectionKey string) []plexLibItem {
	// Validate sectionKey is numeric to prevent path injection
	if _, err := strconv.Atoi(sectionKey); err != nil {
		slog.Warn("invalid section key (non-numeric)", "key", sectionKey)
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		cfg.PlexURL+"/library/sections/"+sectionKey+"/all", http.NoBody)
	if err != nil {
		slog.Error("failed to create Plex library request", "error", err)
		return nil
	}
	req.Header.Set("X-Plex-Token", cfg.PlexToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("failed to get Plex library items", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		drainBody(resp.Body)
		slog.Error("Plex library returned non-200", "status", resp.StatusCode, "section", sectionKey)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlexBody))
	if err != nil {
		slog.Error("failed to read Plex library response", "error", err)
		return nil
	}

	var result struct {
		MediaContainer struct {
			Metadata []struct {
				Title     string `json:"title"`
				RatingKey string `json:"ratingKey"`
				GUID      string `json:"guid"`
				Guids     []struct {
					ID string `json:"id"`
				} `json:"Guid"`
				Year int `json:"year"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		slog.Error("failed to parse Plex library", "error", err)
		return nil
	}

	var items []plexLibItem
	for _, m := range result.MediaContainer.Metadata {
		rk, err := strconv.Atoi(m.RatingKey)
		if err != nil {
			slog.Warn("invalid rating key", "key", m.RatingKey, "title", m.Title)
			continue
		}
		var guids []string
		if g := normalizeGUID(m.GUID); g != "" {
			guids = append(guids, g)
		}
		for _, g := range m.Guids {
			if ng := normalizeGUID(g.ID); ng != "" {
				guids = append(guids, ng)
			}
		}
		items = append(items, plexLibItem{
			RatingKey: rk, Title: m.Title, Year: m.Year, GUIDs: guids,
		})
	}
	return items
}

// --- Utilities ---

// sanitizeErr strips the query string from *url.Error messages to prevent
// API keys from leaking into log output.
func sanitizeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if i := strings.Index(ue.URL, "?"); i >= 0 {
			ue.URL = ue.URL[:i] + "?<redacted>"
		}
	}
	return err
}

// toInt coerces a JSON value (float64, string, or json.Number) to int.
// Returns 0 if the value is nil, empty, or unparseable.
func toInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			return 0
		}
		return n
	case json.Number:
		n, err := val.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	}
	return 0
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required environment variable is missing", "key", key)
		os.Exit(1)
	}
	return v
}
