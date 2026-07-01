// Package remap implements the matching and indexing logic that maps stale
// Tautulli rating keys to current Plex rating keys after library reorganizations.
package remap

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// PlexLibraryFetcher is the narrow interface needed by BuildPlexIndex.
type PlexLibraryFetcher interface {
	LibrarySections(ctx context.Context) ([]Section, error)
	LibraryAll(ctx context.Context, sectionKey string) ([]LibItem, error)
}

// NormalizeTitle applies the canonical title normalization used for index
// keys and lookups. Both BuildPlexIndex and matchOne use this function,
// ensuring the invariant that index key == lookup key is enforced by
// construction.
func NormalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

// titleYearKey builds the composite lookup key for the byTitleYear index from a
// pre-normalized title, the year, and the media type. Folding the media type
// into the key keeps cross-type entries that share a title and year (a Movie
// "Dune" 2021 and a Show "Dune" 2021) in DISTINCT slots, so only genuine
// same-type duplicates collide and trigger refuse-to-match. Both add and
// matchOne build the key through this helper, so the "index key == lookup key"
// invariant holds by construction.
func titleYearKey(normalizedTitle, year string, mediaType MediaType) string {
	return normalizedTitle + "|" + year + "|" + string(mediaType)
}

// titleKey builds the composite lookup key for the byTitle index from a
// pre-normalized title and the media type. See titleYearKey for why the media
// type is part of the key.
func titleKey(normalizedTitle string, mediaType MediaType) string {
	return normalizedTitle + "|" + string(mediaType)
}

// plexIndex accumulates the three lookup maps (by GUID, by title+year, and by
// title) while library sections are scanned concurrently. Its mutex guards all
// three maps so sections can be indexed in parallel. The three ambiguous sets
// record any key for which two DIFFERENT rating keys ever competed; such keys
// are deleted from the lookup maps before BuildPlexIndex returns so matchOne
// refuses to match on them (deterministic and order-independent, unlike the
// former last-writer-wins resolution).
type plexIndex struct {
	byGUID             map[string]PlexEntry
	byTitleYear        map[string]PlexEntry
	byTitle            map[string]PlexEntry
	ambiguousGUID      map[string]struct{}
	ambiguousTitleYear map[string]struct{}
	ambiguousTitle     map[string]struct{}
	mu                 sync.Mutex
}

func newPlexIndex() *plexIndex {
	return &plexIndex{
		byGUID:             map[string]PlexEntry{},
		byTitleYear:        map[string]PlexEntry{},
		byTitle:            map[string]PlexEntry{},
		ambiguousGUID:      map[string]struct{}{},
		ambiguousTitleYear: map[string]struct{}{},
		ambiguousTitle:     map[string]struct{}{},
	}
}

// add indexes a single library item under all of its GUIDs, its
// (title, year, media type), and its (title, media type). The title-based keys
// fold in the media type (see titleYearKey/titleKey) so a Movie and a Show that
// share a title -- and possibly a year -- occupy distinct slots and do not
// falsely collide; only a genuine same-type duplicate marks a key ambiguous.
// byGUID stays keyed by the global GUID alone, since a GUID is meant to be
// unique. A debug line is emitted when an existing GUID or title key is
// shadowed by a different rating key; the title+year shadow is logged at warn
// instead, because the title+year fallback is on by default, so an ambiguous
// slot there is worth surfacing to the operator. Any collision (two different
// rating keys competing for one key) marks that key ambiguous so it is later
// removed from the lookup maps (refuse-to-match) and cannot drive a match: the
// ambiguous slot is dropped rather than silently remapped to the wrong key.
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
		if prev, ok := idx.byGUID[g]; ok && prev.RatingKey != entry.RatingKey {
			slog.Debug("guid index shadow",
				"guid", g,
				"prev_key", prev.RatingKey, "new_key", entry.RatingKey)
			idx.ambiguousGUID[g] = struct{}{}
		}
		idx.byGUID[g] = entry
	}

	normalizedTitle := NormalizeTitle(li.Title)
	tyKey := titleYearKey(normalizedTitle, entry.Year, mediaType)
	if prev, ok := idx.byTitleYear[tyKey]; ok && prev.RatingKey != entry.RatingKey {
		slog.Warn("title+year index shadow; refusing to match this ambiguous slot",
			"title", li.Title, "year", entry.Year,
			"prev_key", prev.RatingKey, "new_key", entry.RatingKey)
		idx.ambiguousTitleYear[tyKey] = struct{}{}
	}
	idx.byTitleYear[tyKey] = entry

	tKey := titleKey(normalizedTitle, mediaType)
	if prev, ok := idx.byTitle[tKey]; ok && prev.RatingKey != entry.RatingKey {
		slog.Debug("title index shadow",
			"title", li.Title,
			"prev_key", prev.RatingKey, "new_key", entry.RatingKey)
		idx.ambiguousTitle[tKey] = struct{}{}
	}
	idx.byTitle[tKey] = entry
}

