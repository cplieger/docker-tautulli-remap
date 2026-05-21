package remap

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestNormalizeGUID(t *testing.T) {
	tests := []struct {
		name string
		guid string
		want string
	}{
		{"imdb legacy with query params", "com.plexapp.agents.imdb://tt1234567?lang=en", "imdb://tt1234567"},
		{"imdb bare", "imdb://tt9999999", "imdb://tt9999999"},
		{"themoviedb legacy agent", "com.plexapp.agents.themoviedb://12345?lang=en", "tmdb://12345"},
		{"tmdb new agent", "tmdb://67890", "tmdb://67890"},
		{"tmdb with query params", "tmdb://12345?lang=en", "tmdb://12345"},
		{"thetvdb legacy with season path", "com.plexapp.agents.thetvdb://271557/3/1?lang=en", "tvdb://271557"},
		{"thetvdb with deep path", "com.plexapp.agents.thetvdb://271557/3/1/5?lang=en", "tvdb://271557"},
		{"tvdb new agent", "tvdb://271557", "tvdb://271557"},
		{"plex movie", "plex://movie/5d776b59ad5437001f79c6f8", "plex://movie/5d776b59ad5437001f79c6f8"},
		{"plex episode", "plex://episode/5d9c135046115600200d30a2", "plex://episode/5d9c135046115600200d30a2"},
		{"mbid", "mbid://abcdef01-2345-6789-abcd-ef0123456789", "mbid://abcdef01-2345-6789-abcd-ef0123456789"},
		{"local unsupported", "local://616507", ""},
		{"agents.none unsupported", "com.plexapp.agents.none://632d404bf27d52a513ccd45e4df820cd276f3090?lang=xn", ""},
		{"unknown scheme", "custom://something", ""},
		{"empty string", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeGUID(tt.guid); got != tt.want {
				t.Errorf("NormalizeGUID(%q) = %q, want %q", tt.guid, got, tt.want)
			}
		})
	}
}

func TestExtractAfter(t *testing.T) {
	tests := []struct {
		s, prefix, want string
	}{
		{"imdb://tt1234567", "imdb://", "tt1234567"},
		{"com.plexapp.agents.imdb://tt1234567?lang=en", "imdb://", "tt1234567"},
		{"tmdb://12345", "imdb://", ""},
		{"", "imdb://", ""},
	}
	for _, tt := range tests {
		if got := ExtractAfter(tt.s, tt.prefix); got != tt.want {
			t.Errorf("ExtractAfter(%q, %q) = %q, want %q", tt.s, tt.prefix, got, tt.want)
		}
	}
}

func TestMatchOne(t *testing.T) {
	t.Run("guid match", func(t *testing.T) {
		item := &TautulliEntry{GUID: "imdb://tt1", Title: "M", Year: "2020", MediaType: Movie}
		byGUID := map[string]PlexEntry{"imdb://tt1": {RatingKey: "200"}}
		key, method := MatchOne(item, "100", byGUID, nil, nil, true, true)
		if key != "200" || method != MethodGUID {
			t.Errorf("got (%q, %q), want (200, guid)", key, method)
		}
	})

	t.Run("title+year fallback", func(t *testing.T) {
		item := &TautulliEntry{Title: "Movie", Year: "2020", MediaType: Movie}
		byTY := map[string]PlexEntry{"movie|2020": {RatingKey: "200"}}
		key, method := MatchOne(item, "100", nil, byTY, nil, true, true)
		if key != "200" || method != MethodTitleYear {
			t.Errorf("got (%q, %q), want (200, title+year)", key, method)
		}
	})

	t.Run("no match", func(t *testing.T) {
		item := &TautulliEntry{Title: "X", Year: "2020", MediaType: Movie}
		key, method := MatchOne(item, "100", nil, nil, nil, true, true)
		if key != "" || method != "" {
			t.Errorf("got (%q, %q), want empty", key, method)
		}
	})
}

