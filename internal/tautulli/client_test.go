package tautulli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestClient creates a Client with fast retry for tests.
func newTestClient(url, apiKey string, httpClient *http.Client) *Client {
	c := New(url, apiKey, httpClient)
	c.RetryDelayUnit = time.Millisecond
	return c
}

func TestAPI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "get_history" {
			t.Errorf("unexpected cmd: %s", r.URL.Query().Get("cmd"))
		}
		if r.URL.Query().Get("apikey") != "testkey" {
			t.Errorf("unexpected apikey: %s", r.URL.Query().Get("apikey"))
		}
		w.Write([]byte(`{"response":{"result":"success"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "testkey", srv.Client())
	body, err := c.API(context.Background(), "get_history", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "success") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestAPI_Non200ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "testkey", srv.Client())
	_, err := c.API(context.Background(), "get_history", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAPI_ExtraParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "100" {
			t.Errorf("expected start=100, got %s", r.URL.Query().Get("start"))
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	extra := url.Values{"start": {"100"}}
	_, err := c.API(context.Background(), "test", extra)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPI_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(srv.URL, "k", srv.Client())
	_, err := c.API(ctx, "test", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestAPI_NonRetryable4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "testkey", srv.Client())
	_, err := c.APIWithRetry(context.Background(), "backup_db", nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
}

func TestAPI_InvalidURL(t *testing.T) {
	c := newTestClient("://invalid-url", "key", &http.Client{})
	_, err := c.API(context.Background(), "test", nil)
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestAPI_ExtraOverridesBaseParams(t *testing.T) {
	var gotCmd, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCmd = r.URL.Query().Get("cmd")
		gotAPIKey = r.URL.Query().Get("apikey")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "base-key", srv.Client())
	extra := url.Values{"cmd": {"override_cmd"}, "apikey": {"override-key"}}
	_, err := c.API(context.Background(), "base_cmd", extra)
	if err != nil {
		t.Fatal(err)
	}
	if gotCmd != "override_cmd" {
		t.Errorf("cmd = %q, want override_cmd", gotCmd)
	}
	if gotAPIKey != "override-key" {
		t.Errorf("apikey = %q, want override-key", gotAPIKey)
	}
}

func TestAPI_ErrorDoesNotLeakAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Skip("server doesn't support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "supersecretkey123", srv.Client())
	_, err := c.API(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "supersecretkey123") {
		t.Errorf("error message leaked API key: %s", err.Error())
	}
}

func TestAPIWithRetry_SucceedsOnFirst(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	body, err := c.APIWithRetry(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "ok") {
		t.Errorf("unexpected body: %s", body)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestAPIWithRetry_RetriesOnServerError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	body, err := c.APIWithRetry(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Errorf("unexpected body: %s", body)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestAPIWithRetry_Exact3Attempts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.APIWithRetry(ctx, "test_cmd", nil)
	if err == nil {
		t.Error("expected error from all-failing retries")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestAPIWithRetry_SuccessOnSecondAttempt(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"response":{"result":"success"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	body, err := c.APIWithRetry(context.Background(), "test_cmd", nil)
	if err != nil {
		t.Fatal(err)
	}
	if body == nil {
		t.Error("expected non-nil body")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestAPIWithRetry_CancelledContextStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(srv.URL, "k", srv.Client())
	_, err := c.APIWithRetry(ctx, "test", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetHistory_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":{"result":"success","data":{
			"recordsFiltered":1,
			"data":[{"rating_key":42,"title":"Movie","year":2020,"media_type":"movie","guid":""}]
		}}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	page, err := c.GetHistory(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if page.RecordsFiltered != 1 {
		t.Errorf("RecordsFiltered = %d, want 1", page.RecordsFiltered)
	}
	if len(page.Rows) != 1 {
		t.Errorf("Rows = %d, want 1", len(page.Rows))
	}
}

func TestGetHistory_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":{"result":"error","message":"bad request"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "k", srv.Client())
	_, err := c.GetHistory(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for non-success result")
	}
}
