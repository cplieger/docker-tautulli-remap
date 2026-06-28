package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestItemExists_Valid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Plex-Token") != "tok" {
			t.Error("missing token header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	exists, err := c.ItemExists(context.Background(), "123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected true for 200")
	}
}

func TestItemExists_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	exists, err := c.ItemExists(context.Background(), "999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false for 404")
	}
}

func TestItemExists_RejectsNonNumericKeys(t *testing.T) {
	c := New("http://localhost:32400", "token", &http.Client{Timeout: 1 * time.Second})
	tests := []string{"../../../etc/passwd", "abc", "123/../../secret", "", "12 34"}
	for _, key := range tests {
		exists, err := c.ItemExists(context.Background(), key)
		if exists {
			t.Errorf("ItemExists should return false for non-numeric key %q", key)
		}
		if err != nil {
			t.Errorf("non-numeric key %q is not-exists, not a Plex error; got err = %v", key, err)
		}
	}
}

// TestItemExists_UnexpectedStatus pins FIX 6's fail-closed contract: a status
// that is neither 200 nor 404 yields (false, non-nil error) so the caller does
// not silently treat an unverifiable item as not-exists. 502/503 are retried
// (transient) and 500/401 surface immediately, but all return an error.
func TestItemExists_UnexpectedStatus(t *testing.T) {
	statuses := []int{
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
		http.StatusUnauthorized,
		http.StatusBadGateway,
	}
	for _, status := range statuses {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()
			c := New(srv.URL, "tok", srv.Client())
			exists, err := c.ItemExists(context.Background(), "42")
			if exists {
				t.Errorf("expected false for status %d", status)
			}
			if err == nil {
				t.Errorf("expected non-nil error for status %d (fail-closed: undetermined existence must not read as not-exists)", status)
			}
		})
	}
}

func TestItemExists_InvalidURL(t *testing.T) {
	c := New("://invalid-url", "token", &http.Client{})
	exists, err := c.ItemExists(context.Background(), "42")
	if exists {
		t.Error("expected false for invalid URL")
	}
	if err == nil {
		t.Error("expected non-nil error for invalid URL")
	}
}

func TestLibrarySections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","title":"Movies","type":"movie"},{"key":"2","title":"TV","type":"show"}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Title != "Movies" {
		t.Errorf("unexpected title: %s", sections[0].Title)
	}
}

func TestLibrarySections_ThreeSections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Directory":[
			{"key":"1","title":"Movies","type":"movie"},
			{"key":"2","title":"TV Shows","type":"show"},
			{"key":"3","title":"Music","type":"artist"}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	if sections[0].Title != "Movies" || sections[0].Type != "movie" {
		t.Errorf("unexpected first section: %+v", sections[0])
	}
}

func TestLibrarySections_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(context.Background())
	if err == nil {
		t.Error("expected error on non-200, got nil")
	}
	if sections != nil {
		t.Errorf("expected nil on error, got %v", sections)
	}
}

func TestLibrarySections_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(context.Background())
	if err == nil {
		t.Error("expected error on invalid JSON, got nil")
	}
	if sections != nil {
		t.Errorf("expected nil on invalid JSON, got %v", sections)
	}
}

func TestLibrarySections_ReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "short")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(context.Background())
	if err == nil {
		t.Error("expected error on truncated body, got nil")
	}
	if sections != nil {
		t.Errorf("expected nil on truncated body, got %v", sections)
	}
}

func TestLibrarySections_InvalidURL(t *testing.T) {
	c := New("://invalid-url", "token", &http.Client{})
	sections, err := c.LibrarySections(context.Background())
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
	if sections != nil {
		t.Errorf("expected nil for invalid URL, got %v", sections)
	}
}

func TestLibraryAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"title":"Test Movie","ratingKey":"42","guid":"plex://movie/abc","Guid":[{"id":"imdb://tt1234"}],"year":2020}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].RatingKey != 42 {
		t.Errorf("unexpected rating key: %d", items[0].RatingKey)
	}
	if items[0].Title != "Test Movie" {
		t.Errorf("unexpected title: %s", items[0].Title)
	}
}

