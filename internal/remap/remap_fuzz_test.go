package remap

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

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
		if err := fi.UnmarshalJSON(data); err != nil {
			return
		}
		// Round-trip stability for values within float64 safe integer range (±2^53).
		const maxSafe = 1 << 53
		v := int(fi)
		if v > -maxSafe && v < maxSafe {
			out, err := json.Marshal(fi)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			var fi2 FlexInt
			if err := fi2.UnmarshalJSON(out); err != nil {
				t.Fatalf("re-unmarshal failed: %v", err)
			}
			if fi != fi2 {
				t.Errorf("round-trip mismatch: %d != %d (marshaled as %s)", fi, fi2, out)
			}
		}
	})
}

// FuzzRatingKey_IsValid pins the security contract of RatingKey.IsValid: the
// app interpolates rating keys straight into Plex/Tautulli URL paths, so any
// key IsValid accepts must be safe there. The invariant is that an accepted
// key is non-empty and contains only ASCII digits, which excludes every
// path-traversal or separator character ('/', '.', '?', whitespace, ...).
func FuzzRatingKey_IsValid(f *testing.F) {
	f.Add("42")
	f.Add("0")
	f.Add("")
	f.Add("-1")
	f.Add("abc")
	f.Add("../../../etc/passwd")
	f.Add("12/34")
	f.Add("9999999999999999999999")
	f.Fuzz(func(t *testing.T, s string) {
		if !RatingKey(s).IsValid() {
			return
		}
		if s == "" {
			t.Errorf("IsValid() accepted the empty string")
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				t.Errorf("IsValid(%q) accepted a key containing non-digit %q", s, c)
			}
		}
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
		if result == "" {
			return
		}
		// Idempotency check
		second := NormalizeGUID(result)
		if second != result {
			t.Errorf("not idempotent: NormalizeGUID(%q)=%q, NormalizeGUID(%q)=%q", guid, result, result, second)
		}
		// Canonical prefix check
		canonicalPrefixes := []string{"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://"}
		hasCanonical := false
		for _, cp := range canonicalPrefixes {
			if strings.HasPrefix(result, cp) {
				hasCanonical = true
				break
			}
		}
		if !hasCanonical {
			t.Errorf("NormalizeGUID(%q) = %q does not start with a canonical prefix", guid, result)
		}
	})
}

