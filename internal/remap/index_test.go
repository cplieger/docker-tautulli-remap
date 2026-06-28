package remap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// collidingFetcher returns its sections and per-section items verbatim, so a test can place two
// distinct items in ONE section to exercise the index shadow/last-write-wins path deterministically.
type collidingFetcher struct {
	sections []Section
	items    map[string][]LibItem
}

func (f *collidingFetcher) LibrarySections(_ context.Context) ([]Section, error) {
	return f.sections, nil
}

func (f *collidingFetcher) LibraryAll(_ context.Context, sectionKey string) ([]LibItem, error) {
	return f.items[sectionKey], nil
}

func TestBuildPlexIndex_TitleYearCollisionRefusesToMatch(t *testing.T) {
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {
				{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0001"}},
				{RatingKey: 2, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0002"}},
			},
		},
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	byGUID, byTitleYear, byTitle, failed := BuildPlexIndex(context.Background(), fetcher, 1)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0 (fetcher never errors)", failed)
	}

	// A collision on the title+year and title keys means the slot is ambiguous,
	// so it is removed entirely (refuse to match) rather than resolved by the
	// last writer.
	if _, ok := byTitleYear["heat|1995|movie"]; ok {
		t.Errorf("byTitleYear[heat|1995|movie] = %q, want ABSENT (ambiguous slot must be removed)", byTitleYear["heat|1995|movie"].RatingKey)
	}
	if _, ok := byTitle["heat|movie"]; ok {
		t.Errorf("byTitle[heat|movie] = %q, want ABSENT (ambiguous slot must be removed)", byTitle["heat|movie"].RatingKey)
	}
	// The two GUIDs are distinct, so neither collided; both are kept.
	if got := byGUID["imdb://tt0001"].RatingKey; got != "1" {
		t.Errorf("byGUID[tt0001].RatingKey = %q, want %q (distinct GUIDs both kept)", got, "1")
	}
	if got := byGUID["imdb://tt0002"].RatingKey; got != "2" {
		t.Errorf("byGUID[tt0002].RatingKey = %q, want %q (distinct GUIDs both kept)", got, "2")
	}
	logged := buf.String()
	if !strings.Contains(logged, "title+year index shadow") {
		t.Errorf("expected title+year shadow warn log on differing-key collision, got:\n%s", logged)
	}
	if !strings.Contains(logged, "title index shadow") {
		t.Errorf("expected title shadow debug log on differing-key collision, got:\n%s", logged)
	}
}

func TestBuildPlexIndex_SameKeyReindexedNoShadow(t *testing.T) {
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {
				{RatingKey: 7, Title: "Dune", Year: 2021, GUIDs: []string{"imdb://tt1"}},
				{RatingKey: 7, Title: "Dune", Year: 2021, GUIDs: []string{"imdb://tt1"}},
			},
		},
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, byTitleYear, _, _ := BuildPlexIndex(context.Background(), fetcher, 1)

	if got := byTitleYear["dune|2021|movie"].RatingKey; got != "7" {
		t.Errorf("byTitleYear[dune|2021|movie].RatingKey = %q, want %q", got, "7")
	}
	if strings.Contains(buf.String(), "shadow") {
		t.Errorf("did not expect a shadow log for an idempotent same-key re-add, got:\n%s", buf.String())
	}
}

// failingFetcher returns its sections verbatim but errors when LibraryAll is
// called for failSection, modeling a partial Plex outage where one section is
// briefly unavailable while others load.
type failingFetcher struct {
	sections    []Section
	items       map[string][]LibItem
	failSection string
}

func (f *failingFetcher) LibrarySections(_ context.Context) ([]Section, error) {
	return f.sections, nil
}

func (f *failingFetcher) LibraryAll(_ context.Context, sectionKey string) ([]LibItem, error) {
	if sectionKey == f.failSection {
		return nil, errors.New("section fetch failed")
	}
	return f.items[sectionKey], nil
}

// listErrorFetcher fails to even list the library sections.
type listErrorFetcher struct{}

func (listErrorFetcher) LibrarySections(_ context.Context) ([]Section, error) {
	return nil, errors.New("cannot list sections")
}

func (listErrorFetcher) LibraryAll(_ context.Context, _ string) ([]LibItem, error) {
	return nil, nil
}