// scanSection fetches one library section and indexes every item it returns.
// A cancelled context short-circuits before the fetch (reported as no failure).
// A non-nil return means the section's items could not be fetched, so the
// caller can count it as a failed section rather than an empty one.
func (idx *plexIndex) scanSection(ctx context.Context, plex PlexLibraryFetcher, sec Section) error {
	if ctx.Err() != nil {
		return nil
	}
	slog.Info("scanning library", "title", sec.Title)
	mediaType := ParseMediaType(sec.Type)
	items, err := plex.LibraryAll(ctx, sec.Key)
	if err != nil {
		if ctx.Err() != nil {
			return nil // cancelled mid-fetch (e.g. shutdown): clean short-circuit, not a section failure
		}
		slog.Error("failed to fetch Plex library section",
			"title", sec.Title, "key", sec.Key, "error", err)
		return err
	}
	for _, li := range items {
		idx.add(li, mediaType)
	}
	slog.Debug("scanned library section", "title", sec.Title, "items", len(items))
	return nil
}

// BuildPlexIndex builds three lookup maps (by GUID, by title+year, by title)
// from the Plex library sections. It fetches sections concurrently up to the
// given parallelism limit. failedSections reports how many sections could not
// be fetched (including a total failure to list sections); a non-zero count
// means the index is incomplete, so the caller must treat Plex as degraded
// rather than trusting a partial index.
func BuildPlexIndex(ctx context.Context, plex PlexLibraryFetcher, parallelism int) (
	byGUID map[string]PlexEntry,
	byTitleYear map[string]PlexEntry,
	byTitle map[string]PlexEntry,
	failedSections int,
) {
	idx := newPlexIndex()
	sections, err := plex.LibrarySections(ctx)
	if err != nil {
		slog.Error("failed to list Plex library sections", "error", err)
		return idx.byGUID, idx.byTitleYear, idx.byTitle, 1
	}

	var failed atomic.Int64

	g, gctx := errgroup.WithContext(ctx)
	// errgroup.SetLimit(0) deadlocks; negative => unbounded.
	parallelism = max(parallelism, 1)
	g.SetLimit(parallelism)

	for _, sec := range sections {
		if sec.Type != string(Movie) && sec.Type != string(Show) {
			continue
		}
		g.Go(func() error {
			if scanErr := idx.scanSection(gctx, plex, sec); scanErr != nil {
				failed.Add(1)
			}
			return nil
		})
	}

	_ = g.Wait()

	// Refuse to match on any ambiguous slot: a key for which two different
	// rating keys ever competed is removed from its lookup map, so matchOne
	// cannot match on it. This is deterministic and order-independent, unlike
	// the former last-writer-wins behavior (where the winning key depended on
	// concurrent section scan ordering).
	for k := range idx.ambiguousGUID {
		delete(idx.byGUID, k)
	}
	for k := range idx.ambiguousTitleYear {
		delete(idx.byTitleYear, k)
	}
	for k := range idx.ambiguousTitle {
		delete(idx.byTitle, k)
	}

	if refused := len(idx.ambiguousGUID) + len(idx.ambiguousTitleYear) + len(idx.ambiguousTitle); refused > 0 {
		slog.Info("refused to match ambiguous index keys (multiple Plex items shared one lookup key)",
			"guid", len(idx.ambiguousGUID),
			"title_year", len(idx.ambiguousTitleYear),
			"title", len(idx.ambiguousTitle))
	}

	return idx.byGUID, idx.byTitleYear, idx.byTitle, int(failed.Load())
}
