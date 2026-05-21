package remap

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// MediaType represents the type of media item.
type MediaType string

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

const (
	MethodGUID      MatchMethod = "guid"
	MethodTitleYear MatchMethod = "title+year"
	MethodTitleOnly MatchMethod = "title only"
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

// FlexInt handles JSON values that may be float64, string, or json.Number.
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
type TautulliEntry struct {
	RatingKey string
	Title     string
	Year      string
	MediaType MediaType
	GUID      string
}

// PlexEntry represents a Plex library item used for matching.
type PlexEntry struct {
	RatingKey string
	Title     string
	Year      string
	Type      MediaType
	GUIDs     []string
}

// MatchResult holds the outcome of a successful match.
type MatchResult struct {
	Title     string
	Year      string
	OldKey    string
	NewKey    string
	MediaType MediaType
	Method    MatchMethod
}

// UnmatchResult holds an item that could not be matched.
type UnmatchResult struct {
	Title     string
	Year      string
	OldKey    string
	MediaType MediaType
}

// HistoryItem represents a single row from Tautulli's get_history response.
type HistoryItem struct {
	Title                string    `json:"title"`
	GrandparentTitle     string    `json:"grandparent_title"`
	GUID                 string    `json:"guid"`
	MediaType            MediaType `json:"media_type"`
	RatingKey            FlexInt   `json:"rating_key"`
	Year                 FlexInt   `json:"year"`
	GrandparentRatingKey FlexInt   `json:"grandparent_rating_key"`
}

// GUIDMapping maps a source prefix to its canonical scheme.
type GUIDMapping struct {
	Source    string
	Canonical string
	StripPath bool
}

// GUID prefix literals (see GUIDMappings for semantics). Each "<name>://"
// string is used in both the Source column and the Canonical column of the
// mapping table, so hoisting them into constants avoids drift when tests
// reference canonical prefixes.
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
	Title string
	Type  string
}

// LibItem represents a Plex library item.
type LibItem struct {
	Title     string
	GUIDs     []string
	RatingKey int
	Year      int
}

// NonRetryableError wraps errors that should not be retried.
type NonRetryableError struct{ Err error }

func (e *NonRetryableError) Error() string { return e.Err.Error() }
func (e *NonRetryableError) Unwrap() error { return e.Err }
