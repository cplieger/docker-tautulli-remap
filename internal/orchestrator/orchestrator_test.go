package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"tautulli-remap/internal/config"
	"tautulli-remap/internal/plex"
	"tautulli-remap/internal/remap"
	"tautulli-remap/internal/tautulli"
)

// --- Fake implementations for unit tests ---

type fakePlex struct{}

func (f *fakePlex) ItemExists(_ context.Context, _ string) bool { return true }
func (f *fakePlex) LibrarySections(_ context.Context) []plex.Section {
	return []plex.Section{{Key: "1", Title: "Movies", Type: "movie"}}
}
func (f *fakePlex) LibraryAll(_ context.Context, _ string) []plex.LibItem {
	return []plex.LibItem{{RatingKey: 1, Title: "Test", Year: 2020, GUIDs: []string{"imdb://tt0001"}}}
}

type fakeTautulli struct{ calls []string }

func (f *fakeTautulli) API(_ context.Context, cmd string, _ url.Values) ([]byte, error) {
	f.calls = append(f.calls, cmd)
	return []byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`), nil
}
func (f *fakeTautulli) APIWithRetry(ctx context.Context, cmd string, params url.Values) ([]byte, error) {
	return f.API(ctx, cmd, params)
}
func (f *fakeTautulli) GetHistory(_ context.Context, _ url.Values) (*tautulli.HistoryPage, error) {
	f.calls = append(f.calls, "get_history")
	return &tautulli.HistoryPage{Rows: nil, RecordsFiltered: 0}, nil
}
func (f *fakeTautulli) UpdateMetadata(_ context.Context, _, _ string, _ remap.MediaType) error {
	f.calls = append(f.calls, "update_metadata_details")
	return nil
}
func (f *fakeTautulli) DeleteRecentlyAdded(_ context.Context) error {
	f.calls = append(f.calls, "delete_recently_added")
	return nil
}

func TestNew(t *testing.T) {
	o := New(&fakePlex{}, &fakeTautulli{}, &config.Config{})
	if o == nil {
		t.Fatal("expected non-nil orchestrator")
	}
}

func TestRun_DryRun_NoStale(t *testing.T) {
	ft := &fakeTautulli{}
	o := New(&fakePlex{}, ft, &config.Config{DryRun: true})
	o.PaginationDelay = time.Millisecond
	ok := o.Run(context.Background())
	if !ok {
		t.Error("expected success when all keys are valid")
	}
}

// --- Integration tests using httptest servers ---

func testServer(t *testing.T, handler http.HandlerFunc) *config.Config {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &config.Config{
		TautulliURL:    srv.URL,
		TautulliAPIKey: "test-key",
		PlexURL:        srv.URL,
		PlexToken:      "test-token",
		DryRun:         true,
	}
}

func newOrch(t *testing.T, cfg *config.Config) *Orchestrator {
	t.Helper()
	pc := plex.New(cfg.PlexURL, cfg.PlexToken, &http.Client{})
	tc := tautulli.New(cfg.TautulliURL, cfg.TautulliAPIKey, &http.Client{})
	tc.RetryDelayUnit = time.Millisecond
	o := New(pc, tc, cfg)
	o.PaginationDelay = time.Millisecond
	return o
}

func TestCollectTautulliItems_SinglePage(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":{"result":"success","data":{
			"recordsFiltered":2,
			"data":[
				{"rating_key":42,"title":"Movie A","year":2020,"media_type":"movie","guid":"imdb://tt1111111"},
				{"rating_key":99,"grandparent_rating_key":50,"title":"Ep 1","grandparent_title":"Show B","year":2021,"media_type":"episode","guid":"tvdb://271557"}
			]
		}}}`))
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items == nil {
		t.Fatal("expected non-nil items")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items["42"].Title != "Movie A" {
		t.Errorf("unexpected movie title: %s", items["42"].Title)
	}
	if items["50"].MediaType != remap.Show {
		t.Errorf("expected show, got %s", items["50"].MediaType)
	}
}

