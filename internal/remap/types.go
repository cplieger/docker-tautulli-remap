package remap

import (
	"encoding/json"
	"strconv"

	"github.com/cplieger/runesafe/v2"
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
// Section / LibItem) carries the runesafe.Untrusted tag since it is
// media-server text sourced from the wild — sanitized automatically at every
// slog emit; NormalizeTitle reads Raw() for matching.
type TautulliEntry struct {
	RatingKey string
	Title     runesafe.Untrusted
	Year      string
	MediaType MediaType
	GUID      string
	// EpisodeGUIDs holds the stable, show-agnostic Plex episode GUIDs
	// (plex://episode/<hash>) observed in history for a Show entry — the only
	// durable handle back to the show, since Tautulli history stores an
	// episode's own GUID but not its show's. Empty for movies and for shows
	// predating the plex:// agent. Deduplicated and capped at
	// maxEpisodeGUIDsPerShow.
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
	// MediaType is decoded as a plain string rather than MediaType so a row
	// with an unexpected value (music, clip, live TV) does not fail the whole
	// page decode; ProcessHistoryRow validates it via ParseMediaType.
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

// GUID prefix literals (see GUIDMappings for semantics). themoviedb:// and
// thetvdb:// are Source-only, canonicalizing to tmdb:// and tvdb://. Hoisted
// into constants so tests referencing canonical prefixes cannot drift.
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
// ORDER IS SIGNIFICANT. NormalizeGUID matches each Source with
// strings.Contains and returns on the first hit, so a Source that embeds a
// shorter one as a substring must be listed first: "thetvdb://" contains
// "tvdb://", so the StripPath=true thetvdb entry must precede the bare tvdb
// entry, or legacy "thetvdb://<id>/<season>/<ep>" GUIDs would resolve via tvdb
// and never strip to the series id. Do not reorder (e.g. alphabetize).
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
