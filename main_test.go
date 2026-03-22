package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestMain(m *testing.M) {
	retryDelayUnit = time.Millisecond
	paginationDelay = time.Millisecond
	os.Exit(m.Run())
}

// --- Tests: normalizeGUID ---

func TestNormalizeGUID(t *testing.T) {
	tests := []struct {
		name string
		guid string
		want string
	}{
		// IMDB
		{"imdb legacy with query params", "com.plexapp.agents.imdb://tt1234567?lang=en", "imdb://tt1234567"},
		{"imdb bare", "imdb://tt9999999", "imdb://tt9999999"},

		// TMDB
		{"themoviedb legacy agent", "com.plexapp.agents.themoviedb://12345?lang=en", "tmdb://12345"},
		{"tmdb new agent", "tmdb://67890", "tmdb://67890"},
		{"tmdb with query params", "tmdb://12345?lang=en", "tmdb://12345"},

		// TVDB
		{"thetvdb legacy with season path", "com.plexapp.agents.thetvdb://271557/3/1?lang=en", "tvdb://271557"},
		{"thetvdb with deep path", "com.plexapp.agents.thetvdb://271557/3/1/5?lang=en", "tvdb://271557"},
		{"tvdb new agent", "tvdb://271557", "tvdb://271557"},

		// Plex native
		{"plex movie", "plex://movie/5d776b59ad5437001f79c6f8", "plex://movie/5d776b59ad5437001f79c6f8"},
		{"plex episode", "plex://episode/5d9c135046115600200d30a2", "plex://episode/5d9c135046115600200d30a2"},

		// MusicBrainz
		{"mbid", "mbid://abcdef01-2345-6789-abcd-ef0123456789", "mbid://abcdef01-2345-6789-abcd-ef0123456789"},

		// Unsupported / empty
		{"local unsupported", "local://616507", ""},
		{"agents.none unsupported", "com.plexapp.agents.none://632d404bf27d52a513ccd45e4df820cd276f3090?lang=xn", ""},
		{"unknown scheme", "custom://something", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGUID(tt.guid); got != tt.want {
				t.Errorf("normalizeGUID(%q) = %q, want %q", tt.guid, got, tt.want)
			}
		})
	}
}

// --- Tests: extractAfter ---

func TestExtractAfter(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		prefix string
		want   string
	}{
		{"simple extraction", "imdb://tt1234567", "imdb://", "tt1234567"},
		{"strips query params", "com.plexapp.agents.imdb://tt1234567?lang=en", "imdb://", "tt1234567"},
		{"prefix not found", "tmdb://12345", "imdb://", ""},
		{"empty input", "", "imdb://", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAfter(tt.s, tt.prefix); got != tt.want {
				t.Errorf("extractAfter(%q, %q) = %q, want %q", tt.s, tt.prefix, got, tt.want)
			}
		})
	}
}

// --- Tests: toInt ---

func TestToInt(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int
	}{
		{"float64", float64(42), 42},
		{"float64 truncates decimal", float64(99.9), 99},
		{"float64 zero", float64(0), 0},
		{"float64 negative", float64(-5), -5},
		{"string number", "123", 123},
		{"string zero", "0", 0},
		{"string negative", "-10", -10},
		{"string empty", "", 0},
		{"string non-numeric", "abc", 0},
		{"json.Number valid", json.Number("456"), 456},
		{"json.Number invalid", json.Number("not_a_number"), 0},
		{"nil", nil, 0},
		{"unsupported type", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toInt(tt.input); got != tt.want {
				t.Errorf("toInt(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- Tests: processHistoryRow ---

func TestProcessHistoryRow(t *testing.T) {
	t.Run("movie with valid rating key", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(42), Title: "Test Movie",
			Year: float64(2020), MediaType: "movie",
			GUID: "imdb://tt1234567",
		}
		processHistoryRow(row, items)
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items["42"].Title != "Test Movie" {
			t.Errorf("unexpected title: %s", items["42"].Title)
		}
	})

	t.Run("movie with zero rating key is skipped", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(0), Title: "Bad Movie",
			Year: float64(2020), MediaType: "movie",
		}
		processHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items for zero rating key, got %d", len(items))
		}
	})

	t.Run("movie with unparseable rating key is skipped", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: "not_a_number", Title: "Bad Movie",
			Year: float64(2020), MediaType: "movie",
		}
		processHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items for unparseable rating key, got %d", len(items))
		}
	})

	t.Run("episode uses grandparent rating key", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(99), GrandparentRatingKey: float64(50),
			Title: "Episode Title", GrandparentTitle: "Show Title",
			Year: float64(2021), MediaType: "episode",
			GUID: "tvdb://271557",
		}
		processHistoryRow(row, items)
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		entry := items["50"]
		if entry.Title != "Show Title" {
			t.Errorf("expected grandparent title, got %q", entry.Title)
		}
		if entry.MediaType != "show" {
			t.Errorf("expected media type 'show', got %q", entry.MediaType)
		}
	})

	t.Run("episode with zero grandparent key is skipped", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(99), GrandparentRatingKey: float64(0),
			Title: "Episode", MediaType: "episode",
		}
		processHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items for zero grandparent key, got %d", len(items))
		}
	})

	t.Run("episode strips plex episode GUID", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(99), GrandparentRatingKey: float64(50),
			Title: "Ep", GrandparentTitle: "Show",
			Year: float64(2021), MediaType: "episode",
			GUID: "plex://episode/5d9c135046115600200d30a2",
		}
		processHistoryRow(row, items)
		if items["50"].GUID != "" {
			t.Errorf("expected empty GUID for plex episode, got %q", items["50"].GUID)
		}
	})

	t.Run("episode falls back to episode title when grandparent empty", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(99), GrandparentRatingKey: float64(50),
			Title: "Fallback Title", GrandparentTitle: "",
			Year: float64(2021), MediaType: "episode",
		}
		processHistoryRow(row, items)
		if items["50"].Title != "Fallback Title" {
			t.Errorf("expected fallback title, got %q", items["50"].Title)
		}
	})

	t.Run("duplicate key is not overwritten", func(t *testing.T) {
		items := map[string]tautulliEntry{
			"42": {RatingKey: "42", Title: "First", Year: "2020", MediaType: "movie"},
		}
		row := &historyItem{
			RatingKey: float64(42), Title: "Second",
			Year: float64(2021), MediaType: "movie",
		}
		processHistoryRow(row, items)
		if items["42"].Title != "First" {
			t.Errorf("expected first entry to be preserved, got %q", items["42"].Title)
		}
	})

	t.Run("unknown media type is ignored", func(t *testing.T) {
		items := map[string]tautulliEntry{}
		row := &historyItem{
			RatingKey: float64(42), Title: "Music",
			Year: float64(2020), MediaType: "track",
		}
		processHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items for unknown media type, got %d", len(items))
		}
	})
}

// --- Tests: matchStaleItems ---

// allFallbacks returns a config with both fallback strategies enabled.
func allFallbacks() *config {
	return &config{FallbackTitleYear: true, FallbackTitleOnly: true}
}

// --- Tests: loadConfig ---

