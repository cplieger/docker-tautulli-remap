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
		id := ExtractAfter(guid, m.Source)
		if m.StripPath {
			if i := strings.Index(id, "/"); i >= 0 {
				id = id[:i]
			}
		}
		return m.Canonical + id
	}
	return ""
}

// ExtractAfter returns the part after the prefix, trimming query params.
func ExtractAfter(s, prefix string) string {
	_, after, found := strings.Cut(s, prefix)
	if !found {
		return ""
	}
	if i := strings.Index(after, "?"); i >= 0 {
		after = after[:i]
	}
	return after
}

// MatchOne applies the GUID → title+year → title-only strategy chain to a
// single stale item, returning the new Plex rating key and the method
// string. Returns ("", "") when no strategy matched.
func MatchOne(
	item *TautulliEntry,
	oldKey string,
	byGUID, byTitleYear, byTitle map[string]PlexEntry,
	fallbackTitleYear, fallbackTitleOnly bool,
) (newKey string, method MatchMethod) {
	normalizedTitle := NormalizeTitle(item.Title)

	// Strategy 1: Match by GUID
	if item.GUID != "" {
		if pe, ok := byGUID[item.GUID]; ok && pe.RatingKey != oldKey {
			return pe.RatingKey, MethodGUID
		}
	}

	// Strategy 2: Match by title+year
	if fallbackTitleYear && normalizedTitle != "" {
		if pe, ok := byTitleYear[normalizedTitle+"|"+item.Year]; ok && pe.RatingKey != oldKey {
			return pe.RatingKey, MethodTitleYear
		}
	}

	// Strategy 3: Match by title only (require same media type)
	if fallbackTitleOnly && normalizedTitle != "" {
		if pe, ok := byTitle[normalizedTitle]; ok && pe.RatingKey != oldKey && pe.Type == item.MediaType {
			return pe.RatingKey, MatchMethod(fmt.Sprintf("title only (%s -> %s)", item.Year, pe.Year))
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
		newKey, method := MatchOne(&item, oldKey, byGUID, byTitleYear, byTitle, fallbackTitleYear, fallbackTitleOnly)
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
