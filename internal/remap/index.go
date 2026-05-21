package remap

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// PlexLibraryFetcher is the narrow interface needed by BuildPlexIndex.
type PlexLibraryFetcher interface {
	LibrarySections(ctx context.Context) []Section
	LibraryAll(ctx context.Context, sectionKey string) []LibItem
}

// NormalizeTitle applies the canonical title normalization used for index
// keys and lookups. Both BuildPlexIndex and MatchOne use this function,
// ensuring the invariant that index key == lookup key is enforced by
// construction.
func NormalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

// BuildPlexIndex builds three lookup maps (by GUID, by title+year, by title)
// from the Plex library sections. It fetches sections concurrently up to the
// given parallelism limit.
func BuildPlexIndex(ctx context.Context, plex PlexLibraryFetcher, parallelism int) (
	byGUID map[string]PlexEntry,
	byTitleYear map[string]PlexEntry,
	byTitle map[string]PlexEntry,
) {
	byGUID = map[string]PlexEntry{}
	byTitleYear = map[string]PlexEntry{}
	byTitle = map[string]PlexEntry{}

	sections := plex.LibrarySections(ctx)

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelism)

	for _, sec := range sections {
		if sec.Type != string(Movie) && sec.Type != string(Show) {
			continue
		}
		g.Go(func() error {
			if gctx.Err() != nil {
				return nil
			}
			slog.Info("scanning library", "title", sec.Title)
			libItems := plex.LibraryAll(gctx, sec.Key)
			for _, li := range libItems {
				rk := strconv.Itoa(li.RatingKey)
				y := strconv.Itoa(li.Year)
				entry := PlexEntry{
					RatingKey: rk, Title: li.Title,
					Year: y, Type: ParseMediaType(sec.Type), GUIDs: li.GUIDs,
				}

				mu.Lock()
				for _, g := range li.GUIDs {
					byGUID[g] = entry
				}

				normalizedTitle := NormalizeTitle(li.Title)
				tyKey := normalizedTitle + "|" + y
				if prev, ok := byTitleYear[tyKey]; ok && prev.RatingKey != rk {
					slog.Debug("title+year index shadow",
						"title", li.Title, "year", y,
						"prev_key", prev.RatingKey, "new_key", rk)
				}
				byTitleYear[tyKey] = entry
				if prev, ok := byTitle[normalizedTitle]; ok && prev.RatingKey != rk {
					slog.Debug("title index shadow",
						"title", li.Title,
						"prev_key", prev.RatingKey, "new_key", rk)
				}
				byTitle[normalizedTitle] = entry
				mu.Unlock()
			}
			return nil
		})
	}

	_ = g.Wait() //nolint:errcheck // goroutines always return nil

	return byGUID, byTitleYear, byTitle
}