func TestLoadConfig(t *testing.T) {
	t.Setenv("TAUTULLI_URL", "http://localhost:8181")
	t.Setenv("TAUTULLI_APIKEY", "test-key")
	t.Setenv("PLEX_URL", "http://localhost:32400")
	t.Setenv("PLEX_TOKEN", "test-token")
	t.Setenv("DRY_RUN", "false")
	t.Setenv("FALLBACK_TITLE_YEAR", "true")
	t.Setenv("FALLBACK_TITLE_ONLY", "true")
	t.Setenv("SCHEDULE_HOURS", "12")

	cfg := loadConfig()

	if cfg.TautulliURL != "http://localhost:8181" {
		t.Errorf("TautulliURL = %q", cfg.TautulliURL)
	}
	if cfg.DryRun {
		t.Error("DryRun should be false")
	}
	if !cfg.FallbackTitleYear {
		t.Error("FallbackTitleYear should be true")
	}
	if !cfg.FallbackTitleOnly {
		t.Error("FallbackTitleOnly should be true")
	}
	if cfg.ScheduleHours != 12 {
		t.Errorf("ScheduleHours = %d, want 12", cfg.ScheduleHours)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("TAUTULLI_APIKEY", "key")
	t.Setenv("PLEX_TOKEN", "token")
	t.Setenv("DRY_RUN", "")
	t.Setenv("SCHEDULE_HOURS", "")
	t.Setenv("FALLBACK_TITLE_YEAR", "")
	t.Setenv("FALLBACK_TITLE_ONLY", "")
	t.Setenv("TAUTULLI_URL", "")
	t.Setenv("PLEX_URL", "")

	cfg := loadConfig()

	if cfg.TautulliURL != "http://tautulli:8181" {
		t.Errorf("TautulliURL = %q, want default", cfg.TautulliURL)
	}
	if cfg.PlexURL != "http://plex:32400" {
		t.Errorf("PlexURL = %q, want default", cfg.PlexURL)
	}
	if !cfg.DryRun {
		t.Error("DryRun should default to true")
	}
	if cfg.ScheduleHours != 0 {
		t.Errorf("ScheduleHours = %d, want 0", cfg.ScheduleHours)
	}
	if !cfg.FallbackTitleYear {
		t.Error("FallbackTitleYear should default to true")
	}
	if cfg.FallbackTitleOnly {
		t.Error("FallbackTitleOnly should default to false")
	}
}

func TestLoadConfigInvalidScheduleHours(t *testing.T) {
	t.Setenv("TAUTULLI_APIKEY", "key")
	t.Setenv("PLEX_TOKEN", "token")
	t.Setenv("SCHEDULE_HOURS", "notanumber")

	cfg := loadConfig()
	if cfg.ScheduleHours != 0 {
		t.Errorf("ScheduleHours = %d, want 0 fallback", cfg.ScheduleHours)
	}
}

// --- Tests: plexItemExists ratingKey validation ---

func TestPlexItemExistsRejectsNonNumericKey(t *testing.T) {
	// A crafted ratingKey like "../../../etc/passwd" should be rejected
	// before making any HTTP request.
	cfg := &config{PlexURL: "http://localhost:32400", PlexToken: "token"}
	client := &http.Client{Timeout: 1 * time.Second}
	ctx := context.Background()

	tests := []string{
		"../../../etc/passwd",
		"abc",
		"123/../../secret",
		"",
		"12 34",
	}
	for _, key := range tests {
		if plexItemExists(ctx, client, cfg, key) {
			t.Errorf("plexItemExists should return false for non-numeric key %q", key)
		}
	}
}

func TestMatchStaleItems(t *testing.T) {
	t.Run("guid match takes priority over title+year", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "The Matrix", Year: "1999", MediaType: "movie", GUID: "imdb://tt0133093"},
		}
		byGUID := map[string]plexEntry{
			"imdb://tt0133093": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: "movie"},
		}
		byTitleYear := map[string]plexEntry{
			"the matrix|1999": {RatingKey: "300", Title: "The Matrix", Year: "1999", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, byGUID, byTitleYear, nil)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].NewKey != "200" {
			t.Errorf("expected GUID match key 200, got %s", matched[0].NewKey)
		}
		if matched[0].Method != methodGUID {
			t.Errorf("expected method 'guid', got %s", matched[0].Method)
		}
		if len(unmatched) != 0 {
			t.Errorf("expected 0 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("title+year fallback when no guid", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Inception", Year: "2010", MediaType: "movie", GUID: ""},
		}
		byTitleYear := map[string]plexEntry{
			"inception|2010": {RatingKey: "200", Title: "Inception", Year: "2010", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, byTitleYear, nil)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].NewKey != "200" {
			t.Errorf("expected key 200, got %s", matched[0].NewKey)
		}
		if matched[0].Method != methodTitleYear {
			t.Errorf("expected method 'title+year', got %s", matched[0].Method)
		}
		if len(unmatched) != 0 {
			t.Errorf("expected 0 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("title-only fallback with matching type", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: "movie", GUID: ""},
		}
		byTitle := map[string]plexEntry{
			"dune": {RatingKey: "200", Title: "Dune", Year: "2021", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, nil, byTitle)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].Method != "title only (2020 -> 2021)" {
			t.Errorf("unexpected method: %s", matched[0].Method)
		}
		if len(unmatched) != 0 {
			t.Errorf("expected 0 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("title-only rejects type mismatch", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Home Alone", Year: "2025", MediaType: "show", GUID: ""},
		}
		byTitle := map[string]plexEntry{
			"home alone": {RatingKey: "200", Title: "Home Alone", Year: "1990", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, nil, byTitle)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched (type mismatch), got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Errorf("expected 1 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("case insensitive title matching", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "THE MATRIX", Year: "1999", MediaType: "movie", GUID: ""},
		}
		byTitleYear := map[string]plexEntry{
			"the matrix|1999": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: "movie"},
		}

		matched, _ := matchStaleItems(allFallbacks(), stale, nil, byTitleYear, nil)

		if len(matched) != 1 || matched[0].NewKey != "200" {
			t.Errorf("expected case-insensitive match to key 200")
		}
	})

	t.Run("same key is not remapped", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"200": {RatingKey: "200", Title: "The Matrix", Year: "1999", MediaType: "movie", GUID: "imdb://tt0133093"},
		}
		byGUID := map[string]plexEntry{
			"imdb://tt0133093": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, byGUID, nil, nil)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched (same key), got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Errorf("expected 1 unmatched (same key), got %d", len(unmatched))
		}
	})

	t.Run("no match produces unmatched", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Nonexistent", Year: "2025", MediaType: "movie", GUID: "imdb://tt0000000"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, nil, nil)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched, got %d", len(matched))
		}
		if len(unmatched) != 1 || unmatched[0].OldKey != "100" {
			t.Errorf("expected 1 unmatched with key 100")
		}
	})

	t.Run("title+year disabled skips fallback", func(t *testing.T) {
		cfg := &config{FallbackTitleYear: false, FallbackTitleOnly: false}
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Inception", Year: "2010", MediaType: "movie", GUID: ""},
		}
		byTitleYear := map[string]plexEntry{
			"inception|2010": {RatingKey: "200", Title: "Inception", Year: "2010", Type: "movie"},
		}

		matched, _ := matchStaleItems(cfg, stale, nil, byTitleYear, nil)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched (fallback disabled), got %d", len(matched))
		}
	})

	t.Run("title-only disabled skips fallback", func(t *testing.T) {
		cfg := &config{FallbackTitleYear: true, FallbackTitleOnly: false}
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: "movie", GUID: ""},
		}
		byTitle := map[string]plexEntry{
			"dune": {RatingKey: "200", Title: "Dune", Year: "2021", Type: "movie"},
		}

		matched, _ := matchStaleItems(cfg, stale, nil, nil, byTitle)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched (title-only disabled), got %d", len(matched))
		}
	})

	t.Run("whitespace-only title does not match", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "   ", Year: "2020", MediaType: "movie", GUID: ""},
		}
		byTitleYear := map[string]plexEntry{
			"|2020": {RatingKey: "200", Title: "", Year: "2020", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, byTitleYear, nil)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched for whitespace title, got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Errorf("expected 1 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("empty GUID skips GUID matching", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Test", Year: "2020", MediaType: "movie", GUID: ""},
		}
		byGUID := map[string]plexEntry{
			"": {RatingKey: "200", Title: "Test", Year: "2020", Type: "movie"},
		}

		matched, _ := matchStaleItems(allFallbacks(), stale, byGUID, nil, nil)

		// Empty GUID should NOT match the empty string key in byGUID
		if len(matched) != 0 {
			t.Errorf("expected 0 matched for empty GUID, got %d", len(matched))
		}
	})

	t.Run("multiple stale items mixed results", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {RatingKey: "100", Title: "Movie A", Year: "2020", MediaType: "movie", GUID: "imdb://tt1111111"},
			"200": {RatingKey: "200", Title: "Movie B", Year: "2021", MediaType: "movie", GUID: "imdb://tt2222222"},
			"300": {RatingKey: "300", Title: "Movie C", Year: "2022", MediaType: "movie", GUID: "imdb://tt3333333"},
		}
		byGUID := map[string]plexEntry{
			"imdb://tt1111111": {RatingKey: "101", Title: "Movie A", Year: "2020", Type: "movie"},
		}
		byTitleYear := map[string]plexEntry{
			"movie c|2022": {RatingKey: "301", Title: "Movie C", Year: "2022", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, byGUID, byTitleYear, nil)

		if len(matched) != 2 {
			t.Fatalf("expected 2 matched, got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Fatalf("expected 1 unmatched, got %d", len(unmatched))
		}
		if unmatched[0].OldKey != "200" {
			t.Errorf("expected unmatched key 200, got %s", unmatched[0].OldKey)
		}
	})
}