func TestProcessHistoryRow(t *testing.T) {
	t.Run("movie with valid rating key", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 42, Title: "Test Movie",
			Year: 2020, MediaType: Movie,
			GUID: "imdb://tt1234567",
		}
		ProcessHistoryRow(row, items)
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items["42"].Title != "Test Movie" {
			t.Errorf("unexpected title: %s", items["42"].Title)
		}
	})

	t.Run("movie with zero rating key is skipped", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 0, Title: "Bad Movie",
			Year: 2020, MediaType: Movie,
		}
		ProcessHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("movie with zero FlexInt rating key is skipped", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 0, Title: "Bad Movie",
			Year: 2020, MediaType: Movie,
		}
		ProcessHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("episode uses grandparent rating key", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 99, GrandparentRatingKey: 50,
			Title: "Episode Title", GrandparentTitle: "Show Title",
			Year: 2021, MediaType: Episode,
			GUID: "tvdb://271557",
		}
		ProcessHistoryRow(row, items)
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		entry := items["50"]
		if entry.Title != "Show Title" {
			t.Errorf("expected grandparent title, got %q", entry.Title)
		}
		if entry.MediaType != Show {
			t.Errorf("expected media type 'show', got %q", entry.MediaType)
		}
	})

	t.Run("episode with zero grandparent key is skipped", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 99, GrandparentRatingKey: 0,
			Title: "Episode", MediaType: Episode,
		}
		ProcessHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("episode strips plex episode GUID", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 99, GrandparentRatingKey: 50,
			Title: "Ep", GrandparentTitle: "Show",
			Year: 2021, MediaType: Episode,
			GUID: "plex://episode/5d9c135046115600200d30a2",
		}
		dropped := ProcessHistoryRow(row, items)
		if !dropped {
			t.Error("expected dropped=true for plex episode GUID")
		}
		if items["50"].GUID != "" {
			t.Errorf("expected empty GUID for plex episode, got %q", items["50"].GUID)
		}
	})

	t.Run("episode falls back to episode title when grandparent empty", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 99, GrandparentRatingKey: 50,
			Title: "Fallback Title", GrandparentTitle: "",
			Year: 2021, MediaType: Episode,
		}
		ProcessHistoryRow(row, items)
		if items["50"].Title != "Fallback Title" {
			t.Errorf("expected fallback title, got %q", items["50"].Title)
		}
	})

	t.Run("duplicate key is not overwritten", func(t *testing.T) {
		items := map[string]TautulliEntry{
			"42": {RatingKey: "42", Title: "First", Year: "2020", MediaType: Movie},
		}
		row := &HistoryItem{
			RatingKey: 42, Title: "Second",
			Year: 2021, MediaType: Movie,
		}
		ProcessHistoryRow(row, items)
		if items["42"].Title != "First" {
			t.Errorf("expected first entry preserved, got %q", items["42"].Title)
		}
	})

	t.Run("unknown media type is ignored", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 42, Title: "Music",
			Year: 2020, MediaType: MediaType("track"),
		}
		ProcessHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("episode with zero grandparent FlexInt is skipped", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 99, GrandparentRatingKey: 0,
			Title: "Episode", GrandparentTitle: "Show",
			Year: 2021, MediaType: Episode,
		}
		ProcessHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("episode duplicate grandparent not overwritten", func(t *testing.T) {
		items := map[string]TautulliEntry{
			"50": {RatingKey: "50", Title: "First Show", Year: "2020", MediaType: Show},
		}
		row := &HistoryItem{
			RatingKey: 99, GrandparentRatingKey: 50,
			Title: "New Episode", GrandparentTitle: "Second Show",
			Year: 2021, MediaType: Episode,
		}
		ProcessHistoryRow(row, items)
		if items["50"].Title != "First Show" {
			t.Errorf("expected first entry preserved, got %q", items["50"].Title)
		}
	})

	t.Run("movie with FlexInt rating key", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: 42, Title: "String Key Movie",
			Year: 2020, MediaType: Movie, GUID: "imdb://tt1234567",
		}
		ProcessHistoryRow(row, items)
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items["42"].Title != "String Key Movie" {
			t.Errorf("title = %q, want %q", items["42"].Title, "String Key Movie")
		}
	})

	t.Run("negative rating key is skipped", func(t *testing.T) {
		items := map[string]TautulliEntry{}
		row := &HistoryItem{
			RatingKey: FlexInt(-1), Title: "Negative Key",
			Year: 2020, MediaType: Movie,
		}
		ProcessHistoryRow(row, items)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
	})
}

func TestFlexIntUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want FlexInt
	}{
		{"float", "42.0", 42},
		{"string", `"123"`, 123},
		{"empty string", `""`, 0},
		{"null", "null", 0},
		{"invalid string", `"abc"`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexInt
			if err := json.Unmarshal([]byte(tt.json), &f); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if f != tt.want {
				t.Errorf("got %d, want %d", f, tt.want)
			}
		})
	}
}

