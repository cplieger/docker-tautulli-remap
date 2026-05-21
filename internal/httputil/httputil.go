package httputil

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

const maxDrainBody = 1 << 20

// DrainBody reads and discards the response body (up to maxDrainBody) so
// that net/http can reuse the keep-alive connection.
func DrainBody(body io.ReadCloser) {
	if _, err := io.Copy(io.Discard, io.LimitReader(body, maxDrainBody)); err != nil && !errors.Is(err, io.EOF) {
		slog.Debug("failed to drain response body", "error", err)
	}
}

// SleepCtx sleeps for d or until ctx is cancelled.
func SleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}

// SanitizeErr strips the query string from *url.Error messages to prevent
// API keys from leaking into log output.
func SanitizeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if i := strings.Index(ue.URL, "?"); i >= 0 {
			ue.URL = ue.URL[:i] + "?<redacted>"
		}
	}
	return err
}