func TestCollectTautulliItems_MultiPage(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		start := r.URL.Query().Get("start")
		if start == "0" {
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1500,
				"data":[{"rating_key":1,"title":"Movie 1","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		} else {
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1500,
				"data":[{"rating_key":2,"title":"Movie 2","year":2021,"media_type":"movie","guid":""}]
			}}}`))
		}
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items == nil {
		t.Fatal("expected non-nil items")
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
	if calls < 2 {
		t.Errorf("expected at least 2 API calls, got %d", calls)
	}
}

func TestCollectTautulliItems_APIError(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":{"result":"error","message":"bad request"}}`))
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items != nil {
		t.Errorf("expected nil on API error, got %v", items)
	}
}

func TestCollectTautulliItems_InvalidJSON(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`not json`))
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items != nil {
		t.Errorf("expected nil on invalid JSON, got %v", items)
	}
}

func TestCollectTautulliItems_EmptyData(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items == nil {
		t.Fatal("expected non-nil map")
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestCollectTautulliItems_HTTPFailure(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items != nil {
		t.Errorf("expected nil on HTTP failure, got %v", items)
	}
}

func TestCollectTautulliItems_ExceedsMaxRecords(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":{"result":"success","data":{
			"recordsFiltered":500001,
			"data":[{"rating_key":1,"title":"M","year":2020,"media_type":"movie","guid":""}]
		}}}`))
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items != nil {
		t.Errorf("expected nil when records exceed cap, got %d items", len(items))
	}
}

func TestCollectTautulliItems_ExactPageBoundary(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		start := r.URL.Query().Get("start")
		if start == "0" {
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1000,
				"data":[{"rating_key":1,"title":"M","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		} else {
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":1000,"data":[]}}}`))
		}
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items == nil {
		t.Fatal("expected non-nil items")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (start=1000 >= total=1000 should break)", calls)
	}
}

func TestCollectTautulliItems_PaginationIncrement(t *testing.T) {
	startValues := []string{}
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		startValues = append(startValues, r.URL.Query().Get("start"))
		start := r.URL.Query().Get("start")
		switch start {
		case "0", "1000":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":2500,
				"data":[{"rating_key":1,"title":"M","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		case "2000":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":2500,
				"data":[{"rating_key":2,"title":"N","year":2021,"media_type":"movie","guid":""}]
			}}}`))
		default:
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":2500,"data":[]}}}`))
		}
	})
	orch := newOrch(t, cfg)
	items, _ := orch.CollectTautulliItems(context.Background())
	if items == nil {
		t.Fatal("expected non-nil items")
	}
	if len(startValues) != 3 {
		t.Errorf("expected 3 API calls, got %d (starts: %v)", len(startValues), startValues)
	}
	want := []string{"0", "1000", "2000"}
	for i, s := range want {
		if i < len(startValues) && startValues[i] != s {
			t.Errorf("call %d: start = %q, want %q", i, startValues[i], s)
		}
	}
}

func TestCollectTautulliItems_CancelDuringPagination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			defer cancel()
		}
		w.Write([]byte(`{"response":{"result":"success","data":{
			"recordsFiltered":5000,
			"data":[{"rating_key":1,"title":"M","year":2020,"media_type":"movie","guid":""}]
		}}}`))
	})
	orch := newOrch(t, cfg)
	orch.PaginationDelay = 200 * time.Millisecond
	items, _ := orch.CollectTautulliItems(ctx)
	if items != nil {
		t.Errorf("expected nil on ctx cancel during pagination, got %d items", len(items))
	}
	if calls != 1 {
		t.Errorf("expected 1 API call before cancel, got %d", calls)
	}
}

func TestFindStaleKeys_IdentifiesStaleAndValid(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/library/metadata/42") {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	orch := newOrch(t, cfg)
	items := map[string]remap.TautulliEntry{
		"42":  {RatingKey: "42", Title: "Valid", MediaType: remap.Movie},
		"999": {RatingKey: "999", Title: "Stale", MediaType: remap.Movie},
	}
	stale := orch.FindStaleKeys(context.Background(), items)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale, got %d", len(stale))
	}
	if _, ok := stale["999"]; !ok {
		t.Error("expected key 999 to be stale")
	}
}