func TestMediaType(t *testing.T) {
	if Movie.String() != "movie" {
		t.Errorf("Movie.String() = %q", Movie.String())
	}
	var m MediaType
	if err := m.UnmarshalText([]byte("show")); err != nil {
		t.Fatal(err)
	}
	if m != Show {
		t.Errorf("got %q", m)
	}
	if err := m.UnmarshalText([]byte("invalid")); err == nil {
		t.Error("expected error for invalid media type")
	}
}

func TestMatchMethod(t *testing.T) {
	if MethodGUID.String() != "guid" {
		t.Errorf("MethodGUID.String() = %q", MethodGUID.String())
	}
}

func TestRatingKeyIsValid(t *testing.T) {
	tests := []struct {
		input RatingKey
		want  bool
	}{
		{"42", true},
		{"0", true},
		{"", false},
		{"-1", false},
		{"abc", false},
	}
	for _, tt := range tests {
		if got := tt.input.IsValid(); got != tt.want {
			t.Errorf("RatingKey(%q).IsValid() = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestMatchStaleItems(t *testing.T) {
	type check func(t *testing.T, matched []MatchResult, unmatched []UnmatchResult)

	matchCount := func(wantMatched, wantUnmatched int) check {
		return func(t *testing.T, matched []MatchResult, unmatched []UnmatchResult) {
			t.Helper()
			if len(matched) != wantMatched {
				t.Errorf("matched: got %d, want %d", len(matched), wantMatched)
			}
			if len(unmatched) != wantUnmatched {
				t.Errorf("unmatched: got %d, want %d", len(unmatched), wantUnmatched)
			}
		}
	}
	matchKey := func(idx int, key string) check {
		return func(t *testing.T, matched []MatchResult, _ []UnmatchResult) {
			t.Helper()
			if idx >= len(matched) {
				t.Fatalf("matched[%d] out of range (len=%d)", idx, len(matched))
			}
			if matched[idx].NewKey != key {
				t.Errorf("matched[%d].NewKey = %q, want %q", idx, matched[idx].NewKey, key)
			}
		}
	}
	matchMethod := func(idx int, method MatchMethod) check {
		return func(t *testing.T, matched []MatchResult, _ []UnmatchResult) {
			t.Helper()
			if idx >= len(matched) {
				return
			}
			if matched[idx].Method != method {
				t.Errorf("matched[%d].Method = %q, want %q", idx, matched[idx].Method, method)
			}
		}
	}
	matchMethodPrefix := func(idx int, prefix string) check {
		return func(t *testing.T, matched []MatchResult, _ []UnmatchResult) {
			t.Helper()
			if idx >= len(matched) {
				return
			}
			if !strings.HasPrefix(string(matched[idx].Method), prefix) {
				t.Errorf("matched[%d].Method = %q, want prefix %q", idx, matched[idx].Method, prefix)
			}
		}
	}
	unmatchedKey := func(idx int, key string) check {
		return func(t *testing.T, _ []MatchResult, unmatched []UnmatchResult) {
			t.Helper()
			if idx >= len(unmatched) {
				return
			}
			if unmatched[idx].OldKey != key {
				t.Errorf("unmatched[%d].OldKey = %q, want %q", idx, unmatched[idx].OldKey, key)
			}
		}
	}

	tests := []struct {
		name        string
		stale       map[string]TautulliEntry
		byGUID      map[string]PlexEntry
		byTitleYear map[string]PlexEntry
		byTitle     map[string]PlexEntry
		enableTY    bool
		enableTO    bool
		checks      []check
	}{
		{
			name:        "guid match takes priority over title+year",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "The Matrix", Year: "1999", MediaType: Movie, GUID: "imdb://tt0133093"}},
			byGUID:      map[string]PlexEntry{"imdb://tt0133093": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
			byTitleYear: map[string]PlexEntry{"the matrix|1999": {RatingKey: "300", Title: "The Matrix", Year: "1999", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200"), matchMethod(0, MethodGUID)},
		},
		{
			name:        "title+year fallback when no guid",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Inception", Year: "2010", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"inception|2010": {RatingKey: "200", Title: "Inception", Year: "2010", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200"), matchMethod(0, MethodTitleYear)},
		},
		{
			name:     "title-only fallback with matching type",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: Movie}},
			byTitle:  map[string]PlexEntry{"dune": {RatingKey: "200", Title: "Dune", Year: "2021", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(1, 0), matchMethodPrefix(0, "title only")},
		},
		{
			name:     "title-only rejects type mismatch",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Home Alone", Year: "2025", MediaType: Show}},
			byTitle:  map[string]PlexEntry{"home alone": {RatingKey: "200", Title: "Home Alone", Year: "1990", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:        "case insensitive title matching",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "THE MATRIX", Year: "1999", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"the matrix|1999": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200")},
		},
		{
			name:     "same key is not remapped",
			stale:    map[string]TautulliEntry{"200": {RatingKey: "200", Title: "The Matrix", Year: "1999", MediaType: Movie, GUID: "imdb://tt0133093"}},
			byGUID:   map[string]PlexEntry{"imdb://tt0133093": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:     "no match produces unmatched",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Nonexistent", Year: "2025", MediaType: Movie, GUID: "imdb://tt0000000"}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1), unmatchedKey(0, "100")},
		},
		{
			name:        "title+year disabled skips fallback",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Inception", Year: "2010", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"inception|2010": {RatingKey: "200", Title: "Inception", Year: "2010", Type: Movie}},
			enableTY:    false, enableTO: false,
			checks: []check{matchCount(0, 1)},
		},

		{
			name:     "title-only disabled skips fallback",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: Movie}},
			byTitle:  map[string]PlexEntry{"dune": {RatingKey: "200", Title: "Dune", Year: "2021", Type: Movie}},
			enableTY: true, enableTO: false,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:        "whitespace-only title does not match",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "   ", Year: "2020", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"|2020": {RatingKey: "200", Title: "", Year: "2020", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:     "empty GUID skips GUID matching",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Test", Year: "2020", MediaType: Movie}},
			byGUID:   map[string]PlexEntry{"": {RatingKey: "200", Title: "Test", Year: "2020", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			name: "multiple stale items mixed results",
			stale: map[string]TautulliEntry{
				"100": {RatingKey: "100", Title: "Movie A", Year: "2020", MediaType: Movie, GUID: "imdb://tt1111111"},
				"200": {RatingKey: "200", Title: "Movie B", Year: "2021", MediaType: Movie, GUID: "imdb://tt2222222"},
				"300": {RatingKey: "300", Title: "Movie C", Year: "2022", MediaType: Movie, GUID: "imdb://tt3333333"},
			},
			byGUID:      map[string]PlexEntry{"imdb://tt1111111": {RatingKey: "101", Title: "Movie A", Year: "2020", Type: Movie}},
			byTitleYear: map[string]PlexEntry{"movie c|2022": {RatingKey: "301", Title: "Movie C", Year: "2022", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{
				matchCount(2, 1),
				unmatchedKey(0, "200"),
			},
		},
		{
			name:        "title with leading trailing whitespace",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "  The Matrix  ", Year: "1999", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"the matrix|1999": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200")},
		},
		{
			name:        "guid match same key falls through to title",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Movie", Year: "2020", MediaType: Movie, GUID: "imdb://tt1111111"}},
			byGUID:      map[string]PlexEntry{"imdb://tt1111111": {RatingKey: "100", Title: "Movie", Year: "2020", Type: Movie}},
			byTitleYear: map[string]PlexEntry{"movie|2020": {RatingKey: "200", Title: "Movie", Year: "2020", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200"), matchMethod(0, MethodTitleYear)},
		},
		{
			name:     "title only same key falls through to unmatched",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Unique Movie", Year: "2020", MediaType: Movie}},
			byTitle:  map[string]PlexEntry{"unique movie": {RatingKey: "100", Title: "Unique Movie", Year: "2020", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:        "title year same key falls through to title only",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"dune|2020": {RatingKey: "100", Title: "Dune", Year: "2020", Type: Movie}},
			byTitle:     map[string]PlexEntry{"dune": {RatingKey: "300", Title: "Dune", Year: "2021", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "300"), matchMethodPrefix(0, "title only")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, unmatched := MatchStaleItems(tt.stale, tt.byGUID, tt.byTitleYear, tt.byTitle, tt.enableTY, tt.enableTO)
			for _, c := range tt.checks {
				c(t, matched, unmatched)
			}
		})
	}
}

// --- Property-based tests ---

func TestNormalizeGUID_idempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.SampledFrom([]string{
			"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://",
			"themoviedb://", "thetvdb://",
			"local://", "com.plexapp.agents.none://", "",
		}).Draw(t, "prefix")
		suffix := rapid.StringMatching(`[a-zA-Z0-9/_\-]{0,40}`).Draw(t, "suffix")
		guid := prefix + suffix
		first := NormalizeGUID(guid)
		second := NormalizeGUID(first)
		if first != second {
			t.Errorf("not idempotent: NormalizeGUID(%q)=%q, NormalizeGUID(%q)=%q", guid, first, first, second)
		}
	})
}

func TestNormalizeGUID_never_panics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		guid := rapid.String().Draw(t, "guid")
		_ = NormalizeGUID(guid)
	})
}

func TestNormalizeGUID_output_uses_canonical_prefix(t *testing.T) {
	canonicalPrefixes := []string{"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://"}
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.SampledFrom([]string{
			"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://",
			"themoviedb://", "thetvdb://",
		}).Draw(t, "prefix")
		id := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "id")
		guid := prefix + id
		result := NormalizeGUID(guid)
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
			t.Errorf("NormalizeGUID(%q) = %q, no canonical prefix", guid, result)
		}
	})
}