func TestLibraryAll_WithGUIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[
			{
				"title":"The Matrix","ratingKey":"42","year":1999,
				"guid":"plex://movie/5d776b59ad5437001f79c6f8",
				"Guid":[{"id":"imdb://tt0133093"},{"id":"tmdb://603"}]
			}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].GUIDs) != 3 {
		t.Errorf("expected 3 GUIDs, got %d: %v", len(items[0].GUIDs), items[0].GUIDs)
	}
}

func TestLibraryAll_NonNumericSectionKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not make HTTP request for non-numeric key")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "abc")
	if err == nil {
		t.Error("expected error for non-numeric key, got nil")
	}
	if items != nil {
		t.Errorf("expected nil for non-numeric key, got %v", items)
	}
}

func TestLibraryAll_SkipsInvalidRatingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[
			{"title":"Bad","ratingKey":"abc","year":2020},
			{"title":"Good","ratingKey":"42","year":2020}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item (bad key skipped), got %d", len(items))
	}
	if items[0].Title != "Good" {
		t.Errorf("expected Good, got %s", items[0].Title)
	}
}

func TestLibraryAll_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err == nil {
		t.Error("expected error on HTTP error, got nil")
	}
	if items != nil {
		t.Errorf("expected nil on HTTP error, got %v", items)
	}
}

func TestLibraryAll_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err == nil {
		t.Error("expected error on invalid JSON, got nil")
	}
	if items != nil {
		t.Errorf("expected nil on invalid JSON, got %v", items)
	}
}

func TestLibraryAll_EmptyMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestLibraryAll_UnsupportedGUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[
			{"title":"Local Item","ratingKey":"42","year":2020,
			 "guid":"local://12345","Guid":[{"id":"local://67890"}]}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].GUIDs) != 0 {
		t.Errorf("expected 0 GUIDs for unsupported format, got %d", len(items[0].GUIDs))
	}
}

func TestLibraryAll_ReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "short")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err == nil {
		t.Error("expected error on truncated body, got nil")
	}
	if items != nil {
		t.Errorf("expected nil on truncated body, got %v", items)
	}
}

func TestLibraryAll_InvalidURL(t *testing.T) {
	c := New("://invalid-url", "token", &http.Client{})
	items, err := c.LibraryAll(context.Background(), "1")
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
	if items != nil {
		t.Errorf("expected nil for invalid URL, got %v", items)
	}
}

func TestItemExists_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(srv.URL, "tok", srv.Client())
	exists, err := c.ItemExists(ctx, "42")
	if exists {
		t.Error("expected false for cancelled context")
	}
	if err == nil {
		t.Error("expected non-nil error for cancelled context")
	}
}

func TestLibrarySections_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Directory":[]}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(ctx)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
	if sections != nil {
		t.Errorf("expected nil for cancelled context, got %v", sections)
	}
}

func TestLibraryAll_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(ctx, "1")
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
	if items != nil {
		t.Errorf("expected nil for cancelled context, got %v", items)
	}
}

func TestItemExists_RetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	exists, err := c.ItemExists(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error after a transient 503 was retried: %v", err)
	}
	if !exists {
		t.Error("expected true: a 503 retried to a 200 must report the item exists")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 503 then one 200, confirming ItemExists actually retried the transient failure)", calls)
	}
}

func TestLibrarySections_RetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections, err := c.LibrarySections(context.Background())
	if err != nil {
		t.Fatalf("unexpected error after a transient 503 was retried: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("expected 1 section after retry, got %d", len(sections))
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 503 then one 200, confirming LibrarySections retried the transient failure)", calls)
	}
}

func TestLibraryAll_RetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"title":"Test Movie","ratingKey":"42","year":2020}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items, err := c.LibraryAll(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error after a transient 503 was retried: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after retry, got %d", len(items))
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 503 then one 200, confirming LibraryAll retried the transient failure)", calls)
	}
}
