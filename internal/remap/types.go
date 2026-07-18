package remap

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/cplieger/runesafe"
)

// MediaType represents the type of media item.
type MediaType string

// Movie, Show, and Episode are the supported Plex media type values.
const (
	Movie   MediaType = "movie"
	Show    MediaType = "show"
	Episode MediaType = "episode"
)

// String returns the string representation of a MediaType.
func (m MediaType) String() string { return string(m) }

// ParseMediaType converts a string to MediaType, returning empty string for unknown values.
func ParseMediaType(s string) MediaType {
	switch MediaType(s) {
	case Movie, Show, Episode:
		return MediaType(s)
	default:
		return ""
	}
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *MediaType) UnmarshalText(text []byte) error {
	switch MediaType(text) {
	case Movie, Show, Episode:
		*m = MediaType(text)
		return nil
	default:
		return fmt.Errorf("unknown media type: %q", text)
	}
}

// MatchMethod identifies the strategy used to match a stale item.
type MatchMethod string

// MethodEpisodeGUID, MethodGUID, MethodTitleYear, and MethodTitleOnly enumerate
// the available matching strategies in increasing order of aggressiveness.
// MethodEpisodeGUID is show-only: a stale show is resolved through one of its
// watched episodes' GUIDs (which Tautulli history retains even when the show's
// own GUID is not stored), giving an exact current show key with no title or
// year guesswork.
const (
	MethodEpisodeGUID MatchMethod = "episode-guid"
	MethodGUID        MatchMethod = "guid"
	MethodTitleYear   MatchMethod = "title+year"
	MethodTitleOnly   MatchMethod = "title only"
)

// String returns the string representation of a MatchMethod.
func (m MatchMethod) String() string { return string(m) }

// RatingKey is a Plex rating key (always a positive integer as string).
type RatingKey string

// IsValid returns true if the rating key is a non-empty string of ASCII digits.
func (r RatingKey) IsValid() bool {
	if r == "" {
		return false
	}
	for _, c := range r {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// FlexInt unmarshals a JSON number or a quoted numeric string into an int,
// coercing empty, null, or otherwise non-numeric JSON values to zero.
type FlexInt int

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexInt) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch val := raw.(type) {
	case float64:
		*f = FlexInt(int(val))
	case string:
		if val == "" {
			*f = 0
			return nil
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			*f = 0
			return nil
		}
		*f = FlexInt(n)
	case nil:
		*f = 0
	default:
		*f = 0
	}
	return nil
}

// TautulliEntry represents a unique item from Tautulli history.
//
// Title (here and on PlexEntry / MatchResult / UnmatchResult / HistoryItem /
// Section / LibItem) is media-server text sourced from the wild — agents,
// filenames — so it carries the runesafe.Untrusted tag: raw bytes in for
// matching (NormalizeTitle reads Raw()), sanitized automatically at every
// slog emit.
type TautulliEntry struct {
	RatingKey string
	Title     runesafe.Untrusted
	Year      string
	MediaType MediaType
	GUID      string
	// EpisodeGUIDs holds the stable, show-agnostic Plex episode GUIDs
	// (plex://episode/<hash>) observed in history for a Show entry. Tautulli
	// history stores an episode's own GUID but not its show's, so these are the
	// only durable handle back to the show: resolving any one of them against
	// Plex yields the show's current rating key. Empty for movies and for shows
	// whose history predates the plex:// agent (those carry a show-level GUID in
	// GUID instead). Deduplicated and capped at maxEpisodeGUIDsPerShow.
	EpisodeGUIDs []string
}

// PlexEntry represents a Plex library item used for matching.
type PlexEntry struct {
	RatingKey string
	Title     runesafe.Untrusted
	Year      string
	Type      MediaType
	GUIDs     []string
}

// MatchResult holds the outcome of a successful match.
type MatchResult struct {
	Title     runesafe.Untrusted
	Year      string
	OldKey    string
	NewKey    string
	MediaType MediaType
	Method    MatchMethod
	// MatchedYear is the matched Plex entry's release year, set only for
	// title-only matches — the one strategy where it can differ from Year
	// (the history item's year). Consumers surface the transition instead of
	// encoding it into Method, which stays a closed enum.
	MatchedYear string
}

// UnmatchResult holds an item that could not be matched.
type UnmatchResult struct {
	Title     runesafe.Untrusted
	Year      string
	OldKey    string
	MediaType MediaType
}

// HistoryItem represents a single row from Tautulli's get_history response.
// Title and GrandparentTitle are tagged at this decode boundary (see
// TautulliEntry).
type HistoryItem struct {
	Title            runesafe.Untrusted `json:"title"`
	GrandparentTitle runesafe.Untrusted `json:"grandparent_title"`
	GUID             string             `json:"guid"`
	// MediaType is decoded as a plain string rather than the MediaType type so a
	// row with an unexpected value (music "track", "clip", live TV, or a future
	// type) does not fail the whole page decode through MediaType's strict
	// UnmarshalText. ProcessHistoryRow validates it via ParseMediaType and skips
	// unknown types, keeping the wire boundary as fail-open as the processing.
	MediaType            string  `json:"media_type"`
	RatingKey            FlexInt `json:"rating_key"`
	Year                 FlexInt `json:"year"`
	GrandparentRatingKey FlexInt `json:"grandparent_rating_key"`
}

// GUIDMapping maps a source prefix to its canonical scheme.
type GUIDMapping struct {
	Source    string
	Canonical string
	StripPath bool
}

// GUID prefix literals (see GUIDMappings for semantics). Most appear in both
// the Source and Canonical columns of the mapping table; themoviedb:// and
// thetvdb:// are Source-only, canonicalizing to tmdb:// and tvdb://. Hoisting
// them into constants avoids drift when tests reference canonical prefixes.
const (
	GUIDPrefixTheMovieDB = "themoviedb://"
	GUIDPrefixTheTVDB    = "thetvdb://"
	GUIDPrefixIMDB       = "imdb://"
	GUIDPrefixTMDB       = "tmdb://"
	GUIDPrefixTVDB       = "tvdb://"
	GUIDPrefixMBID       = "mbid://"
	GUIDPrefixPlex       = "plex://"
)

// GUIDMappings defines the known GUID prefix transformations.
//
// ORDER IS SIGNIFICANT. NormalizeGUID matches each Source with strings.Contains
// (substring, not prefix) and returns on the first hit, so any Source that embeds a
// shorter one as a substring MUST be listed first: "thetvdb://" contains "tvdb://",
// so the StripPath=true thetvdb entry has to precede the bare tvdb entry, or legacy
// "thetvdb://<id>/<season>/<ep>" GUIDs would resolve via tvdb and never strip to the
// series id. Do not reorder (e.g. alphabetize) without preserving this invariant.
var GUIDMappings = [...]GUIDMapping{
	{GUIDPrefixTheMovieDB, GUIDPrefixTMDB, false},
	{GUIDPrefixTheTVDB, GUIDPrefixTVDB, true},
	{GUIDPrefixIMDB, GUIDPrefixIMDB, false},
	{GUIDPrefixTMDB, GUIDPrefixTMDB, false},
	{GUIDPrefixTVDB, GUIDPrefixTVDB, false},
	{GUIDPrefixMBID, GUIDPrefixMBID, false},
	{GUIDPrefixPlex, GUIDPrefixPlex, false},
}

// Section represents a Plex library section.
type Section struct {
	Key   string
	Title runesafe.Untrusted
	Type  string
}

// LibItem represents a Plex library item.
type LibItem struct {
	Title     runesafe.Untrusted
	GUIDs     []string
	RatingKey int
	Year      int
}