func TestNormalizeGUID_tvdb_strips_deep_paths(t *testing.T) {
	tests := []struct {
		name, guid, want string
	}{
		{"tvdb preserves path (new agent)", "tvdb://271557/3", "tvdb://271557/3"},
		{"tvdb with no path", "tvdb://271557", "tvdb://271557"},
		{"thetvdb strips path (legacy agent)", "thetvdb://12345/1/2?lang=en", "tvdb://12345"},
		{"thetvdb strips single path segment", "thetvdb://99999/1?lang=en", "tvdb://99999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeGUID(tt.guid); got != tt.want {
				t.Errorf("NormalizeGUID(%q) = %q, want %q", tt.guid, got, tt.want)
			}
		})
	}
}

func TestExtractAfter_never_panics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		prefix := rapid.String().Draw(t, "prefix")
		_ = ExtractAfter(s, prefix)
	})
}

func TestExtractAfter_strips_query_params(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.SampledFrom([]string{"imdb://", "tmdb://", "tvdb://"}).Draw(t, "prefix")
		id := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "id")
		query := rapid.StringMatching(`[a-z]{1,10}=[a-z]{1,10}`).Draw(t, "query")
		input := prefix + id + "?" + query
		result := ExtractAfter(input, prefix)
		if strings.Contains(result, "?") {
			t.Errorf("ExtractAfter(%q, %q) = %q, still contains query params", input, prefix, result)
		}
		if result != id {
			t.Errorf("ExtractAfter(%q, %q) = %q, want %q", input, prefix, result, id)
		}
	})
}

