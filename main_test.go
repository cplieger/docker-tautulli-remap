package main

import (
	"testing"
)

func TestNormalizeGUID(t *testing.T) {
	tests := []struct {
		name string
		guid string
		want string
	}{
		{
			name: "imdb with query params",
			guid: "com.plexapp.agents.imdb://tt1234567?lang=en",
			want: "imdb://tt1234567",
		},
		{
			name: "bare imdb",
			guid: "imdb://tt9999999",
			want: "imdb://tt9999999",
		},
		{
			name: "themoviedb legacy agent",
			guid: "com.plexapp.agents.themoviedb://12345?lang=en",
			want: "tmdb://12345",
		},
		{
			name: "tmdb new agent",
			guid: "tmdb://67890",
			want: "tmdb://67890",
		},
		{
			name: "thetvdb legacy with season path",
			guid: "com.plexapp.agents.thetvdb://271557/3/1?lang=en",
			want: "tvdb://271557",
		},
		{
			name: "tvdb new agent",
			guid: "tvdb://271557",
			want: "tvdb://271557",
		},
		{
			name: "plex agent movie",
			guid: "plex://movie/5d776b59ad5437001f79c6f8",
			want: "plex://movie/5d776b59ad5437001f79c6f8",
		},
		{
			name: "plex agent episode",
			guid: "plex://episode/5d9c135046115600200d30a2",
			want: "plex://episode/5d9c135046115600200d30a2",
		},
		{
			name: "mbid music",
			guid: "mbid://abcdef01-2345-6789-abcd-ef0123456789",
			want: "mbid://abcdef01-2345-6789-abcd-ef0123456789",
		},
		{
			name: "local unsupported",
			guid: "local://616507",
			want: "",
		},
		{
			name: "agents.none unsupported",
			guid: "com.plexapp.agents.none://632d404bf27d52a513ccd45e4df820cd276f3090?lang=xn",
			want: "",
		},
		{
			name: "empty string",
			guid: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeGUID(tt.guid)
			if got != tt.want {
				t.Errorf("normalizeGUID(%q) = %q, want %q", tt.guid, got, tt.want)
			}
		})
	}
}

func TestExtractAfter(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		prefix string
		want   string
	}{
		{
			name:   "simple extraction",
			s:      "imdb://tt1234567",
			prefix: "imdb://",
			want:   "tt1234567",
		},
		{
			name:   "with query params",
			s:      "com.plexapp.agents.imdb://tt1234567?lang=en",
			prefix: "imdb://",
			want:   "tt1234567",
		},
		{
			name:   "prefix not found",
			s:      "tmdb://12345",
			prefix: "imdb://",
			want:   "",
		},
		{
			name:   "empty input",
			s:      "",
			prefix: "imdb://",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAfter(tt.s, tt.prefix)
			if got != tt.want {
				t.Errorf("extractAfter(%q, %q) = %q, want %q", tt.s, tt.prefix, got, tt.want)
			}
		})
	}
}

// allFallbacks returns a config with both fallback strategies enabled.
func allFallbacks() *config {
	return &config{FallbackTitleYear: true, FallbackTitleOnly: true}
}