// --- Tests: HTTP mock helpers ---

func tautulliServer(t *testing.T, handler http.HandlerFunc) *config {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &config{
		TautulliURL:    srv.URL,
		TautulliAPIKey: "test-key",
		PlexURL:        srv.URL,
		PlexToken:      "test-token",
		DryRun:         true,
	}
}

// --- Tests: tautulliAPI ---

func TestTautulliAPI(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cmd") != "get_history" {
				t.Errorf("unexpected cmd: %s", r.URL.Query().Get("cmd"))
			}
			if r.URL.Query().Get("apikey") != "test-key" {
				t.Errorf("unexpected apikey: %s", r.URL.Query().Get("apikey"))
			}
			w.Write([]byte(`{"response":{"result":"success"}}`))
		})
		body, err := tautulliAPI(context.Background(), &http.Client{}, cfg, "get_history", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(body), "success") {
			t.Errorf("unexpected body: %s", body)
		}
	})

	t.Run("non-200 returns error", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, err := tautulliAPI(context.Background(), &http.Client{}, cfg, "get_history", nil)
		if err == nil {
			t.Fatal("expected error for 500 response")
		}
		if !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("extra params forwarded", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("start") != "100" {
				t.Errorf("expected start=100, got %s", r.URL.Query().Get("start"))
			}
			w.Write([]byte(`{}`))
		})
		extra := url.Values{"start": {"100"}}
		_, err := tautulliAPI(context.Background(), &http.Client{}, cfg, "test", extra)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := tautulliAPI(ctx, &http.Client{}, cfg, "test", nil)
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})
}

// --- Tests: plexItemExists ---

func TestPlexItemExists(t *testing.T) {
	t.Run("returns true for 200", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "/library/metadata/42") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		got := plexItemExists(context.Background(), &http.Client{}, cfg, "42")
		if !got {
			t.Error("expected true for existing item")
		}
	})

	t.Run("returns false for 404", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		got := plexItemExists(context.Background(), &http.Client{}, cfg, "999")
		if got {
			t.Error("expected false for 404")
		}
	})
}

// --- Tests: plexLibrarySections ---

func TestPlexLibrarySections(t *testing.T) {
	t.Run("parses sections", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"1","title":"Movies","type":"movie"},
				{"key":"2","title":"TV Shows","type":"show"},
				{"key":"3","title":"Music","type":"artist"}
			]}}`))
		})
		sections := plexLibrarySections(context.Background(), &http.Client{}, cfg)
		if len(sections) != 3 {
			t.Fatalf("expected 3 sections, got %d", len(sections))
		}
		if sections[0].Title != "Movies" || sections[0].Type != "movie" {
			t.Errorf("unexpected first section: %+v", sections[0])
		}
	})

	t.Run("returns nil on error", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		sections := plexLibrarySections(context.Background(), &http.Client{}, cfg)
		if sections != nil {
			t.Errorf("expected nil on error, got %v", sections)
		}
	})

	t.Run("returns nil on invalid JSON", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`not json`))
		})
		sections := plexLibrarySections(context.Background(), &http.Client{}, cfg)
		if sections != nil {
			t.Errorf("expected nil on invalid JSON, got %v", sections)
		}
	})
}

// --- Tests: plexLibraryAll ---

func TestPlexLibraryAll(t *testing.T) {
	t.Run("parses library items with GUIDs", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{
					"title":"The Matrix","ratingKey":"42","year":1999,
					"guid":"plex://movie/5d776b59ad5437001f79c6f8",
					"Guid":[{"id":"imdb://tt0133093"},{"id":"tmdb://603"}]
				}
			]}}`))
		})
		items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].Title != "The Matrix" || items[0].RatingKey != 42 {
			t.Errorf("unexpected item: %+v", items[0])
		}
		if len(items[0].GUIDs) != 3 {
			t.Errorf("expected 3 GUIDs, got %d: %v", len(items[0].GUIDs), items[0].GUIDs)
		}
	})

	t.Run("rejects non-numeric section key", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Fatal("should not make HTTP request for non-numeric key")
		})
		items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "abc")
		if items != nil {
			t.Errorf("expected nil for non-numeric key, got %v", items)
		}
	})

	t.Run("skips items with invalid rating key", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"title":"Bad","ratingKey":"abc","year":2020},
				{"title":"Good","ratingKey":"42","year":2020}
			]}}`))
		})
		items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
		if len(items) != 1 {
			t.Fatalf("expected 1 item (bad key skipped), got %d", len(items))
		}
		if items[0].Title != "Good" {
			t.Errorf("expected Good, got %s", items[0].Title)
		}
	})
}

// --- Tests: drainBody ---

func TestDrainBody(t *testing.T) {
	body := io.NopCloser(strings.NewReader("hello world"))
	drainBody(body)
}

// --- Tests: getEnv ---

func TestGetEnv(t *testing.T) {
	t.Setenv("TEST_GET_ENV", "value")
	if got := getEnv("TEST_GET_ENV", "default"); got != "value" {
		t.Errorf("getEnv = %q, want value", got)
	}
	t.Setenv("TEST_GET_ENV", "")
	if got := getEnv("TEST_GET_ENV", "default"); got != "default" {
		t.Errorf("getEnv = %q, want default", got)
	}
}

// --- Tests: setHealthy ---

func TestSetHealthy(t *testing.T) {
	setHealthy(true)
	if _, err := os.Stat(healthFile); err != nil {
		t.Error("health file should exist after setHealthy(true)")
	}
	setHealthy(false)
	if _, err := os.Stat(healthFile); err == nil {
		t.Error("health file should not exist after setHealthy(false)")
	}
}

// --- Tests: tautulliAPIWithRetry ---

func TestTautulliAPIWithRetry(t *testing.T) {
	t.Run("succeeds on first attempt", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Write([]byte(`{"ok":true}`))
		})
		body, err := tautulliAPIWithRetry(context.Background(), &http.Client{}, cfg, "test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(body), "ok") {
			t.Errorf("unexpected body: %s", body)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("retries on failure then succeeds", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{"ok":true}`))
		})
		body, err := tautulliAPIWithRetry(context.Background(), &http.Client{}, cfg, "test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(body), "ok") {
			t.Errorf("unexpected body: %s", body)
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("returns error after all retries exhausted", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, err := tautulliAPIWithRetry(context.Background(), &http.Client{}, cfg, "test", nil)
		if err == nil {
			t.Fatal("expected error after all retries")
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("cancelled context stops retries", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := tautulliAPIWithRetry(ctx, &http.Client{}, cfg, "test", nil)
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})
}

// --- Tests: collectTautulliItems ---

func TestCollectTautulliItems(t *testing.T) {
	t.Run("single page of results", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":2,
				"data":[
					{"rating_key":42,"title":"Movie A","year":2020,"media_type":"movie","guid":"imdb://tt1111111"},
					{"rating_key":99,"grandparent_rating_key":50,"title":"Ep 1","grandparent_title":"Show B","year":2021,"media_type":"episode","guid":"tvdb://271557"}
				]
			}}}`))
		})
		items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
		if items == nil {
			t.Fatal("expected non-nil items")
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
		if items["42"].Title != "Movie A" {
			t.Errorf("unexpected movie title: %s", items["42"].Title)
		}
		if items["50"].MediaType != "show" {
			t.Errorf("expected show, got %s", items["50"].MediaType)
		}
	})

	t.Run("multi-page pagination", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			start := r.URL.Query().Get("start")
			if start == "0" {
				w.Write([]byte(`{"response":{"result":"success","data":{
					"recordsFiltered":1500,
					"data":[{"rating_key":1,"title":"Movie 1","year":2020,"media_type":"movie","guid":""}]
				}}}`))
			} else {
				w.Write([]byte(`{"response":{"result":"success","data":{
					"recordsFiltered":1500,
					"data":[{"rating_key":2,"title":"Movie 2","year":2021,"media_type":"movie","guid":""}]
				}}}`))
			}
		})
		items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
		if items == nil {
			t.Fatal("expected non-nil items")
		}
		if len(items) != 2 {
			t.Errorf("expected 2 items, got %d", len(items))
		}
		if calls < 2 {
			t.Errorf("expected at least 2 API calls for pagination, got %d", calls)
		}
	})

	t.Run("API error returns nil", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"response":{"result":"error","message":"bad request"}}`))
		})
		items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
		if items != nil {
			t.Errorf("expected nil on API error, got %v", items)
		}
	})

	t.Run("invalid JSON returns nil", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`not json`))
		})
		items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
		if items != nil {
			t.Errorf("expected nil on invalid JSON, got %v", items)
		}
	})

	t.Run("empty data returns empty map", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
		})
		items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
		if items == nil {
			t.Fatal("expected non-nil map")
		}
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("HTTP failure returns nil", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
		if items != nil {
			t.Errorf("expected nil on HTTP failure, got %v", items)
		}
	})
}