func TestBuildPlexIndex_ReportsFailedSections(t *testing.T) {
	fetcher := &failingFetcher{
		sections: []Section{
			{Key: "1", Title: "Movies", Type: "movie"},
			{Key: "2", Title: "Movies 4K", Type: "movie"},
		},
		items: map[string][]LibItem{
			"1": {{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0001"}}},
		},
		failSection: "2",
	}

	_, byTitleYear, _, failed := BuildPlexIndex(context.Background(), fetcher, 1)

	if failed != 1 {
		t.Errorf("failedSections = %d, want 1 (one section errored)", failed)
	}
	if _, ok := byTitleYear["heat|1995|movie"]; !ok {
		t.Error("expected the section that loaded to still be indexed")
	}
}

func TestBuildPlexIndex_SectionListError(t *testing.T) {
	byGUID, byTitleYear, byTitle, failed := BuildPlexIndex(context.Background(), listErrorFetcher{}, 1)

	if failed == 0 {
		t.Error("expected non-zero failedSections when the section list fetch fails")
	}
	if len(byGUID) != 0 || len(byTitleYear) != 0 || len(byTitle) != 0 {
		t.Error("expected empty index when the section list fetch fails")
	}
}

func TestBuildPlexIndex_GUIDCollisionRefusesToMatch(t *testing.T) {
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {
				{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0113277"}},
				{RatingKey: 2, Title: "Heat 4K", Year: 1995, GUIDs: []string{"imdb://tt0113277"}},
			},
		},
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	byGUID, _, _, failed := BuildPlexIndex(context.Background(), fetcher, 1)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0 (fetcher never errors)", failed)
	}
	// The shared GUID collided on two different rating keys, so the slot is
	// ambiguous and removed (refuse to match) rather than kept as last-writer.
	if _, ok := byGUID["imdb://tt0113277"]; ok {
		t.Errorf("byGUID[imdb://tt0113277] = %q, want ABSENT (ambiguous GUID slot must be removed)", byGUID["imdb://tt0113277"].RatingKey)
	}
	logged := buf.String()
	if !strings.Contains(logged, "guid index shadow") {
		t.Errorf("expected guid index shadow debug log on same-GUID differing-key collision, got:\n%s", logged)
	}
	if strings.Contains(logged, "title index shadow") || strings.Contains(logged, "title+year index shadow") {
		t.Errorf("did not expect a title shadow when only the GUID collides, got:\n%s", logged)
	}
}

func TestBuildPlexIndex_ParallelismBelowOneStillIndexes(t *testing.T) {
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0113277"}}},
		},
	}

	byGUID, byTitleYear, _, failed := BuildPlexIndex(context.Background(), fetcher, 0)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0", failed)
	}
	if got := byGUID["imdb://tt0113277"].RatingKey; got != "1" {
		t.Errorf("byGUID[imdb://tt0113277].RatingKey = %q, want %q (indexed despite parallelism=0)", got, "1")
	}
	if got := byTitleYear["heat|1995|movie"].RatingKey; got != "1" {
		t.Errorf("byTitleYear[heat|1995|movie].RatingKey = %q, want %q", got, "1")
	}
}

// cancelOnFetchFetcher cancels the run context during the library fetch and then
// returns an error, modeling a shutdown that lands mid-scan: the fetch fails, but
// because the cause is cancellation (not Plex), the section must not count as failed.
type cancelOnFetchFetcher struct {
	sections []Section
	cancel   context.CancelFunc
}

func (f *cancelOnFetchFetcher) LibrarySections(_ context.Context) ([]Section, error) {
	return f.sections, nil
}

func (f *cancelOnFetchFetcher) LibraryAll(_ context.Context, _ string) ([]LibItem, error) {
	f.cancel()
	return nil, errors.New("fetch aborted")
}

func TestBuildPlexIndex_CancelledBeforeScanIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0113277"}}},
		},
	}
	byGUID, _, _, failed := BuildPlexIndex(ctx, fetcher, 1)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0 (a context cancelled before the scan is shutdown, not a Plex failure)", failed)
	}
	if len(byGUID) != 0 {
		t.Errorf("byGUID has %d entries, want 0 (a cancelled scan must index nothing)", len(byGUID))
	}
}

func TestBuildPlexIndex_CancelledMidFetchIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fetcher := &cancelOnFetchFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		cancel:   cancel,
	}
	_, _, _, failed := BuildPlexIndex(ctx, fetcher, 1)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0 (a fetch error caused by context cancellation is shutdown, not a Plex failure)", failed)
	}
}

