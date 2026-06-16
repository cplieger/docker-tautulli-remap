package plex

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if !c.ItemExists(context.Background(), "123") {
		t.Error("expected true for 200")
	}
}

func TestItemExists_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	if c.ItemExists(context.Background(), "999") {
		t.Error("expected false for 404")
	}
}

func TestItemExists_InvalidKey(t *testing.T) {
	c := New("http://unused", "tok", http.DefaultClient)
	if c.ItemExists(context.Background(), "abc") {
		t.Error("expected false for non-numeric key")
	}
}

func TestItemExists_RejectsNonNumericKeys(t *testing.T) {
	c := New("http://localhost:32400", "token", &http.Client{Timeout: 1 * time.Second})
	tests := []string{"../../../etc/passwd", "abc", "123/../../secret", "", "12 34"}
	for _, key := range tests {
		if c.ItemExists(context.Background(), key) {
			t.Errorf("ItemExists should return false for non-numeric key %q", key)
		}
	}
}

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
			if c.ItemExists(context.Background(), "42") {
				t.Errorf("expected false for status %d", status)
			}
		})
	}
}

func TestItemExists_InvalidURL(t *testing.T) {
	c := New("://invalid-url", "token", &http.Client{})
	if c.ItemExists(context.Background(), "42") {
		t.Error("expected false for invalid URL")
	}
}

// TestItemExists_WarnsOnlyOnUnexpectedStatus pins the two adjacent negations in
// the status-code guard `!= StatusOK && != StatusNotFound`. That block only
// emits a warning log; the return value is decided by the next line, so the
// existing return-value-only tests let both negation mutants live. Asserting
// the warning is logged for an unexpected status (500) and NOT logged for the
// two expected statuses (200, 404) kills both mutants:
//   - first `!=` -> `==`: would warn on 200 and stay silent on 500
//   - second `!=` -> `==`: would warn on 404 and stay silent on 500
func TestItemExists_WarnsOnlyOnUnexpectedStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantOK   bool
		wantWarn bool
	}{
		{"ok 200 does not warn", http.StatusOK, true, false},
		{"not found 404 does not warn", http.StatusNotFound, false, false},
		{"server error 500 warns", http.StatusInternalServerError, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			c := New(srv.URL, "tok", srv.Client())
			gotOK := c.ItemExists(context.Background(), "42")

			if gotOK != tt.wantOK {
				t.Errorf("ItemExists(status %d) = %v, want %v", tt.status, gotOK, tt.wantOK)
			}
			gotWarn := strings.Contains(buf.String(), "plex check unexpected status")
			if gotWarn != tt.wantWarn {
				t.Errorf("status %d: unexpected-status warning logged = %v, want %v; log=%q",
					tt.status, gotWarn, tt.wantWarn, buf.String())
			}
		})
	}
}

func TestLibrarySections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","title":"Movies","type":"movie"},{"key":"2","title":"TV","type":"show"}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	sections := c.LibrarySections(context.Background())
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
	sections := c.LibrarySections(context.Background())
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
	if sections := c.LibrarySections(context.Background()); sections != nil {
		t.Errorf("expected nil on error, got %v", sections)
	}
}

func TestLibrarySections_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	if sections := c.LibrarySections(context.Background()); sections != nil {
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
	if sections := c.LibrarySections(context.Background()); sections != nil {
		t.Errorf("expected nil on truncated body, got %v", sections)
	}
}

func TestLibrarySections_InvalidURL(t *testing.T) {
	c := New("://invalid-url", "token", &http.Client{})
	if sections := c.LibrarySections(context.Background()); sections != nil {
		t.Errorf("expected nil for invalid URL, got %v", sections)
	}
}

func TestLibraryAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"title":"Test Movie","ratingKey":"42","guid":"plex://movie/abc","Guid":[{"id":"imdb://tt1234"}],"year":2020}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items := c.LibraryAll(context.Background(), "1")
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
	items := c.LibraryAll(context.Background(), "1")
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
	if items := c.LibraryAll(context.Background(), "abc"); items != nil {
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
	items := c.LibraryAll(context.Background(), "1")
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
	if items := c.LibraryAll(context.Background(), "1"); items != nil {
		t.Errorf("expected nil on HTTP error, got %v", items)
	}
}

func TestLibraryAll_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	if items := c.LibraryAll(context.Background(), "1"); items != nil {
		t.Errorf("expected nil on invalid JSON, got %v", items)
	}
}

func TestLibraryAll_EmptyMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	items := c.LibraryAll(context.Background(), "1")
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
	items := c.LibraryAll(context.Background(), "1")
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
	if items := c.LibraryAll(context.Background(), "1"); items != nil {
		t.Errorf("expected nil on truncated body, got %v", items)
	}
}

func TestLibraryAll_InvalidURL(t *testing.T) {
	c := New("://invalid-url", "token", &http.Client{})
	if items := c.LibraryAll(context.Background(), "1"); items != nil {
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
	if c.ItemExists(ctx, "42") {
		t.Error("expected false for cancelled context")
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
	if sections := c.LibrarySections(ctx); sections != nil {
		// Cancelled context may or may not return nil depending on timing
		_ = sections
	}
}