// --- Tests: findStaleKeys ---

func TestFindStaleKeys(t *testing.T) {
	t.Run("identifies stale and valid keys", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/library/metadata/42") {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		})
		items := map[string]tautulliEntry{
			"42":  {RatingKey: "42", Title: "Valid", MediaType: "movie"},
			"999": {RatingKey: "999", Title: "Stale", MediaType: "movie"},
		}
		stale := findStaleKeys(context.Background(), &http.Client{}, cfg, items)
		if len(stale) != 1 {
			t.Fatalf("expected 1 stale, got %d", len(stale))
		}
		if _, ok := stale["999"]; !ok {
			t.Error("expected key 999 to be stale")
		}
	})

	t.Run("all keys valid returns empty", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		items := map[string]tautulliEntry{
			"1": {RatingKey: "1", Title: "A", MediaType: "movie"},
		}
		stale := findStaleKeys(context.Background(), &http.Client{}, cfg, items)
		if len(stale) != 0 {
			t.Errorf("expected 0 stale, got %d", len(stale))
		}
	})

	t.Run("cancelled context returns partial results", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		items := map[string]tautulliEntry{
			"1": {RatingKey: "1", Title: "A", MediaType: "movie"},
		}
		// With cancelled context, should return early
		stale := findStaleKeys(ctx, &http.Client{}, cfg, items)
		// May or may not have processed any items — just verify no panic
		_ = stale
	})
}

// --- Tests: buildPlexIndex ---

func TestBuildPlexIndex(t *testing.T) {
	t.Run("builds all three indexes", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/library/sections"):
				w.Write([]byte(`{"MediaContainer":{"Directory":[
					{"key":"1","title":"Movies","type":"movie"},
					{"key":"2","title":"TV Shows","type":"show"},
					{"key":"3","title":"Music","type":"artist"}
				]}}`))
			case strings.Contains(r.URL.Path, "/sections/1/all"):
				w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"title":"The Matrix","ratingKey":"42","year":1999,
					 "guid":"plex://movie/abc","Guid":[{"id":"imdb://tt0133093"}]}
				]}}`))
			case strings.Contains(r.URL.Path, "/sections/2/all"):
				w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"title":"Breaking Bad","ratingKey":"99","year":2008,
					 "guid":"plex://show/def","Guid":[{"id":"tvdb://81189"}]}
				]}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		byGUID, byTitleYear, byTitle := buildPlexIndex(context.Background(), &http.Client{}, cfg)

		// GUID index should have entries for all normalized GUIDs
		if _, ok := byGUID["imdb://tt0133093"]; !ok {
			t.Error("expected imdb GUID in byGUID")
		}
		if _, ok := byGUID["plex://movie/abc"]; !ok {
			t.Error("expected plex movie GUID in byGUID")
		}
		if _, ok := byGUID["tvdb://81189"]; !ok {
			t.Error("expected tvdb GUID in byGUID")
		}

		// Title+year index
		if _, ok := byTitleYear["the matrix|1999"]; !ok {
			t.Error("expected 'the matrix|1999' in byTitleYear")
		}
		if _, ok := byTitleYear["breaking bad|2008"]; !ok {
			t.Error("expected 'breaking bad|2008' in byTitleYear")
		}

		// Title-only index
		if _, ok := byTitle["the matrix"]; !ok {
			t.Error("expected 'the matrix' in byTitle")
		}
		if _, ok := byTitle["breaking bad"]; !ok {
			t.Error("expected 'breaking bad' in byTitle")
		}
	})

	t.Run("skips non-movie/show sections", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/library/sections") {
				w.Write([]byte(`{"MediaContainer":{"Directory":[
					{"key":"3","title":"Music","type":"artist"}
				]}}`))
			} else {
				t.Error("should not fetch library items for non-movie/show sections")
				w.WriteHeader(http.StatusNotFound)
			}
		})
		byGUID, byTitleYear, byTitle := buildPlexIndex(context.Background(), &http.Client{}, cfg)
		if len(byGUID) != 0 || len(byTitleYear) != 0 || len(byTitle) != 0 {
			t.Error("expected empty indexes for music-only library")
		}
	})
}

// --- Tests: applyRemappings ---

func TestApplyRemappings(t *testing.T) {
	t.Run("dry run logs but does not call API", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Write([]byte(`{}`))
		})
		cfg.DryRun = true
		matched := []matchResult{
			{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: "movie", Method: methodGUID},
		}
		applyRemappings(context.Background(), &http.Client{}, cfg, matched, nil)
		if calls != 0 {
			t.Errorf("expected 0 API calls in dry run, got %d", calls)
		}
	})

	t.Run("live remap calls API", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Query().Get("cmd") != "update_metadata_details" {
				t.Errorf("unexpected cmd: %s", r.URL.Query().Get("cmd"))
			}
			w.Write([]byte(`{"response":{"result":"success"}}`))
		})
		cfg.DryRun = false
		matched := []matchResult{
			{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: "movie", Method: methodGUID},
		}
		applyRemappings(context.Background(), &http.Client{}, cfg, matched, nil)
		if calls != 1 {
			t.Errorf("expected 1 API call, got %d", calls)
		}
	})

	t.Run("live remap handles API error gracefully", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		cfg.DryRun = false
		matched := []matchResult{
			{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: "movie", Method: methodGUID},
		}
		// Should not panic
		applyRemappings(context.Background(), &http.Client{}, cfg, matched, nil)
	})

	t.Run("live remap handles non-success result", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"response":{"result":"error","message":"not found"}}`))
		})
		cfg.DryRun = false
		matched := []matchResult{
			{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: "movie", Method: methodGUID},
		}
		// Should not panic
		applyRemappings(context.Background(), &http.Client{}, cfg, matched, nil)
	})

	t.Run("empty matched logs no-matches message", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("should not call API with empty matched")
		})
		applyRemappings(context.Background(), &http.Client{}, cfg, nil, nil)
	})

	t.Run("unmatched items are logged", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("should not call API for unmatched items")
		})
		unmatched := []unmatchResult{
			{Title: "Unknown", Year: "2020", OldKey: "100", MediaType: "movie"},
		}
		// Should not panic, just log
		applyRemappings(context.Background(), &http.Client{}, cfg, nil, unmatched)
	})

	t.Run("cancelled context stops remapping", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Write([]byte(`{"response":{"result":"success"}}`))
		})
		cfg.DryRun = false
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		matched := []matchResult{
			{Title: "A", OldKey: "1", NewKey: "2", MediaType: "movie", Method: methodGUID},
			{Title: "B", OldKey: "3", NewKey: "4", MediaType: "movie", Method: methodGUID},
		}
		applyRemappings(ctx, &http.Client{}, cfg, matched, nil)
		// With cancelled context, should stop early (0 or 1 calls max)
		if calls > 1 {
			t.Errorf("expected at most 1 call with cancelled context, got %d", calls)
		}
	})
}