func TestBuildPlexIndex_SkipsNonMovieShowSections(t *testing.T) {
	fetcher := &collidingFetcher{
		sections: []Section{
			{Key: "1", Title: "Movies", Type: "movie"},
			{Key: "2", Title: "Music", Type: "artist"},
		},
		items: map[string][]LibItem{
			"1": {{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0113277"}}},
			"2": {{RatingKey: 9, Title: "Some Album", Year: 2001, GUIDs: []string{"mbid://abc"}}},
		},
	}
	byGUID, byTitleYear, byTitle, failed := BuildPlexIndex(context.Background(), fetcher, 1)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0", failed)
	}
	if _, ok := byGUID["imdb://tt0113277"]; !ok {
		t.Error("expected the movie section to be indexed")
	}
	if _, ok := byGUID["mbid://abc"]; ok {
		t.Error("non-movie/show (artist) section must be skipped, but its GUID was indexed")
	}
	// If the skip guard regressed, the artist item would be indexed with an
	// empty media type (ParseMediaType("artist") == ""), i.e. under "some album|"
	// and "some album|2001|". Assert those exact slots stay absent.
	if _, ok := byTitle["some album|"]; ok {
		t.Error("non-movie/show (artist) section must be skipped, but its title was indexed")
	}
	if _, ok := byTitleYear["some album|2001|"]; ok {
		t.Error("non-movie/show (artist) section must be skipped, but its title+year was indexed")
	}
}

func TestBuildPlexIndex_TitleOnlyCollisionKeepsTitleYearMatchable(t *testing.T) {
	// Same title, DIFFERENT years: the title-only key "dune" collides (two
	// distinct rating keys) and is refused, but the two title+year slots do
	// NOT collide and must stay matchable. Pins that the three ambiguous sets
	// are independent -- a collision in the less-specific title index must not
	// poison the more-specific title+year index.
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {
				{RatingKey: 1, Title: "Dune", Year: 2021, GUIDs: []string{"imdb://tt1"}},
				{RatingKey: 2, Title: "Dune", Year: 1984, GUIDs: []string{"imdb://tt2"}},
			},
		},
	}

	byGUID, byTitleYear, byTitle, failed := BuildPlexIndex(context.Background(), fetcher, 1)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0 (fetcher never errors)", failed)
	}
	if _, ok := byTitle["dune|movie"]; ok {
		t.Errorf("byTitle[dune|movie] = %q, want ABSENT (title-only slot is ambiguous)", byTitle["dune|movie"].RatingKey)
	}
	if got := byTitleYear["dune|2021|movie"].RatingKey; got != "1" {
		t.Errorf("byTitleYear[dune|2021|movie].RatingKey = %q, want %q (non-colliding title+year slot must stay matchable)", got, "1")
	}
	if got := byTitleYear["dune|1984|movie"].RatingKey; got != "2" {
		t.Errorf("byTitleYear[dune|1984|movie].RatingKey = %q, want %q (non-colliding title+year slot must stay matchable)", got, "2")
	}
	if got := byGUID["imdb://tt1"].RatingKey; got != "1" {
		t.Errorf("byGUID[imdb://tt1].RatingKey = %q, want %q", got, "1")
	}
	if got := byGUID["imdb://tt2"].RatingKey; got != "2" {
		t.Errorf("byGUID[imdb://tt2].RatingKey = %q, want %q", got, "2")
	}
}

func TestMatch_CrossTypeSameTitleYear_RecoversMovieMatch(t *testing.T) {
	// Plex holds a Movie "Dune" 2021 (key 10) AND a Show "Dune" 2021 (key 20).
	// Before the index keys folded in media type, both occupied the title+year
	// key "dune|2021" and collided, so the slot was pruned (refuse-to-match) and
	// a stale Movie "Dune" 2021 whose GUID no longer resolves was MISSED. With
	// the media type in the key the two occupy distinct slots, so the stale Movie
	// now matches the Movie (key 10) and never the Show.
	fetcher := &collidingFetcher{
		sections: []Section{
			{Key: "1", Title: "Movies", Type: "movie"},
			{Key: "2", Title: "Shows", Type: "show"},
		},
		items: map[string][]LibItem{
			"1": {{RatingKey: 10, Title: "Dune", Year: 2021, GUIDs: []string{"imdb://tt-movie"}}},
			"2": {{RatingKey: 20, Title: "Dune", Year: 2021, GUIDs: []string{"tvdb://show"}}},
		},
	}

	byGUID, byTitleYear, byTitle, failed := BuildPlexIndex(context.Background(), fetcher, 1)
	if failed != 0 {
		t.Fatalf("failedSections = %d, want 0 (fetcher never errors)", failed)
	}
	// Both type-specific title+year slots survive: distinct keys, no collision.
	if got := byTitleYear["dune|2021|movie"].RatingKey; got != "10" {
		t.Errorf("byTitleYear[dune|2021|movie] = %q, want 10 (movie slot must survive cross-type coexistence)", got)
	}
	if got := byTitleYear["dune|2021|show"].RatingKey; got != "20" {
		t.Errorf("byTitleYear[dune|2021|show] = %q, want 20 (show slot must survive cross-type coexistence)", got)
	}

	// Stale Movie whose GUID no longer resolves (absent from byGUID) falls back
	// to title+year and must land on the Movie, not the Show.
	stale := map[string]TautulliEntry{
		"99": {RatingKey: "99", Title: "Dune", Year: "2021", MediaType: Movie, GUID: "imdb://stale-gone"},
	}
	matched, unmatched := MatchStaleItems(stale, byGUID, byTitleYear, byTitle, true, false)
	if len(matched) != 1 || len(unmatched) != 0 {
		t.Fatalf("matched=%d unmatched=%d, want 1/0 (recovered same-type match)", len(matched), len(unmatched))
	}
	if matched[0].NewKey != "10" {
		t.Errorf("NewKey = %q, want 10 (the Movie, never the Show key 20)", matched[0].NewKey)
	}
	if matched[0].Method != MethodTitleYear {
		t.Errorf("Method = %q, want %q", matched[0].Method, MethodTitleYear)
	}
}

