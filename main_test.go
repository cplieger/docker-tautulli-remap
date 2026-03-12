package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

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