func TestMatchStaleItems(t *testing.T) {
	t.Run("guid match takes priority", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "The Matrix", Year: "1999",
				MediaType: "movie", GUID: "imdb://tt0133093",
			},
		}
		byGUID := map[string]plexEntry{
			"imdb://tt0133093": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: "movie"},
		}
		byTitleYear := map[string]plexEntry{
			"the matrix|1999": {RatingKey: "300", Title: "The Matrix", Year: "1999", Type: "movie"},
		}
		byTitle := map[string]plexEntry{}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, byGUID, byTitleYear, byTitle)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].NewKey != "200" {
			t.Errorf("expected GUID match key 200, got %s", matched[0].NewKey)
		}
		if matched[0].Method != "guid" {
			t.Errorf("expected method 'guid', got %s", matched[0].Method)
		}
		if len(unmatched) != 0 {
			t.Errorf("expected 0 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("title+year fallback when no guid", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "Inception", Year: "2010",
				MediaType: "movie", GUID: "",
			},
		}
		byGUID := map[string]plexEntry{}
		byTitleYear := map[string]plexEntry{
			"inception|2010": {RatingKey: "200", Title: "Inception", Year: "2010", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, byGUID, byTitleYear, nil)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].NewKey != "200" {
			t.Errorf("expected key 200, got %s", matched[0].NewKey)
		}
		if matched[0].Method != "title+year" {
			t.Errorf("expected method 'title+year', got %s", matched[0].Method)
		}
		if len(unmatched) != 0 {
			t.Errorf("expected 0 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("title-only fallback with matching type", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "Dune", Year: "2020",
				MediaType: "movie", GUID: "",
			},
		}
		byTitle := map[string]plexEntry{
			"dune": {RatingKey: "200", Title: "Dune", Year: "2021", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, nil, byTitle)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].NewKey != "200" {
			t.Errorf("expected key 200, got %s", matched[0].NewKey)
		}
		if matched[0].Method != "title only (2020 -> 2021)" {
			t.Errorf("unexpected method: %s", matched[0].Method)
		}
		if len(unmatched) != 0 {
			t.Errorf("expected 0 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("no match produces unmatched", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "Nonexistent Movie", Year: "2025",
				MediaType: "movie", GUID: "imdb://tt0000000",
			},
		}

		matched, unmatched := matchStaleItems(allFallbacks(), stale, nil, nil, nil)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched, got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Fatalf("expected 1 unmatched, got %d", len(unmatched))
		}
		if unmatched[0].OldKey != "100" {
			t.Errorf("expected old key 100, got %s", unmatched[0].OldKey)
		}
	})

	t.Run("same key not remapped", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"200": {
				RatingKey: "200", Title: "The Matrix", Year: "1999",
				MediaType: "movie", GUID: "imdb://tt0133093",
			},
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

	t.Run("case insensitive title matching", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "THE MATRIX", Year: "1999",
				MediaType: "movie", GUID: "",
			},
		}
		byTitleYear := map[string]plexEntry{
			"the matrix|1999": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: "movie"},
		}

		matched, _ := matchStaleItems(allFallbacks(), stale, nil, byTitleYear, nil)

		if len(matched) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matched))
		}
		if matched[0].NewKey != "200" {
			t.Errorf("expected key 200, got %s", matched[0].NewKey)
		}
	})

	t.Run("title-only rejects type mismatch", func(t *testing.T) {
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "Home Alone", Year: "2025",
				MediaType: "show", GUID: "",
			},
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

	t.Run("title+year disabled skips fallback", func(t *testing.T) {
		cfg := &config{FallbackTitleYear: false, FallbackTitleOnly: false}
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "Inception", Year: "2010",
				MediaType: "movie", GUID: "",
			},
		}
		byTitleYear := map[string]plexEntry{
			"inception|2010": {RatingKey: "200", Title: "Inception", Year: "2010", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(cfg, stale, nil, byTitleYear, nil)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched (fallback disabled), got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Errorf("expected 1 unmatched, got %d", len(unmatched))
		}
	})

	t.Run("title-only disabled skips fallback", func(t *testing.T) {
		cfg := &config{FallbackTitleYear: true, FallbackTitleOnly: false}
		stale := map[string]tautulliEntry{
			"100": {
				RatingKey: "100", Title: "Dune", Year: "2020",
				MediaType: "movie", GUID: "",
			},
		}
		byTitle := map[string]plexEntry{
			"dune": {RatingKey: "200", Title: "Dune", Year: "2021", Type: "movie"},
		}

		matched, unmatched := matchStaleItems(cfg, stale, nil, nil, byTitle)

		if len(matched) != 0 {
			t.Errorf("expected 0 matched (title-only disabled), got %d", len(matched))
		}
		if len(unmatched) != 1 {
			t.Errorf("expected 1 unmatched, got %d", len(unmatched))
		}
	})
}