func TestMatch_SameTypeTitleYearTwin_StillRefusesToMatch(t *testing.T) {
	// Companion to the cross-type recovery test: two Movies "Dune" 2021 (keys 10
	// and 11) genuinely collide on the type-keyed slot "dune|2021|movie", so it
	// stays pruned and a stale Movie "Dune" 2021 remains unmatched. Folding the
	// media type into the key must not weaken same-type ambiguity detection.
	fetcher := &collidingFetcher{
		sections: []Section{{Key: "1", Title: "Movies", Type: "movie"}},
		items: map[string][]LibItem{
			"1": {
				{RatingKey: 10, Title: "Dune", Year: 2021, GUIDs: []string{"imdb://tt-a"}},
				{RatingKey: 11, Title: "Dune", Year: 2021, GUIDs: []string{"imdb://tt-b"}},
			},
		},
	}

	byGUID, byTitleYear, byTitle, failed := BuildPlexIndex(context.Background(), fetcher, 1)
	if failed != 0 {
		t.Fatalf("failedSections = %d, want 0 (fetcher never errors)", failed)
	}
	if _, ok := byTitleYear["dune|2021|movie"]; ok {
		t.Errorf("byTitleYear[dune|2021|movie] present, want ABSENT (same-type twins must refuse-to-match)")
	}

	stale := map[string]TautulliEntry{
		"99": {RatingKey: "99", Title: "Dune", Year: "2021", MediaType: Movie, GUID: "imdb://stale-gone"},
	}
	matched, unmatched := MatchStaleItems(stale, byGUID, byTitleYear, byTitle, true, false)
	if len(matched) != 0 || len(unmatched) != 1 {
		t.Fatalf("matched=%d unmatched=%d, want 0/1 (ambiguous slot pruned)", len(matched), len(unmatched))
	}
}

func TestBuildPlexIndex_CrossSectionGUIDCollisionRefusesToMatch(t *testing.T) {
	// Two DIFFERENT movie sections each carry the same GUID with a different
	// rating key, scanned CONCURRENTLY (parallelism 2). The ambiguous-set
	// design must refuse to match regardless of which goroutine's add lands
	// last -- the order-independence the package claims over the former
	// last-writer-wins behavior. Every other BuildPlexIndex test runs at
	// parallelism 0 or 1, so this is the only exercise of the concurrent
	// fan-out path (and validates the add mutex under -race). Distinct titles
	// keep the collision isolated to byGUID.
	fetcher := &collidingFetcher{
		sections: []Section{
			{Key: "1", Title: "Movies", Type: "movie"},
			{Key: "2", Title: "Movies 4K", Type: "movie"},
		},
		items: map[string][]LibItem{
			"1": {{RatingKey: 1, Title: "Heat", Year: 1995, GUIDs: []string{"imdb://tt0113277"}}},
			"2": {{RatingKey: 2, Title: "Heat 4K", Year: 1995, GUIDs: []string{"imdb://tt0113277"}}},
		},
	}
	byGUID, _, _, failed := BuildPlexIndex(context.Background(), fetcher, 2)
	if failed != 0 {
		t.Errorf("failedSections = %d, want 0 (fetcher never errors)", failed)
	}
	if _, ok := byGUID["imdb://tt0113277"]; ok {
		t.Errorf("byGUID[imdb://tt0113277] = %q, want ABSENT (cross-section GUID collision must refuse-to-match under concurrency)", byGUID["imdb://tt0113277"].RatingKey)
	}
}
