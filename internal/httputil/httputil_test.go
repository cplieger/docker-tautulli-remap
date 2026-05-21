package httputil

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestDrainBody(t *testing.T) {
	body := io.NopCloser(strings.NewReader("hello world"))
	DrainBody(body)
}

func TestDrainBody_LargeContent(t *testing.T) {
	large := strings.Repeat("x", 8192)
	body := io.NopCloser(strings.NewReader(large))
	DrainBody(body)
}

func TestDrainBody_ErrorReader(t *testing.T) {
	errReader := io.NopCloser(&failReader{err: fmt.Errorf("simulated read error")})
	DrainBody(errReader)
}

type failReader struct{ err error }

func (r *failReader) Read([]byte) (int, error) { return 0, r.err }

func TestSanitizeErr(t *testing.T) {
	t.Run("strips query string from url.Error", func(t *testing.T) {
		ue := &url.Error{
			Op:  "Get",
			URL: "http://tautulli:8181/api/v2?apikey=secret123&cmd=get_history",
			Err: fmt.Errorf("connection refused"),
		}
		got := SanitizeErr(ue)
		s := got.Error()
		if strings.Contains(s, "secret123") {
			t.Errorf("SanitizeErr leaked API key: %s", s)
		}
		if !strings.Contains(s, "?<redacted>") {
			t.Errorf("expected redacted query string, got: %s", s)
		}
	})

	t.Run("passes through non-url.Error unchanged", func(t *testing.T) {
		err := fmt.Errorf("some other error")
		got := SanitizeErr(err)
		if got.Error() != "some other error" {
			t.Errorf("expected unchanged error, got: %s", got.Error())
		}
	})

	t.Run("handles url.Error without query string", func(t *testing.T) {
		ue := &url.Error{
			Op:  "Get",
			URL: "http://tautulli:8181/api/v2",
			Err: fmt.Errorf("connection refused"),
		}
		got := SanitizeErr(ue)
		s := got.Error()
		if strings.Contains(s, "<redacted>") {
			t.Errorf("should not redact URL without query string: %s", s)
		}
	})
}
