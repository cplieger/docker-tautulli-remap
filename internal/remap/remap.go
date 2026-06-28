package remap

import (
	"fmt"
	"strconv"
	"strings"
)

// NormalizeGUID extracts a canonical ID from a Plex GUID string.
// Returns empty string for unsupported formats.
func NormalizeGUID(guid string) string {
	for _, m := range GUIDMappings {
		if !strings.Contains(guid, m.Source) {
			continue
		}
		id := extractAfter(guid, m.Source)
		if m.StripPath {
			if i := strings.Index(id, "/"); i >= 0 {
				id = id[:i]
			}
		}
		if id == "" {
			return ""
		}
		return m.Canonical + id
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

// matchOne applies the GUID → title+year → title-only strategy chain to a
// single stale item, returning the new Plex rating key and the method
// string. Returns ("", "") when no strategy matched. Every strategy is
// media-type-safe: strategy 1 guards on media type explicitly (byGUID is not
// type-keyed), and strategies 2 and 3 look up keys that encode the media type
// (see titleYearKey/titleKey), so a stale item never cross-type matches.
func matchOne(
	item *TautulliEntry,
	oldKey string,
	byGUID, byTitleYear, byTitle map[string]PlexEntry,
	fallbackTitleYear, fallbackTitleOnly bool,
) (newKey string, method MatchMethod) {
	normalizedTitle := NormalizeTitle(item.Title)

	// Strategy 1: Match by GUID. byGUID is keyed by the global, normalized GUID
	// (not by media type), so a stale item and an index entry can share a GUID
	// across types -- TMDB movies and TV series occupy the same tmdb://<id>
	// namespace with no type tag. Guard on media type so a stale Movie is never
	// remapped onto a same-id Show (or vice versa); the type-keyed title indexes
	// below cannot catch this because the GUID index is intentionally not
	// type-keyed.
	if item.GUID != "" {
		if pe, ok := byGUID[item.GUID]; ok && pe.RatingKey != oldKey && pe.Type == item.MediaType {
			return pe.RatingKey, MethodGUID
		}
	}

	// Strategy 2: Match by title+year. The lookup key folds in the stale item's
	// media type (see titleYearKey), so it can only resolve to a same-type slot;
	// an explicit pe.Type == item.MediaType guard is therefore redundant here and
	// is omitted.
	if fallbackTitleYear && normalizedTitle != "" {
		if pe, ok := byTitleYear[titleYearKey(normalizedTitle, item.Year, item.MediaType)]; ok && pe.RatingKey != oldKey {
			return pe.RatingKey, MethodTitleYear
		}
	}

	// Strategy 3: Match by title only. As in strategy 2 the lookup key encodes
	// the media type (see titleKey), so the match is same-type by construction
	// and needs no separate type guard.
	if fallbackTitleOnly && normalizedTitle != "" {
		if pe, ok := byTitle[titleKey(normalizedTitle, item.MediaType)]; ok && pe.RatingKey != oldKey {
			return pe.RatingKey, MatchMethod(fmt.Sprintf("%s (%s -> %s)", MethodTitleOnly, item.Year, pe.Year))
		}
	}

	return "", ""
}

// MatchStaleItems matches all stale items against the Plex index.
func MatchStaleItems(
	stale map[string]TautulliEntry,
	byGUID, byTitleYear, byTitle map[string]PlexEntry,
	fallbackTitleYear, fallbackTitleOnly bool,
) ([]MatchResult, []UnmatchResult) {
	var matched []MatchResult
	var unmatched []UnmatchResult

	for oldKey, item := range stale {
		newKey, method := matchOne(&item, oldKey, byGUID, byTitleYear, byTitle, fallbackTitleYear, fallbackTitleOnly)
		if newKey != "" {
			matched = append(matched, MatchResult{
				Title: item.Title, Year: item.Year,
				OldKey: oldKey, NewKey: newKey,
				MediaType: item.MediaType, Method: method,
			})
			continue
		}
		unmatched = append(unmatched, UnmatchResult{
			Title: item.Title, Year: item.Year,
			OldKey: oldKey, MediaType: item.MediaType,
		})
	}

	return matched, unmatched
}

// ProcessHistoryRow extracts a TautulliEntry from a single history row
// and inserts it into items (keyed by rating key). Returns true when an
// episode-level plex:// GUID had to be dropped.
func ProcessHistoryRow(row *HistoryItem, items map[string]TautulliEntry) bool {
	year := strconv.Itoa(int(row.Year))
	guid := NormalizeGUID(row.GUID)

	switch row.MediaType {
	case Movie:
		ratingKey := int(row.RatingKey)
		if ratingKey <= 0 {
			return false
		}
		key := strconv.Itoa(ratingKey)
		if _, ok := items[key]; !ok {
			items[key] = TautulliEntry{
				RatingKey: key, Title: row.Title,
				Year: year, MediaType: Movie, GUID: guid,
			}
		}
	case Episode:
		grandparentRatingKey := int(row.GrandparentRatingKey)
		if grandparentRatingKey <= 0 {
			return false
		}
		key := strconv.Itoa(grandparentRatingKey)
		if _, ok := items[key]; !ok {
			title := row.GrandparentTitle
			if title == "" {
				title = row.Title
			}
			showGUID := guid
			dropped := false
			if strings.Contains(showGUID, "plex://episode/") {
				showGUID = ""
				dropped = true
			}
			items[key] = TautulliEntry{
				RatingKey: key, Title: title,
				Year: year, MediaType: Show, GUID: showGUID,
			}
			return dropped
		}
	}
	return false
}