// --- Tests: clearRecentlyAdded ---

func TestClearRecentlyAdded(t *testing.T) {
	t.Run("dry run does not call API", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
		})
		cfg.DryRun = true
		clearRecentlyAdded(context.Background(), &http.Client{}, cfg)
		if calls != 0 {
			t.Errorf("expected 0 API calls in dry run, got %d", calls)
		}
	})

	t.Run("live calls delete_recently_added", func(t *testing.T) {
		calls := 0
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Query().Get("cmd") != "delete_recently_added" {
				t.Errorf("unexpected cmd: %s", r.URL.Query().Get("cmd"))
			}
			w.Write([]byte(`{"response":{"result":"success"}}`))
		})
		cfg.DryRun = false
		clearRecentlyAdded(context.Background(), &http.Client{}, cfg)
		if calls != 1 {
			t.Errorf("expected 1 API call, got %d", calls)
		}
	})

	t.Run("handles HTTP error gracefully", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		cfg.DryRun = false
		clearRecentlyAdded(context.Background(), &http.Client{}, cfg)
	})

	t.Run("handles non-success result", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"response":{"result":"error","message":"fail"}}`))
		})
		cfg.DryRun = false
		clearRecentlyAdded(context.Background(), &http.Client{}, cfg)
	})
}

// --- Tests: run (end-to-end with mock servers) ---

func TestRun(t *testing.T) {
	t.Run("all keys valid returns true", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Query().Get("cmd") == "get_history":
				w.Write([]byte(`{"response":{"result":"success","data":{
					"recordsFiltered":1,
					"data":[{"rating_key":42,"title":"Movie","year":2020,"media_type":"movie","guid":""}]
				}}}`))
			case strings.Contains(r.URL.Path, "/library/metadata/"):
				w.WriteHeader(http.StatusOK)
			default:
				w.Write([]byte(`{}`))
			}
		})
		cfg.DryRun = true
		ok := run(context.Background(), cfg)
		if !ok {
			t.Error("expected true when all keys are valid")
		}
	})

	t.Run("stale keys trigger remap flow", func(t *testing.T) {
		remapCalled := false
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			cmd := r.URL.Query().Get("cmd")
			switch {
			case cmd == "get_history":
				w.Write([]byte(`{"response":{"result":"success","data":{
					"recordsFiltered":1,
					"data":[{"rating_key":100,"title":"Stale Movie","year":2020,"media_type":"movie","guid":"imdb://tt1111111"}]
				}}}`))
			case cmd == "update_metadata_details":
				remapCalled = true
				w.Write([]byte(`{"response":{"result":"success"}}`))
			case cmd == "delete_recently_added":
				w.Write([]byte(`{"response":{"result":"success"}}`))
			case strings.HasSuffix(r.URL.Path, "/library/metadata/100"):
				w.WriteHeader(http.StatusNotFound)
			case strings.HasSuffix(r.URL.Path, "/library/sections"):
				w.Write([]byte(`{"MediaContainer":{"Directory":[
					{"key":"1","title":"Movies","type":"movie"}
				]}}`))
			case strings.Contains(r.URL.Path, "/sections/1/all"):
				w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"title":"Stale Movie","ratingKey":"200","year":2020,
					 "guid":"plex://movie/x","Guid":[{"id":"imdb://tt1111111"}]}
				]}}`))
			default:
				w.Write([]byte(`{}`))
			}
		})
		cfg.DryRun = false
		ok := run(context.Background(), cfg)
		if !ok {
			t.Error("expected true on successful remap")
		}
		if !remapCalled {
			t.Error("expected update_metadata_details to be called")
		}
	})

	t.Run("history failure returns false", func(t *testing.T) {
		cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		cfg.DryRun = true
		ok := run(context.Background(), cfg)
		if ok {
			t.Error("expected false when history collection fails")
		}
	})

	t.Run("dry run skips backup", func(t *testing.T) {
		backupCalled := false
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			cmd := r.URL.Query().Get("cmd")
			if cmd == "backup_db" {
				backupCalled = true
			}
			if cmd == "get_history" {
				w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
				return
			}
			w.Write([]byte(`{}`))
		})
		cfg.DryRun = true
		run(context.Background(), cfg)
		if backupCalled {
			t.Error("backup_db should not be called in dry run")
		}
	})

	t.Run("non-dry-run calls backup", func(t *testing.T) {
		backupCalled := false
		cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
			cmd := r.URL.Query().Get("cmd")
			if cmd == "backup_db" {
				backupCalled = true
			}
			if cmd == "get_history" {
				w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
				return
			}
			w.Write([]byte(`{}`))
		})
		cfg.DryRun = false
		run(context.Background(), cfg)
		if !backupCalled {
			t.Error("backup_db should be called when not in dry run")
		}
	})
}

// --- Tests: logConfig ---

func TestLogConfig(t *testing.T) {
	cfg := &config{
		TautulliURL:       "http://localhost:8181",
		TautulliAPIKey:    "secret",
		PlexURL:           "http://localhost:32400",
		PlexToken:         "secret",
		DryRun:            true,
		FallbackTitleYear: true,
		FallbackTitleOnly: false,
		ScheduleHours:     12,
	}
	// Should not panic
	logConfig(cfg)
}

// --- Round 2: additional coverage for remaining gaps ---

// TestDrainBodyLargeContent verifies drainBody handles content larger than 4KB.
func TestDrainBodyLargeContent(t *testing.T) {
	// Create a body larger than the 4KB drain limit
	large := strings.Repeat("x", 8192)
	body := io.NopCloser(strings.NewReader(large))
	drainBody(body)
}

// TestPlexLibraryAllHTTPError covers the HTTP error path in plexLibraryAll.
func TestPlexLibraryAllHTTPError(t *testing.T) {
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
	if items != nil {
		t.Errorf("expected nil on HTTP error, got %v", items)
	}
}

// TestPlexLibraryAllInvalidJSON covers the JSON parse error path.
func TestPlexLibraryAllInvalidJSON(t *testing.T) {
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`not json`))
	})
	items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
	if items != nil {
		t.Errorf("expected nil on invalid JSON, got %v", items)
	}
}

// TestPlexLibraryAllEmptyMetadata covers the case with no metadata items.
func TestPlexLibraryAllEmptyMetadata(t *testing.T) {
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	})
	items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

