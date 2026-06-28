package remap

import (
	"strings"
	"testing"
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
		// A leading-slash id (separator at index 0) strips to empty, and an
		// empty id is unsupported: "thetvdb:///271557" normalizes to "" (not
		// "tvdb://" and never "tvdb:///271557").
		{"thetvdb leading-slash id strips to empty", "com.plexapp.agents.thetvdb:///271557", ""},
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

func TestExtractAfter(t *testing.T) {
	tests := []struct {
		s, prefix, want string
	}{
		{"imdb://tt1234567", "imdb://", "tt1234567"},
		{"com.plexapp.agents.imdb://tt1234567?lang=en", "imdb://", "tt1234567"},
		{"tmdb://12345", "imdb://", ""},
		// A query separator at index 0 of the remainder must still be stripped:
		// "imdb://?lang=en" yields "" (everything after the prefix is query).
		{"imdb://?lang=en", "imdb://", ""},
		{"", "imdb://", ""},
	}
	for _, tt := range tests {
		if got := extractAfter(tt.s, tt.prefix); got != tt.want {
			t.Errorf("extractAfter(%q, %q) = %q, want %q", tt.s, tt.prefix, got, tt.want)
		}
	}
}

func TestMatchOne(t *testing.T) {
	t.Run("guid match", func(t *testing.T) {
		item := &TautulliEntry{GUID: "imdb://tt1", Title: "M", Year: "2020", MediaType: Movie}
		byGUID := map[string]PlexEntry{"imdb://tt1": {RatingKey: "200", Type: Movie}}
		key, method := matchOne(item, "100", byGUID, nil, nil, true, true)
		if key != "200" || method != MethodGUID {
			t.Errorf("got (%q, %q), want (200, guid)", key, method)
		}
	})

	t.Run("guid cross-type rejected", func(t *testing.T) {
		// A stale Movie and a Show can share the tmdb:// namespace (TMDB IDs are
		// not type-tagged). byGUID is keyed by the bare GUID, so strategy 1 must
		// guard on media type: a stale Movie's tmdb://12345 must NOT match a Show
		// indexed under the same normalized GUID, otherwise the history row would
		// be remapped onto the wrong-type item.
		item := &TautulliEntry{GUID: "tmdb://12345", Title: "Quiz Show", Year: "1994", MediaType: Movie}
		byGUID := map[string]PlexEntry{"tmdb://12345": {RatingKey: "200", Title: "Quiz Show", Year: "1994", Type: Show}}
		key, method := matchOne(item, "100", byGUID, nil, nil, true, true)
		if key != "" || method != "" {
			t.Errorf("got (%q, %q), want empty (GUID strategy must reject a cross-type match)", key, method)
		}
	})

	t.Run("title+year fallback", func(t *testing.T) {
		item := &TautulliEntry{Title: "Movie", Year: "2020", MediaType: Movie}
		byTY := map[string]PlexEntry{"movie|2020|movie": {RatingKey: "200", Type: Movie}}
		key, method := matchOne(item, "100", nil, byTY, nil, true, true)
		if key != "200" || method != MethodTitleYear {
			t.Errorf("got (%q, %q), want (200, title+year)", key, method)
		}
	})

	t.Run("no match", func(t *testing.T) {
		item := &TautulliEntry{Title: "X", Year: "2020", MediaType: Movie}
		key, method := matchOne(item, "100", nil, nil, nil, true, true)
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
		checks      []check
		enableTY    bool
		enableTO    bool
	}{
		{
			name:        "guid match takes priority over title+year",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "The Matrix", Year: "1999", MediaType: Movie, GUID: "imdb://tt0133093"}},
			byGUID:      map[string]PlexEntry{"imdb://tt0133093": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
			byTitleYear: map[string]PlexEntry{"the matrix|1999|movie": {RatingKey: "300", Title: "The Matrix", Year: "1999", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200"), matchMethod(0, MethodGUID)},
		},
		{
			name:        "title+year fallback when no guid",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Inception", Year: "2010", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"inception|2010|movie": {RatingKey: "200", Title: "Inception", Year: "2010", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200"), matchMethod(0, MethodTitleYear)},
		},
		{
			name:     "title-only fallback with matching type",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: Movie}},
			byTitle:  map[string]PlexEntry{"dune|movie": {RatingKey: "200", Title: "Dune", Year: "2021", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(1, 0), matchMethodPrefix(0, "title only")},
		},
		{
			name:     "title-only rejects type mismatch",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Home Alone", Year: "2025", MediaType: Show}},
			byTitle:  map[string]PlexEntry{"home alone|movie": {RatingKey: "200", Title: "Home Alone", Year: "1990", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			// FIX 1: the title+year lookup key folds in the stale item's media
			// type, so a stale Movie can only resolve to a Movie slot. Its only
			// same-title+year index entry here is a Show, so it must NOT match.
			name:        "title+year rejects type mismatch",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Heat", Year: "1995", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"heat|1995|show": {RatingKey: "200", Title: "Heat", Year: "1995", Type: Show}},
			enableTY:    true, enableTO: false,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:        "case insensitive title matching",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "THE MATRIX", Year: "1999", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"the matrix|1999|movie": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
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
			byTitleYear: map[string]PlexEntry{"inception|2010|movie": {RatingKey: "200", Title: "Inception", Year: "2010", Type: Movie}},
			enableTY:    false, enableTO: false,
			checks: []check{matchCount(0, 1)},
		},

		{
			name:     "title-only disabled skips fallback",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: Movie}},
			byTitle:  map[string]PlexEntry{"dune|movie": {RatingKey: "200", Title: "Dune", Year: "2021", Type: Movie}},
			enableTY: true, enableTO: false,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:        "whitespace-only title does not match",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "   ", Year: "2020", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"|2020|movie": {RatingKey: "200", Title: "", Year: "2020", Type: Movie}},
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
			byTitleYear: map[string]PlexEntry{"movie c|2022|movie": {RatingKey: "301", Title: "Movie C", Year: "2022", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{
				matchCount(2, 1),
				unmatchedKey(0, "200"),
			},
		},
		{
			name:        "title with leading trailing whitespace",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "  The Matrix  ", Year: "1999", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"the matrix|1999|movie": {RatingKey: "200", Title: "The Matrix", Year: "1999", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200")},
		},
		{
			name:        "guid match same key falls through to title",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Movie", Year: "2020", MediaType: Movie, GUID: "imdb://tt1111111"}},
			byGUID:      map[string]PlexEntry{"imdb://tt1111111": {RatingKey: "100", Title: "Movie", Year: "2020", Type: Movie}},
			byTitleYear: map[string]PlexEntry{"movie|2020|movie": {RatingKey: "200", Title: "Movie", Year: "2020", Type: Movie}},
			enableTY:    true, enableTO: true,
			checks: []check{matchCount(1, 0), matchKey(0, "200"), matchMethod(0, MethodTitleYear)},
		},
		{
			name:     "title only same key falls through to unmatched",
			stale:    map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Unique Movie", Year: "2020", MediaType: Movie}},
			byTitle:  map[string]PlexEntry{"unique movie|movie": {RatingKey: "100", Title: "Unique Movie", Year: "2020", Type: Movie}},
			enableTY: true, enableTO: true,
			checks: []check{matchCount(0, 1)},
		},
		{
			name:        "title year same key falls through to title only",
			stale:       map[string]TautulliEntry{"100": {RatingKey: "100", Title: "Dune", Year: "2020", MediaType: Movie}},
			byTitleYear: map[string]PlexEntry{"dune|2020|movie": {RatingKey: "100", Title: "Dune", Year: "2020", Type: Movie}},
			byTitle:     map[string]PlexEntry{"dune|movie": {RatingKey: "300", Title: "Dune", Year: "2021", Type: Movie}},
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

func TestNormalizeGUID_emptyID_returnsEmpty(t *testing.T) {
	// The empty-id guard returns "" for ANY mapping whose extracted id is
	// empty, including the non-StripPath route where extractAfter strips a
	// bare prefix or a query-only remainder to "". Without the guard these
	// return the bare canonical prefix (e.g. "imdb://").
	tests := []struct {
		name, guid, want string
	}{
		{"imdb bare prefix only", "imdb://", ""},
		{"imdb prefix with query only", "imdb://?lang=en", ""},
		{"tmdb bare prefix only", "tmdb://", ""},
		{"tmdb prefix with query only", "tmdb://?lang=en", ""},
		{"tvdb bare prefix only", "tvdb://", ""},
		{"plex bare prefix only", "plex://", ""},
		{"mbid bare prefix only", "mbid://", ""},
		{"themoviedb legacy prefix query only", "com.plexapp.agents.themoviedb://?lang=en", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeGUID(tt.guid); got != tt.want {
				t.Errorf("NormalizeGUID(%q) = %q, want %q", tt.guid, got, tt.want)
			}
		})
	}
}

func TestMatchOne_titleYearTakesPriorityOverTitleOnly(t *testing.T) {
	// Both the title+year and title-only indexes hold a valid (non-stale) match
	// for the item. The chain is ordered by increasing aggressiveness, so
	// strategy 2 (title+year) must win and the riskier strategy 3 (title-only)
	// must never be reached when title+year already resolves.
	item := &TautulliEntry{Title: "Dune", Year: "2020", MediaType: Movie}
	byTitleYear := map[string]PlexEntry{"dune|2020|movie": {RatingKey: "200", Title: "Dune", Year: "2020", Type: Movie}}
	byTitle := map[string]PlexEntry{"dune|movie": {RatingKey: "300", Title: "Dune", Year: "2021", Type: Movie}}
	key, method := matchOne(item, "100", nil, byTitleYear, byTitle, true, true)
	if key != "200" {
		t.Errorf("matchOne key = %q, want 200 (title+year must win over title-only)", key)
	}
	if method != MethodTitleYear {
		t.Errorf("matchOne method = %q, want %q (strategy 2 precedes strategy 3)", method, MethodTitleYear)
	}
}

func TestMatchOne_titleOnlyMethodShowsYearTransition(t *testing.T) {
	// The title-only method label encodes the stale->matched year drift it
	// tolerated (item.Year -> pe.Year), surfaced to the operator judging the
	// riskiest match. Existing tests assert only the "title only" prefix, so a
	// regression that drops or swaps the years survives; pin the exact format.
	item := &TautulliEntry{Title: "Dune", Year: "1984", MediaType: Movie}
	byTitle := map[string]PlexEntry{"dune|movie": {RatingKey: "300", Title: "Dune", Year: "2021", Type: Movie}}
	key, method := matchOne(item, "100", nil, nil, byTitle, true, true)
	if key != "300" {
		t.Errorf("matchOne key = %q, want 300", key)
	}
	want := MatchMethod("title only (1984 -> 2021)")
	if method != want {
		t.Errorf("matchOne method = %q, want %q", method, want)
	}
}