func FuzzProcessHistoryRow(f *testing.F) {
	f.Add([]byte(`{"rating_key":42,"title":"Test","year":2020,"media_type":"movie","guid":"imdb://tt1234567"}`))
	f.Add([]byte(`{"rating_key":"99","grandparent_rating_key":"50","title":"Ep","grandparent_title":"Show","year":2021,"media_type":"episode","guid":"tvdb://271557"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"rating_key":0,"title":"","year":0,"media_type":"track"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var row HistoryItem
		if err := json.Unmarshal(data, &row); err != nil {
			return
		}
		items := map[string]TautulliEntry{}
		// Must never panic
		ProcessHistoryRow(&row, items)
		// Validate outputs
		canonicalPrefixes := []string{"imdb://", "tmdb://", "tvdb://", "plex://", "mbid://"}
		for key, entry := range items {
			// RatingKey must be non-zero positive
			rk, err := strconv.Atoi(key)
			if err != nil || rk <= 0 {
				t.Errorf("invalid rating key %q in output", key)
			}
			if entry.RatingKey != key {
				t.Errorf("entry.RatingKey=%q != map key=%q", entry.RatingKey, key)
			}
			// MediaType must be Movie or Show
			if entry.MediaType != Movie && entry.MediaType != Show {
				t.Errorf("unexpected MediaType %q for key %s", entry.MediaType, key)
			}
			// GUID if non-empty must start with canonical prefix
			if entry.GUID != "" {
				hasPrefix := false
				for _, cp := range canonicalPrefixes {
					if strings.HasPrefix(entry.GUID, cp) {
						hasPrefix = true
						break
					}
				}
				if !hasPrefix {
					t.Errorf("entry GUID %q does not start with canonical prefix", entry.GUID)
				}
			}
		}
	})
}

func FuzzMatchOne(f *testing.F) {
	f.Add("Test Movie", "imdb://tt1234567", "movie")
	f.Add("", "", "")
	f.Add("Show Title", "tvdb://12345", "show")
	f.Fuzz(func(t *testing.T, title, guid, mediaType string) {
		mt := ParseMediaType(mediaType)
		item := &TautulliEntry{
			RatingKey: "100",
			Title:     title,
			Year:      "2020",
			MediaType: mt,
			GUID:      guid,
		}
		byGUID := map[string]PlexEntry{
			"imdb://tt1234567": {RatingKey: "200", Title: "M", Year: "2020", Type: Movie},
			"tvdb://12345":     {RatingKey: "300", Title: "S", Year: "2021", Type: Show},
		}
		byTitleYear := map[string]PlexEntry{
			"test movie|2020": {RatingKey: "400", Title: "Test Movie", Year: "2020", Type: Movie},
		}
		byTitle := map[string]PlexEntry{
			"show title": {RatingKey: "500", Title: "Show Title", Year: "2021", Type: Show},
		}
		validKeys := map[string]bool{"200": true, "300": true, "400": true, "500": true}

		// Must never panic
		newKey, _ := MatchOne(item, "100", byGUID, byTitleYear, byTitle, true, true)

		// Result must be either empty or from the input maps
		if newKey != "" && !validKeys[newKey] {
			t.Errorf("MatchOne returned key %q which is not in the input maps", newKey)
		}
	})
}

// --- JSON unmarshal fuzz targets ---
// These use local struct equivalents to avoid circular imports with the
// tautulli and plex packages which import remap.

func FuzzHistoryResponseUnmarshal(f *testing.F) {
	f.Add([]byte(`{"response":{"result":"success","data":{"data":[{"title":"M","media_type":"movie","rating_key":1,"year":2020,"guid":"imdb://tt1"}],"recordsFiltered":1}}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"response":{"result":"error","message":"bad"}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Local equivalent of tautulli.HistoryResponse
		var resp struct {
			Response struct {
				Result  string `json:"result"`
				Message string `json:"message"`
				Data    struct {
					Data            []HistoryItem `json:"data"`
					RecordsFiltered int           `json:"recordsFiltered"`
				} `json:"data"`
			} `json:"response"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		if resp.Response.Data.RecordsFiltered < 0 {
			t.Errorf("recordsFiltered = %d, want >= 0", resp.Response.Data.RecordsFiltered)
		}
		for i, item := range resp.Response.Data.Data {
			// Exercise FlexInt fields - must not panic
			_ = int(item.RatingKey)
			_ = int(item.Year)
			_ = int(item.GrandparentRatingKey)
			_ = item.Title
			_ = item.MediaType
			if int(item.RatingKey) < 0 {
				t.Errorf("data[%d].RatingKey = %d, want >= 0 if parsed", i, int(item.RatingKey))
			}
		}
	})
}

func FuzzPlexLibrarySectionsUnmarshal(f *testing.F) {
	f.Add([]byte(`{"MediaContainer":{"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"MediaContainer":{"Directory":[]}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var result struct {
			MediaContainer struct {
				Directory []struct {
					Key   string `json:"key"`
					Title string `json:"title"`
					Type  string `json:"type"`
				} `json:"Directory"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return
		}
		for _, d := range result.MediaContainer.Directory {
			// Non-empty key is the expected invariant of well-formed Plex
			// responses. We verify field access never panics.
			if d.Key == "" {
				// Not a valid Plex section entry; skip further checks.
				continue
			}
			_ = d.Title
			_ = d.Type
		}
	})
}

func FuzzPlexLibraryAllUnmarshal(f *testing.F) {
	f.Add([]byte(`{"MediaContainer":{"Metadata":[{"title":"Movie","ratingKey":"123","guid":"tmdb://1","Guid":[{"id":"imdb://tt1"}],"year":2020}]}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
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
		if err := json.Unmarshal(data, &result); err != nil {
			return
		}
		for _, m := range result.MediaContainer.Metadata {
			// Exercise strconv.Atoi on ratingKey - must not panic
			if rk, err := strconv.Atoi(m.RatingKey); err == nil {
				if rk < 0 {
					t.Errorf("ratingKey %q parsed to %d, want non-negative", m.RatingKey, rk)
				}
			}
			// Exercise NormalizeGUID on guid and each Guid[].id - must not panic
			_ = NormalizeGUID(m.GUID)
			for _, g := range m.Guids {
				_ = NormalizeGUID(g.ID)
			}
		}
	})
}