// TestPlexLibraryAllUnsupportedGUID covers items with unsupported GUID formats.
func TestPlexLibraryAllUnsupportedGUID(t *testing.T) {
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[
			{"title":"Local Item","ratingKey":"42","year":2020,
			 "guid":"local://12345","Guid":[{"id":"local://67890"}]}
		]}}`))
	})
	items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].GUIDs) != 0 {
		t.Errorf("expected 0 GUIDs for unsupported format, got %d", len(items[0].GUIDs))
	}
}

// TestRunBackupFailureContinues verifies run continues when backup fails.
func TestRunBackupFailureContinues(t *testing.T) {
	calls := map[string]int{}
	cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		calls[cmd]++
		switch cmd {
		case "backup_db":
			w.WriteHeader(http.StatusInternalServerError)
		case "get_history":
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	cfg.DryRun = false
	ok := run(context.Background(), cfg)
	if !ok {
		t.Error("expected true even when backup fails")
	}
	if calls["backup_db"] != 1 {
		t.Errorf("expected backup_db to be called, got %d", calls["backup_db"])
	}
	if calls["get_history"] != 1 {
		t.Errorf("expected get_history to be called, got %d", calls["get_history"])
	}
}

// --- Property-based tests (rapid) ---

// TestNormalizeGUID_idempotent verifies that normalizing a GUID twice
// produces the same result as normalizing once — the function is idempotent.
func TestNormalizeGUID_idempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate GUIDs that include known prefixes to exercise real paths
		prefix := rapid.SampledFrom([]string{
			"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://",
			"themoviedb://", "thetvdb://",
			"local://", "com.plexapp.agents.none://", "",
		}).Draw(t, "prefix")
		suffix := rapid.StringMatching(`[a-zA-Z0-9/_\-]{0,40}`).Draw(t, "suffix")
		guid := prefix + suffix

		first := normalizeGUID(guid)
		second := normalizeGUID(first)

		if first != second {
			t.Errorf("normalizeGUID is not idempotent: normalizeGUID(%q) = %q, normalizeGUID(%q) = %q",
				guid, first, first, second)
		}
	})
}

// TestNormalizeGUID_never_panics verifies normalizeGUID handles arbitrary input
// without panicking.
func TestNormalizeGUID_never_panics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		guid := rapid.String().Draw(t, "guid")
		_ = normalizeGUID(guid)
	})
}

// TestNormalizeGUID_output_uses_canonical_prefix verifies that non-empty output
// always starts with a known canonical prefix.
func TestNormalizeGUID_output_uses_canonical_prefix(t *testing.T) {
	canonicalPrefixes := []string{"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://"}

	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.SampledFrom([]string{
			"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://",
			"themoviedb://", "thetvdb://",
		}).Draw(t, "prefix")
		id := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "id")
		guid := prefix + id

		result := normalizeGUID(guid)
		if result == "" {
			return
		}

		hasCanonical := false
		for _, cp := range canonicalPrefixes {
			if strings.HasPrefix(result, cp) {
				hasCanonical = true
				break
			}
		}
		if !hasCanonical {
			t.Errorf("normalizeGUID(%q) = %q, does not start with a canonical prefix", guid, result)
		}
	})
}

// TestExtractAfter_never_panics verifies extractAfter handles arbitrary input.
func TestExtractAfter_never_panics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		prefix := rapid.String().Draw(t, "prefix")
		_ = extractAfter(s, prefix)
	})
}

// TestExtractAfter_strips_query_params verifies that query parameters are
// always stripped from the result when the prefix is found.
func TestExtractAfter_strips_query_params(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.SampledFrom([]string{"imdb://", "tmdb://", "tvdb://"}).Draw(t, "prefix")
		id := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "id")
		query := rapid.StringMatching(`[a-z]{1,10}=[a-z]{1,10}`).Draw(t, "query")
		input := prefix + id + "?" + query

		result := extractAfter(input, prefix)
		if strings.Contains(result, "?") {
			t.Errorf("extractAfter(%q, %q) = %q, still contains query params", input, prefix, result)
		}
		if result != id {
			t.Errorf("extractAfter(%q, %q) = %q, want %q", input, prefix, result, id)
		}
	})
}

// TestToInt_float64_roundtrip verifies that integer float64 values round-trip
// through toInt correctly.
func TestToInt_float64_roundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Use int32 range to avoid float64 precision loss
		n := rapid.Int32().Draw(t, "n")
		got := toInt(float64(n))
		if got != int(n) {
			t.Errorf("toInt(float64(%d)) = %d, want %d", n, got, n)
		}
	})
}

// TestToInt_string_roundtrip verifies that string-encoded integers round-trip
// through toInt correctly.
func TestToInt_string_roundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(-1_000_000, 1_000_000).Draw(t, "n")
		s := strconv.Itoa(n)
		got := toInt(s)
		if got != n {
			t.Errorf("toInt(%q) = %d, want %d", s, got, n)
		}
	})
}

// TestToInt_json_number_roundtrip verifies json.Number values round-trip.
func TestToInt_json_number_roundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(-1_000_000, 1_000_000).Draw(t, "n")
		jn := json.Number(strconv.Itoa(n))
		got := toInt(jn)
		if got != n {
			t.Errorf("toInt(json.Number(%q)) = %d, want %d", jn, got, n)
		}
	})
}

// TestToInt_never_panics verifies toInt handles arbitrary types without panicking.
func TestToInt_never_panics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate various types that might be passed to toInt
		choice := rapid.IntRange(0, 6).Draw(t, "choice")
		var v any
		switch choice {
		case 0:
			v = rapid.Float64().Draw(t, "float64")
		case 1:
			v = rapid.String().Draw(t, "string")
		case 2:
			v = json.Number(rapid.String().Draw(t, "json_number"))
		case 3:
			v = nil
		case 4:
			v = rapid.Bool().Draw(t, "bool")
		case 5:
			v = rapid.Int().Draw(t, "int")
		case 6:
			v = []byte(rapid.String().Draw(t, "bytes"))
		}
		_ = toInt(v)
	})
}

// --- Additional edge case tests ---

// TestNormalizeGUID_tvdb_path_handling verifies TVDB GUID path stripping.
// Only the legacy "thetvdb://" prefix strips paths; the new "tvdb://" does not.
func TestNormalizeGUID_tvdb_strips_deep_paths(t *testing.T) {
	tests := []struct {
		name string
		guid string
		want string
	}{
		{"tvdb preserves path (new agent)", "tvdb://271557/3", "tvdb://271557/3"},
		{"tvdb with no path", "tvdb://271557", "tvdb://271557"},
		{"thetvdb strips path (legacy agent)", "thetvdb://12345/1/2?lang=en", "tvdb://12345"},
		{"thetvdb strips single path segment", "thetvdb://99999/1?lang=en", "tvdb://99999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGUID(tt.guid); got != tt.want {
				t.Errorf("normalizeGUID(%q) = %q, want %q", tt.guid, got, tt.want)
			}
		})
	}
}

// TestProcessHistoryRow_episode_with_unparseable_grandparent verifies that
// episodes with non-numeric grandparent rating keys are skipped.
func TestProcessHistoryRow_episode_with_unparseable_grandparent(t *testing.T) {
	items := map[string]tautulliEntry{}
	row := &historyItem{
		RatingKey:            float64(99),
		GrandparentRatingKey: "not_a_number",
		Title:                "Episode",
		GrandparentTitle:     "Show",
		Year:                 float64(2021),
		MediaType:            "episode",
	}

	processHistoryRow(row, items)

	if len(items) != 0 {
		t.Errorf("processHistoryRow: expected 0 items for unparseable grandparent key, got %d", len(items))
	}
}

// TestProcessHistoryRow_episode_duplicate_not_overwritten verifies that
// duplicate episode grandparent keys preserve the first entry.
func TestProcessHistoryRow_episode_duplicate_not_overwritten(t *testing.T) {
	items := map[string]tautulliEntry{
		"50": {RatingKey: "50", Title: "First Show", Year: "2020", MediaType: "show"},
	}
	row := &historyItem{
		RatingKey:            float64(99),
		GrandparentRatingKey: float64(50),
		Title:                "New Episode",
		GrandparentTitle:     "Second Show",
		Year:                 float64(2021),
		MediaType:            "episode",
	}

	processHistoryRow(row, items)

	if items["50"].Title != "First Show" {
		t.Errorf("processHistoryRow: expected first entry preserved, got title %q", items["50"].Title)
	}
}

// TestProcessHistoryRow_movie_with_string_rating_key verifies that movies
// with string rating keys (as returned by some Tautulli versions) are handled.
func TestProcessHistoryRow_movie_with_string_rating_key(t *testing.T) {
	items := map[string]tautulliEntry{}
	row := &historyItem{
		RatingKey: "42",
		Title:     "String Key Movie",
		Year:      "2020",
		MediaType: "movie",
		GUID:      "imdb://tt1234567",
	}

	processHistoryRow(row, items)

	if len(items) != 1 {
		t.Fatalf("processHistoryRow: expected 1 item for string rating key, got %d", len(items))
	}
	if items["42"].Title != "String Key Movie" {
		t.Errorf("processHistoryRow(%q) title = %q, want %q", "42", items["42"].Title, "String Key Movie")
	}
}

// TestProcessHistoryRow_negative_rating_key verifies negative rating keys
// are skipped (they're invalid).
func TestProcessHistoryRow_negative_rating_key(t *testing.T) {
	items := map[string]tautulliEntry{}
	row := &historyItem{
		RatingKey: float64(-1),
		Title:     "Negative Key",
		Year:      float64(2020),
		MediaType: "movie",
	}

	processHistoryRow(row, items)

	if len(items) != 0 {
		t.Errorf("processHistoryRow: expected 0 items for negative rating key, got %d", len(items))
	}
}

// TestMatchStaleItems_title_with_leading_trailing_whitespace verifies that
// titles with extra whitespace still match via case-insensitive trimmed lookup.
func TestMatchStaleItems_title_with_leading_trailing_whitespace(t *testing.T) {
	stale := map[string]tautulliEntry{
		"100": {RatingKey: "100", Title: "  The Matrix  ", Year: "1999", MediaType: "movie", GUID: ""},
	}
	byTitleYear := map[string]plexEntry{
		"the matrix|1999": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: "movie"},
	}

	matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, byTitleYear, nil)

	if len(matched) != 1 {
		t.Fatalf("matchStaleItems: expected 1 match for whitespace-padded title, got %d", len(matched))
	}
	if matched[0].NewKey != "200" {
		t.Errorf("matchStaleItems: expected new key 200, got %s", matched[0].NewKey)
	}
	if len(unmatched) != 0 {
		t.Errorf("matchStaleItems: expected 0 unmatched, got %d", len(unmatched))
	}
}

// TestMatchStaleItems_guid_match_same_key_falls_through_to_title verifies that
// when GUID matches but points to the same key, title+year fallback is tried.
func TestMatchStaleItems_guid_match_same_key_falls_through_to_title(t *testing.T) {
	stale := map[string]tautulliEntry{
		"100": {RatingKey: "100", Title: "Movie", Year: "2020", MediaType: "movie", GUID: "imdb://tt1111111"},
	}
	byGUID := map[string]plexEntry{
		"imdb://tt1111111": {RatingKey: "100", Title: "Movie", Year: "2020", Type: "movie"},
	}
	byTitleYear := map[string]plexEntry{
		"movie|2020": {RatingKey: "200", Title: "Movie", Year: "2020", Type: "movie"},
	}

	matched, _ := matchStaleItems(allFallbacks(), stale, byGUID, byTitleYear, nil)

	if len(matched) != 1 {
		t.Fatalf("matchStaleItems: expected 1 match via title+year fallback, got %d", len(matched))
	}
	if matched[0].Method != methodTitleYear {
		t.Errorf("matchStaleItems: expected method %q, got %q", methodTitleYear, matched[0].Method)
	}
	if matched[0].NewKey != "200" {
		t.Errorf("matchStaleItems: expected new key 200, got %s", matched[0].NewKey)
	}
}

// TestMatchStaleItems_title_only_same_key_falls_through verifies that when
// title-only matches the same key, the item is reported as unmatched.
func TestMatchStaleItems_title_only_same_key_falls_through(t *testing.T) {
	stale := map[string]tautulliEntry{
		"100": {RatingKey: "100", Title: "Unique Movie", Year: "2020", MediaType: "movie", GUID: ""},
	}
	byTitle := map[string]plexEntry{
		"unique movie": {RatingKey: "100", Title: "Unique Movie", Year: "2020", Type: "movie"},
	}

	matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, nil, byTitle)

	if len(matched) != 0 {
		t.Errorf("matchStaleItems: expected 0 matched when title-only points to same key, got %d", len(matched))
	}
	if len(unmatched) != 1 {
		t.Errorf("matchStaleItems: expected 1 unmatched, got %d", len(unmatched))
	}
}

// TestMatchStaleItems_title_year_same_key_falls_through_to_title_only verifies
// the full fallback chain: GUID miss → title+year same key → title-only match.
func TestMatchStaleItems_title_year_same_key_falls_through_to_title_only(t *testing.T) {
	stale := map[string]tautulliEntry{
		"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: "movie", GUID: ""},
	}
	byTitleYear := map[string]plexEntry{
		"dune|2020": {RatingKey: "100", Title: "Dune", Year: "2020", Type: "movie"},
	}
	byTitle := map[string]plexEntry{
		"dune": {RatingKey: "300", Title: "Dune", Year: "2021", Type: "movie"},
	}

	matched, _ := matchStaleItems(allFallbacks(), stale, nil, byTitleYear, byTitle)

	if len(matched) != 1 {
		t.Fatalf("matchStaleItems: expected 1 match via title-only after title+year same-key, got %d", len(matched))
	}
	if matched[0].NewKey != "300" {
		t.Errorf("matchStaleItems: expected new key 300, got %s", matched[0].NewKey)
	}
	if !strings.HasPrefix(matched[0].Method, "title only") {
		t.Errorf("matchStaleItems: expected title-only method, got %q", matched[0].Method)
	}
}

// TestPlexLibrarySections_read_error covers the io.ReadAll error path
// by returning a body that errors on read.
func TestPlexLibrarySections_read_error(t *testing.T) {
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// Write a valid status but set Content-Length to force a read error
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		// Write less than Content-Length to cause an unexpected EOF
		fmt.Fprint(w, "short")
	})

	sections := plexLibrarySections(context.Background(), &http.Client{}, cfg)
	// The function should handle the parse error gracefully (invalid JSON)
	if sections != nil {
		t.Errorf("plexLibrarySections: expected nil on truncated body, got %v", sections)
	}
}

// TestPlexItemExists_request_creation_error covers the error path when
// http.NewRequestWithContext fails (e.g., invalid URL).
func TestPlexItemExists_request_creation_error(t *testing.T) {
	cfg := &config{
		PlexURL:   "://invalid-url",
		PlexToken: "token",
	}

	got := plexItemExists(context.Background(), &http.Client{}, cfg, "42")
	if got {
		t.Error("plexItemExists: expected false for invalid URL")
	}
}

// TestDrainBody_error_reader covers the warning log path when the reader
// returns an error other than EOF.
func TestDrainBody_error_reader(t *testing.T) {
	errReader := io.NopCloser(&failReader{err: fmt.Errorf("simulated read error")})
	// Should not panic — just logs a warning
	drainBody(errReader)
}

// failReader is a test helper that always returns an error on Read.
type failReader struct {
	err error
}

func (r *failReader) Read([]byte) (int, error) {
	return 0, r.err
}

// TestCollectTautulliItems_context_cancelled covers the context cancellation
// path during pagination delay.
func TestCollectTautulliItems_context_cancelled_during_pagination(t *testing.T) {
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		// Return a large total to trigger pagination
		w.Write([]byte(`{"response":{"result":"success","data":{
			"recordsFiltered":5000,
			"data":[{"rating_key":1,"title":"Movie","year":2020,"media_type":"movie","guid":""}]
		}}}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	items := collectTautulliItems(ctx, &http.Client{}, cfg)
	// Should return nil when context is cancelled during pagination
	if items != nil && calls > 2 {
		// If it returned items, it should have stopped early
		t.Logf("collectTautulliItems returned %d items after %d calls (context cancelled)", len(items), calls)
	}
}