func TestMatchStaleItems_partition_property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 10).Draw(t, "n")
		stale := map[string]TautulliEntry{}
		byGUID := map[string]PlexEntry{}
		byTitleYear := map[string]PlexEntry{}
		byTitle := map[string]PlexEntry{}

		for i := range n {
			key := strconv.Itoa(100 + i)
			title := rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(t, fmt.Sprintf("title_%d", i))
			year := strconv.Itoa(rapid.IntRange(1990, 2025).Draw(t, fmt.Sprintf("year_%d", i)))
			mediaType := MediaType(rapid.SampledFrom([]string{"movie", "show"}).Draw(t, fmt.Sprintf("type_%d", i)))
			guid := ""
			if rapid.Bool().Draw(t, fmt.Sprintf("has_guid_%d", i)) {
				guid = "imdb://tt" + strconv.Itoa(rapid.IntRange(1000000, 9999999).Draw(t, fmt.Sprintf("guid_%d", i)))
			}
			stale[key] = TautulliEntry{RatingKey: key, Title: title, Year: year, MediaType: mediaType, GUID: guid}

			if guid != "" && rapid.Bool().Draw(t, fmt.Sprintf("in_guid_map_%d", i)) {
				newKey := strconv.Itoa(200 + i)
				byGUID[guid] = PlexEntry{RatingKey: newKey, Title: title, Year: year, Type: mediaType}
			}
			t2 := strings.ToLower(strings.TrimSpace(title))
			if t2 != "" && rapid.Bool().Draw(t, fmt.Sprintf("in_ty_map_%d", i)) {
				newKey := strconv.Itoa(300 + i)
				byTitleYear[t2+"|"+year] = PlexEntry{RatingKey: newKey, Title: title, Year: year, Type: mediaType}
			}
			if t2 != "" && rapid.Bool().Draw(t, fmt.Sprintf("in_t_map_%d", i)) {
				newKey := strconv.Itoa(400 + i)
				byTitle[t2] = PlexEntry{RatingKey: newKey, Title: title, Year: year, Type: mediaType}
			}
		}

		matched, unmatched := MatchStaleItems(stale, byGUID, byTitleYear, byTitle, true, true)

		seen := map[string]bool{}
		for _, m := range matched {
			if seen[m.OldKey] {
				t.Errorf("duplicate matched key %s", m.OldKey)
			}
			seen[m.OldKey] = true
		}
		for _, u := range unmatched {
			if seen[u.OldKey] {
				t.Errorf("key %s in both matched and unmatched", u.OldKey)
			}
			seen[u.OldKey] = true
		}
		if len(seen) != len(stale) {
			t.Errorf("partition has %d items, stale has %d", len(seen), len(stale))
		}
		for _, m := range matched {
			if m.OldKey == m.NewKey {
				t.Errorf("self-remap OldKey == NewKey == %s", m.OldKey)
			}
		}
		for _, m := range matched {
			if m.Method == MethodGUID {
				entry := stale[m.OldKey]
				if entry.GUID == "" {
					t.Errorf("GUID method for empty GUID, key %s", m.OldKey)
				}
			}
		}
	})
}

