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

	"github.com/cplieger/keyenc"
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
//
// The components are escaped with keyenc rather than concatenated because the
// first one is free-form: normalizedTitle is a Plex media title, lower-cased
// and trimmed but otherwise arbitrary operator- and metadata-agent-supplied
// text, and real titles do contain the separator ("Dune | Extended Edition").
// A collision here is not a cache miss, it is a merged identity, and this index
// decides which rating key gets written into Tautulli's history database:
// either two different Plex items land in one slot and the ambiguity guard
// prunes it, silently refusing a legitimate remap, or a history item's lookup
// resolves to the entry of a DIFFERENT item and live mode writes that wrong
// rating key into the database, re-attaching watch history to the wrong title.
//
// No such collision is reachable today, because the two trailing components
// have constrained alphabets: year is strconv.Itoa of an int and mediaType is
// one of ParseMediaType's enum values (or empty). That safety is a property of
// the current field list rather than of the key — appending a free-form
// component (an edition tag, a section name) or widening year to a range would
// open it silently, at a site whose failure mode is a wrong database write.
// keyenc makes it a property of the key.
//
// The separator changed from '|' to keyenc's ':' with the adoption. Free here:
// the three indexes are rebuilt in memory by every BuildPlexIndex call and
// never persisted, logged as keys, or compared across runs.
func titleYearKey(normalizedTitle, year string, mediaType MediaType) string {
	return keyenc.Join(normalizedTitle, year, string(mediaType))
}

// titleKey builds the composite lookup key for the byTitle index from a
// pre-normalized title and the media type. See titleYearKey for why the media
// type is part of the key, and why the components are escaped rather than
// concatenated.
func titleKey(normalizedTitle string, mediaType MediaType) string {
	return keyenc.Join(normalizedTitle, string(mediaType))
}

// Index is the Plex library lookup MatchStaleItems consumes: three maps over
// one entry set, each keyed by a different match strategy. Grouping them in a
// struct (rather than three positional map[string]PlexEntry parameters) makes
// every assignment to a strategy NAMED: the old positional form let a swapped
// pair compile invisibly, match on the wrong key, and report success. The
// residual gap is honest to state: the fields share one map type, so a
// composite literal that names the wrong field still compiles — but it now
// says ByGUID: byTitleYear at the one construction site instead of hiding in
// an argument list.
type Index struct {
	// ByGUID is keyed by the normalized global GUID. Deliberately NOT
	// type-keyed (TMDB movies and TV series share the tmdb://<id> namespace),
	// so matchOne guards media type at the lookup.
	ByGUID map[string]PlexEntry
	// ByTitleYear is keyed by titleYearKey (normalized title + year + media
	// type), so a hit is same-type and same-year by construction.
	ByTitleYear map[string]PlexEntry
	// ByTitle is keyed by titleKey (normalized title + media type).
	ByTitle map[string]PlexEntry
}

// Empty reports whether no strategy has any entry — the orchestrator's
// "cannot match anything" abort signal.
func (ix Index) Empty() bool {
	return len(ix.ByGUID) == 0 && len(ix.ByTitleYear) == 0 && len(ix.ByTitle) == 0
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

// index snapshots the accumulator's three lookup maps into the exported
// Index shape — the ONE place the field-to-strategy mapping is written, so a
// transposition cannot creep in at a second construction site.
func (idx *plexIndex) index() Index {
	return Index{ByGUID: idx.byGUID, ByTitleYear: idx.byTitleYear, ByTitle: idx.byTitle}
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

	normalizedTitle := NormalizeTitle(li.Title.Raw())
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

// BuildPlexIndex builds the Index (GUID, title+year and title lookups) from
// the Plex library sections. It fetches sections concurrently up to the given
// parallelism limit. failedSections reports how many sections could not be
// fetched (including a total failure to list sections); a non-zero count means
// the index is incomplete, so the caller must treat Plex as degraded rather
// than trusting a partial index.
func BuildPlexIndex(ctx context.Context, plex PlexLibraryFetcher, parallelism int) (idx Index, failedSections int) {
	acc := newPlexIndex()
	sections, err := plex.LibrarySections(ctx)
	if err != nil {
		slog.Error("failed to list Plex library sections", "error", err)
		return acc.index(), 1
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
			if scanErr := acc.scanSection(gctx, plex, sec); scanErr != nil {
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
	for k := range acc.ambiguousGUID {
		delete(acc.byGUID, k)
	}
	for k := range acc.ambiguousTitleYear {
		delete(acc.byTitleYear, k)
	}
	for k := range acc.ambiguousTitle {
		delete(acc.byTitle, k)
	}

	if refused := len(acc.ambiguousGUID) + len(acc.ambiguousTitleYear) + len(acc.ambiguousTitle); refused > 0 {
		slog.Info("refused to match ambiguous index keys (multiple Plex items shared one lookup key)",
			"guid", len(acc.ambiguousGUID),
			"title_year", len(acc.ambiguousTitleYear),
			"title", len(acc.ambiguousTitle))
	}

	return acc.index(), int(failed.Load())
}