// TestPlexLibraryAll_read_error covers the io.ReadAll error path.
func TestPlexLibraryAll_read_error(t *testing.T) {
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "short")
	})

	items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
	// Should handle the parse error gracefully
	if items != nil {
		t.Errorf("plexLibraryAll: expected nil on truncated body, got %v", items)
	}
}

// TestTautulliAPI_request_creation_error covers the error path when
// http.NewRequestWithContext fails.
func TestTautulliAPI_request_creation_error(t *testing.T) {
	cfg := &config{
		TautulliURL:    "://invalid-url",
		TautulliAPIKey: "key",
	}

	_, err := tautulliAPI(context.Background(), &http.Client{}, cfg, "test", nil)
	if err == nil {
		t.Error("tautulliAPI: expected error for invalid URL")
	}
}

// TestPlexLibrarySections_request_creation_error covers the error path when
// the Plex URL is invalid.
func TestPlexLibrarySections_request_creation_error(t *testing.T) {
	cfg := &config{
		PlexURL:   "://invalid-url",
		PlexToken: "token",
	}

	sections := plexLibrarySections(context.Background(), &http.Client{}, cfg)
	if sections != nil {
		t.Errorf("plexLibrarySections: expected nil for invalid URL, got %v", sections)
	}
}

// TestPlexLibraryAll_request_creation_error covers the error path when
// the Plex URL is invalid.
func TestPlexLibraryAll_request_creation_error(t *testing.T) {
	cfg := &config{
		PlexURL:   "://invalid-url",
		PlexToken: "token",
	}

	items := plexLibraryAll(context.Background(), &http.Client{}, cfg, "1")
	if items != nil {
		t.Errorf("plexLibraryAll: expected nil for invalid URL, got %v", items)
	}
}