func TestProcessHistoryRow_never_removes_entries(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := map[string]TautulliEntry{}
		nExisting := rapid.IntRange(0, 5).Draw(t, "n_existing")
		for i := range nExisting {
			key := strconv.Itoa(i + 1)
			items[key] = TautulliEntry{RatingKey: key, Title: fmt.Sprintf("Existing %d", i), Year: "2020", MediaType: Movie}
		}
		beforeLen := len(items)
		beforeKeys := map[string]string{}
		for k, v := range items {
			beforeKeys[k] = v.Title
		}

		mediaType := MediaType(rapid.SampledFrom([]string{"movie", "episode", "track", ""}).Draw(t, "media_type"))
		row := &HistoryItem{
			RatingKey:            FlexInt(rapid.IntRange(-1, 10).Draw(t, "rk")),
			GrandparentRatingKey: FlexInt(rapid.IntRange(-1, 10).Draw(t, "grk")),
			Title:                rapid.StringMatching(`[A-Za-z ]{0,15}`).Draw(t, "title"),
			GrandparentTitle:     rapid.StringMatching(`[A-Za-z ]{0,15}`).Draw(t, "gp_title"),
			Year:                 FlexInt(rapid.IntRange(2000, 2025).Draw(t, "year")),
			MediaType:            mediaType,
			GUID:                 rapid.SampledFrom([]string{"", "imdb://tt1234567", "plex://episode/abc", "local://123"}).Draw(t, "guid"),
		}

		ProcessHistoryRow(row, items)

		if len(items) < beforeLen {
			t.Errorf("map shrank from %d to %d", beforeLen, len(items))
		}
		for k, title := range beforeKeys {
			if items[k].Title != title {
				t.Errorf("existing entry %q changed from %q to %q", k, title, items[k].Title)
			}
		}
	})
}

// --- Fuzz targets ---

func FuzzFlexIntUnmarshal(f *testing.F) {
	f.Add([]byte(`42`))
	f.Add([]byte(`"123"`))
	f.Add([]byte(`""`))
	f.Add([]byte(`null`))
	f.Add([]byte(`0.5`))
	f.Add([]byte(`"abc"`))
	f.Add([]byte(`99999999999999999`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var fi FlexInt
		_ = fi.UnmarshalJSON(data)
	})
}

func FuzzNormalizeGUID(f *testing.F) {
	f.Add("imdb://tt1234567")
	f.Add("com.plexapp.agents.imdb://tt1234567?lang=en")
	f.Add("tmdb://12345")
	f.Add("tvdb://271557")
	f.Add("com.plexapp.agents.thetvdb://271557/3/1?lang=en")
	f.Add("plex://movie/5d776b59ad5437001f79c6f8")
	f.Add("local://616507")
	f.Add("")
	f.Add("custom://something")
	f.Fuzz(func(t *testing.T, guid string) {
		result := NormalizeGUID(guid)
		// Idempotency check
		if result != "" {
			second := NormalizeGUID(result)
			if second != result {
				t.Errorf("not idempotent: NormalizeGUID(%q)=%q, NormalizeGUID(%q)=%q", guid, result, result, second)
			}
		}
	})
}