func TestFindStaleKeys_AllValid(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	orch := newOrch(t, cfg)
	items := map[string]remap.TautulliEntry{
		"1": {RatingKey: "1", Title: "A", MediaType: remap.Movie},
	}
	stale := orch.FindStaleKeys(context.Background(), items)
	if len(stale) != 0 {
		t.Errorf("expected 0 stale, got %d", len(stale))
	}
}

func TestFindStaleKeys_CancelledContext(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	orch := newOrch(t, cfg)
	items := map[string]remap.TautulliEntry{
		"1": {RatingKey: "1", Title: "A", MediaType: remap.Movie},
	}
	_ = orch.FindStaleKeys(ctx, items)
}

func TestFindStaleKeys_ProgressLogBoundary(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	orch := newOrch(t, cfg)
	items := map[string]remap.TautulliEntry{}
	for i := 1; i <= 250; i++ {
		k := strconv.Itoa(i)
		items[k] = remap.TautulliEntry{RatingKey: k, Title: "M", MediaType: remap.Movie}
	}
	stale := orch.FindStaleKeys(context.Background(), items)
	if len(stale) != 250 {
		t.Errorf("expected 250 stale, got %d", len(stale))
	}
}

func TestBuildPlexIndex_AllThreeIndexes(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"1","title":"Movies","type":"movie"},
				{"key":"2","title":"TV Shows","type":"show"},
				{"key":"3","title":"Music","type":"artist"}
			]}}`))
		case strings.Contains(r.URL.Path, "/sections/1/all"):
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"title":"The Matrix","ratingKey":"42","year":1999,
				 "guid":"plex://movie/abc","Guid":[{"id":"imdb://tt0133093"}]}
			]}}`))
		case strings.Contains(r.URL.Path, "/sections/2/all"):
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"title":"Breaking Bad","ratingKey":"99","year":2008,
				 "guid":"plex://show/def","Guid":[{"id":"tvdb://81189"}]}
			]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	pc := plex.New(cfg.PlexURL, cfg.PlexToken, &http.Client{})
	byGUID, byTitleYear, byTitle := remap.BuildPlexIndex(context.Background(), pc, 8)

	if _, ok := byGUID["imdb://tt0133093"]; !ok {
		t.Error("expected imdb GUID in byGUID")
	}
	if _, ok := byGUID["plex://movie/abc"]; !ok {
		t.Error("expected plex movie GUID in byGUID")
	}
	if _, ok := byGUID["tvdb://81189"]; !ok {
		t.Error("expected tvdb GUID in byGUID")
	}
	if _, ok := byTitleYear["the matrix|1999"]; !ok {
		t.Error("expected 'the matrix|1999' in byTitleYear")
	}
	if _, ok := byTitleYear["breaking bad|2008"]; !ok {
		t.Error("expected 'breaking bad|2008' in byTitleYear")
	}
	if _, ok := byTitle["the matrix"]; !ok {
		t.Error("expected 'the matrix' in byTitle")
	}
	if _, ok := byTitle["breaking bad"]; !ok {
		t.Error("expected 'breaking bad' in byTitle")
	}
}

func TestBuildPlexIndex_SkipsNonMovieShowSections(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/library/sections") {
			w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"3","title":"Music","type":"artist"}
			]}}`))
		} else {
			t.Error("should not fetch library items for non-movie/show sections")
			w.WriteHeader(http.StatusNotFound)
		}
	})
	pc := plex.New(cfg.PlexURL, cfg.PlexToken, &http.Client{})
	byGUID, byTitleYear, byTitle := remap.BuildPlexIndex(context.Background(), pc, 8)
	if len(byGUID) != 0 || len(byTitleYear) != 0 || len(byTitle) != 0 {
		t.Error("expected empty indexes for music-only library")
	}
}

func TestBuildPlexIndex_CancelBetweenSections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sectionAllHits := 0
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"1","title":"Movies","type":"movie"},
				{"key":"2","title":"TV","type":"show"}
			]}}`))
		case strings.Contains(r.URL.Path, "/sections/"):
			sectionAllHits++
			cancel()
			w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
		}
	})
	pc := plex.New(cfg.PlexURL, cfg.PlexToken, &http.Client{})
	byGUID, byTitleYear, byTitle := remap.BuildPlexIndex(ctx, pc, 8)
	if sectionAllHits != 1 {
		t.Errorf("expected 1 section fetch before cancel, got %d", sectionAllHits)
	}
	if len(byGUID) != 0 || len(byTitleYear) != 0 || len(byTitle) != 0 {
		t.Errorf("expected empty indexes on early cancel")
	}
}

