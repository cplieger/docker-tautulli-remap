// Package remap implements the matching and indexing logic that maps stale
// Tautulli rating keys to current Plex rating keys after library reorganizations.
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

// plexIndex accumulates the three lookup maps (by GUID, by title+year, and by
// title) while library sections are scanned concurrently. Its mutex guards all
// three maps so sections can be indexed in parallel.
type plexIndex struct {
	byGUID      map[string]PlexEntry
	byTitleYear map[string]PlexEntry
	byTitle     map[string]PlexEntry
	mu          sync.Mutex
}

func newPlexIndex() *plexIndex {
	return &plexIndex{
		byGUID:      map[string]PlexEntry{},
		byTitleYear: map[string]PlexEntry{},
		byTitle:     map[string]PlexEntry{},
	}
}

// add indexes a single library item under all of its GUIDs, its title+year,
// and its title. A debug line is emitted when an existing title or title+year
// key is shadowed by a different rating key.
func (idx *plexIndex) add(li LibItem, mediaType MediaType) {
	entry := PlexEntry{
		RatingKey: strconv.Itoa(li.RatingKey),
		Title:     li.Title,
		Year:      strconv.Itoa(li.Year),
		Type:      mediaType,
		GUIDs:     li.GUIDs,
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, g := range li.GUIDs {
		idx.byGUID[g] = entry
	}

	normalizedTitle := NormalizeTitle(li.Title)
	tyKey := normalizedTitle + "|" + entry.Year
	if prev, ok := idx.byTitleYear[tyKey]; ok && prev.RatingKey != entry.RatingKey {
		slog.Debug("title+year index shadow",
			"title", li.Title, "year", entry.Year,
			"prev_key", prev.RatingKey, "new_key", entry.RatingKey)
	}
	idx.byTitleYear[tyKey] = entry
	if prev, ok := idx.byTitle[normalizedTitle]; ok && prev.RatingKey != entry.RatingKey {
		slog.Debug("title index shadow",
			"title", li.Title,
			"prev_key", prev.RatingKey, "new_key", entry.RatingKey)
	}
	idx.byTitle[normalizedTitle] = entry
}

// scanSection fetches one library section and indexes every item it returns.
// A cancelled context short-circuits before the fetch.
func (idx *plexIndex) scanSection(ctx context.Context, plex PlexLibraryFetcher, sec Section) {
	if ctx.Err() != nil {
		return
	}
	slog.Info("scanning library", "title", sec.Title)
	mediaType := ParseMediaType(sec.Type)
	for _, li := range plex.LibraryAll(ctx, sec.Key) {
		idx.add(li, mediaType)
	}
}

// BuildPlexIndex builds three lookup maps (by GUID, by title+year, by title)
// from the Plex library sections. It fetches sections concurrently up to the
// given parallelism limit.
func BuildPlexIndex(ctx context.Context, plex PlexLibraryFetcher, parallelism int) (
	byGUID map[string]PlexEntry,
	byTitleYear map[string]PlexEntry,
	byTitle map[string]PlexEntry,
) {
	idx := newPlexIndex()
	sections := plex.LibrarySections(ctx)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelism)

	for _, sec := range sections {
		if sec.Type != string(Movie) && sec.Type != string(Show) {
			continue
		}
		g.Go(func() error {
			idx.scanSection(gctx, plex, sec)
			return nil
		})
	}

	_ = g.Wait()

	return idx.byGUID, idx.byTitleYear, idx.byTitle
}