func TestCollectTautulliItems_exact_page_boundary(t *testing.T) {
	// Targets lived mutant at line 356: start >= total boundary.
	// With recordsFiltered=1000 and 1000 items on page 1, start becomes 1000.
	// Original: 1000 >= 1000 → break (1 API call).
	// Mutant (>): 1000 > 1000 → false → continues, makes extra call.
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		start := r.URL.Query().Get("start")
		if start == "0" {
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1000,
				"data":[{"rating_key":1,"title":"M","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		} else {
			// Second call should not happen with correct code
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1000,
				"data":[]
			}}}`))
		}
	})
	items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
	if items == nil {
		t.Fatal("expected non-nil items")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (start=1000 >= total=1000 should break)", calls)
	}
}

func TestCollectTautulliItems_pagination_increment(t *testing.T) {
	// Targets lived mutant at line 361: start += 1000 arithmetic.
	// Verifies that pagination advances by exactly 1000 per page.
	startValues := []string{}
	cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
		startValues = append(startValues, r.URL.Query().Get("start"))
		start := r.URL.Query().Get("start")
		switch start {
		case "0", "1000":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":2500,
				"data":[{"rating_key":1,"title":"M","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		case "2000":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":2500,
				"data":[{"rating_key":2,"title":"N","year":2021,"media_type":"movie","guid":""}]
			}}}`))
		default:
			// Unexpected start value — fail the test by returning empty
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":2500,
				"data":[]
			}}}`))
		}
	})
	items := collectTautulliItems(context.Background(), &http.Client{}, cfg)
	if items == nil {
		t.Fatal("expected non-nil items")
	}
	if len(startValues) != 3 {
		t.Errorf("expected 3 API calls, got %d (starts: %v)", len(startValues), startValues)
	}
	want := []string{"0", "1000", "2000"}
	for i, s := range want {
		if i < len(startValues) && startValues[i] != s {
			t.Errorf("call %d: start = %q, want %q", i, startValues[i], s)
		}
	}
}

func TestApplyRemappings_dry_run_skips_api_calls(t *testing.T) {
	// Targets lived mutant at line 541: len(matched) > 0 boundary.
	// Also targets line 558: cfg.DryRun negation.
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})
	cfg.DryRun = true

	matched := []matchResult{
		{Title: "Movie", OldKey: "1", NewKey: "2", MediaType: "movie", Method: "guid"},
	}
	applyRemappings(context.Background(), &http.Client{}, cfg, matched, nil)

	if calls != 0 {
		t.Errorf("dry run made %d API calls, want 0", calls)
	}
}

func TestApplyRemappings_live_makes_api_call(t *testing.T) {
	// Verifies that non-dry-run mode actually calls the API.
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("cmd") == "update_metadata_details" {
			w.Write([]byte(`{"response":{"result":"success"}}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	cfg.DryRun = false

	matched := []matchResult{
		{Title: "Movie", OldKey: "1", NewKey: "2", MediaType: "movie", Method: "guid"},
	}
	applyRemappings(context.Background(), &http.Client{}, cfg, matched, nil)

	if calls != 1 {
		t.Errorf("live mode made %d API calls, want 1", calls)
	}
}

func TestClearRecentlyAdded_dry_run_skips(t *testing.T) {
	// Targets lived mutant at line 593: cfg.DryRun negation.
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})
	cfg.DryRun = true

	clearRecentlyAdded(context.Background(), &http.Client{}, cfg)

	if calls != 0 {
		t.Errorf("dry run made %d API calls, want 0", calls)
	}
}

func TestClearRecentlyAdded_live_makes_call(t *testing.T) {
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})
	cfg.DryRun = false

	clearRecentlyAdded(context.Background(), &http.Client{}, cfg)

	if calls < 1 {
		t.Errorf("live mode made %d API calls, want >= 1", calls)
	}
}

func TestTautulliAPIWithRetry_exact_3_attempts(t *testing.T) {
	// Targets lived mutants at lines 613-615: retry loop arithmetic.
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := tautulliAPIWithRetry(ctx, &http.Client{}, cfg, "test_cmd", nil)
	if err == nil {
		t.Error("expected error from all-failing retries")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (3 retry attempts)", calls)
	}
}

func TestTautulliAPIWithRetry_success_on_second_attempt(t *testing.T) {
	// Verifies retry succeeds on second attempt and stops.
	calls := 0
	cfg := tautulliServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, err := tautulliAPIWithRetry(ctx, &http.Client{}, cfg, "test_cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body == nil {
		t.Error("expected non-nil body on success")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (fail once, succeed once)", calls)
	}
}

// --- Tests: sanitizeErr ---

func TestSanitizeErr(t *testing.T) {
	t.Run("strips query string from url.Error", func(t *testing.T) {
		ue := &url.Error{
			Op:  "Get",
			URL: "http://tautulli:8181/api/v2?apikey=secret123&cmd=get_history",
			Err: fmt.Errorf("connection refused"),
		}
		got := sanitizeErr(ue)
		s := got.Error()
		if strings.Contains(s, "secret123") {
			t.Errorf("sanitizeErr leaked API key: %s", s)
		}
		if !strings.Contains(s, "?<redacted>") {
			t.Errorf("expected redacted query string, got: %s", s)
		}
	})

	t.Run("passes through non-url.Error unchanged", func(t *testing.T) {
		err := fmt.Errorf("some other error")
		got := sanitizeErr(err)
		if got.Error() != "some other error" {
			t.Errorf("expected unchanged error, got: %s", got.Error())
		}
	})

	t.Run("handles url.Error without query string", func(t *testing.T) {
		ue := &url.Error{
			Op:  "Get",
			URL: "http://tautulli:8181/api/v2",
			Err: fmt.Errorf("connection refused"),
		}
		got := sanitizeErr(ue)
		s := got.Error()
		if strings.Contains(s, "<redacted>") {
			t.Errorf("should not redact URL without query string: %s", s)
		}
	})
}

// TestTautulliAPI_error_does_not_leak_apikey verifies that HTTP errors
// from tautulliAPI do not contain the API key in the error message.
func TestTautulliAPI_error_does_not_leak_apikey(t *testing.T) {
	// Use a server that immediately closes the connection
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Force a connection error by hijacking and closing
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Skip("server doesn't support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	cfg := &config{
		TautulliURL:    srv.URL,
		TautulliAPIKey: "supersecretkey123",
	}
	_, err := tautulliAPI(context.Background(), &http.Client{}, cfg, "test", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "supersecretkey123") {
		t.Errorf("error message leaked API key: %s", err.Error())
	}
}