func TestApplyRemappings_DryRun(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{}`))
	})
	cfg.DryRun = true
	orch := newOrch(t, cfg)
	matched := []remap.MatchResult{
		{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: remap.Movie, Method: remap.MethodGUID},
	}
	orch.ApplyRemappings(context.Background(), matched, nil)
	if calls != 0 {
		t.Errorf("expected 0 API calls in dry run, got %d", calls)
	}
}

func TestApplyRemappings_LiveRemap(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("cmd") != "update_metadata_details" {
			t.Errorf("unexpected cmd: %s", r.URL.Query().Get("cmd"))
		}
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	matched := []remap.MatchResult{
		{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: remap.Movie, Method: remap.MethodGUID},
	}
	orch.ApplyRemappings(context.Background(), matched, nil)
	if calls != 1 {
		t.Errorf("expected 1 API call, got %d", calls)
	}
}

func TestApplyRemappings_APIError(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	matched := []remap.MatchResult{
		{Title: "Movie A", Year: "2020", OldKey: "100", NewKey: "200", MediaType: remap.Movie, Method: remap.MethodGUID},
	}
	orch.ApplyRemappings(context.Background(), matched, nil)
}

func TestApplyRemappings_EmptyMatched(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("should not call API with empty matched")
	})
	orch := newOrch(t, cfg)
	orch.ApplyRemappings(context.Background(), nil, nil)
}

func TestApplyRemappings_CancelledContext(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})
	cfg.DryRun = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	orch := newOrch(t, cfg)
	matched := []remap.MatchResult{
		{Title: "A", OldKey: "1", NewKey: "2", MediaType: remap.Movie, Method: remap.MethodGUID},
		{Title: "B", OldKey: "3", NewKey: "4", MediaType: remap.Movie, Method: remap.MethodGUID},
	}
	orch.ApplyRemappings(ctx, matched, nil)
	if calls > 1 {
		t.Errorf("expected at most 1 call with cancelled context, got %d", calls)
	}
}

func TestApplyRemappings_AbortsAfterConsecutiveFailures(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	matched := make([]remap.MatchResult, 15)
	for i := range matched {
		matched[i] = remap.MatchResult{
			Title: fmt.Sprintf("Movie %d", i), Year: "2020",
			OldKey: strconv.Itoa(i), NewKey: strconv.Itoa(100 + i),
			MediaType: remap.Movie, Method: remap.MethodGUID,
		}
	}
	updated, failed := orch.ApplyRemappings(context.Background(), matched, nil)
	if updated != 0 {
		t.Errorf("updated = %d, want 0", updated)
	}
	if calls > 10 {
		t.Errorf("calls = %d, want <= 10 (should abort after consecutive failures)", calls)
	}
	if failed != 10 {
		t.Errorf("failed = %d, want 10", failed)
	}
}

func TestClearRecentlyAdded_DryRun(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
	})
	cfg.DryRun = true
	orch := newOrch(t, cfg)
	orch.ClearRecentlyAdded(context.Background())
	if calls != 0 {
		t.Errorf("expected 0 API calls in dry run, got %d", calls)
	}
}

func TestClearRecentlyAdded_Live(t *testing.T) {
	calls := 0
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("cmd") != "delete_recently_added" {
			t.Errorf("unexpected cmd: %s", r.URL.Query().Get("cmd"))
		}
		w.Write([]byte(`{"response":{"result":"success"}}`))
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	orch.ClearRecentlyAdded(context.Background())
	if calls != 1 {
		t.Errorf("expected 1 API call, got %d", calls)
	}
}

func TestClearRecentlyAdded_HTTPError(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	orch.ClearRecentlyAdded(context.Background())
}

func TestRun_AllKeysValid(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("cmd") == "get_history":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1,
				"data":[{"rating_key":42,"title":"Movie","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		case strings.Contains(r.URL.Path, "/library/metadata/"):
			w.WriteHeader(http.StatusOK)
		default:
			w.Write([]byte(`{}`))
		}
	})
	cfg.DryRun = true
	orch := newOrch(t, cfg)
	if !orch.Run(context.Background()) {
		t.Error("expected true when all keys are valid")
	}
}

func TestRun_StaleKeysTriggerRemap(t *testing.T) {
	remapCalled := false
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		switch {
		case cmd == "get_history":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1,
				"data":[{"rating_key":100,"title":"Stale Movie","year":2020,"media_type":"movie","guid":"imdb://tt1111111"}]
			}}}`))
		case cmd == "update_metadata_details":
			remapCalled = true
			w.Write([]byte(`{"response":{"result":"success"}}`))
		case cmd == "delete_recently_added":
			w.Write([]byte(`{"response":{"result":"success"}}`))
		case strings.HasSuffix(r.URL.Path, "/library/metadata/100"):
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`))
		case strings.Contains(r.URL.Path, "/sections/1/all"):
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"title":"Stale Movie","ratingKey":"200","year":2020,
				 "guid":"plex://movie/x","Guid":[{"id":"imdb://tt1111111"}]}
			]}}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	if !orch.Run(context.Background()) {
		t.Error("expected true on successful remap")
	}
	if !remapCalled {
		t.Error("expected update_metadata_details to be called")
	}
}

func TestRun_HistoryFailure(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg.DryRun = true
	orch := newOrch(t, cfg)
	if orch.Run(context.Background()) {
		t.Error("expected false when history collection fails")
	}
}

func TestRun_DryRunSkipsBackup(t *testing.T) {
	backupCalled := false
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		if cmd == "backup_db" {
			backupCalled = true
		}
		if cmd == "get_history" {
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
			return
		}
		w.Write([]byte(`{}`))
	})
	cfg.DryRun = true
	orch := newOrch(t, cfg)
	orch.Run(context.Background())
	if backupCalled {
		t.Error("backup_db should not be called in dry run")
	}
}

func TestRun_NonDryRunCallsBackup(t *testing.T) {
	backupCalled := false
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		if cmd == "backup_db" {
			backupCalled = true
		}
		if cmd == "get_history" {
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
			return
		}
		w.Write([]byte(`{}`))
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	orch.Run(context.Background())
	if !backupCalled {
		t.Error("backup_db should be called when not in dry run")
	}
}

func TestRun_EmptyPlexIndex(t *testing.T) {
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		switch {
		case cmd == "get_history":
			w.Write([]byte(`{"response":{"result":"success","data":{
				"recordsFiltered":1,
				"data":[{"rating_key":100,"title":"Movie","year":2020,"media_type":"movie","guid":""}]
			}}}`))
		case strings.HasSuffix(r.URL.Path, "/library/metadata/100"):
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			w.Write([]byte(`{"MediaContainer":{"Directory":[]}}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	cfg.DryRun = true
	orch := newOrch(t, cfg)
	if orch.Run(context.Background()) {
		t.Error("expected false when Plex library index is empty")
	}
}

func TestRun_BackupFailureContinues(t *testing.T) {
	callCounts := map[string]int{}
	cfg := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		callCounts[cmd]++
		switch cmd {
		case "backup_db":
			w.WriteHeader(http.StatusInternalServerError)
		case "get_history":
			w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":0,"data":[]}}}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	cfg.DryRun = false
	orch := newOrch(t, cfg)
	if !orch.Run(context.Background()) {
		t.Error("expected true even when backup fails")
	}
	if callCounts["backup_db"] != 3 {
		t.Errorf("expected backup_db called 3 times via retry, got %d", callCounts["backup_db"])
	}
	if callCounts["get_history"] != 1 {
		t.Errorf("expected get_history called, got %d", callCounts["get_history"])
	}
}
