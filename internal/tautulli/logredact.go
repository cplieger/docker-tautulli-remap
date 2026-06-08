package tautulli

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

// redactedLogger returns a logger that scrubs the api key (raw and
// query-escaped) from log output, writing to stderr like the app's default
// logger. httpx logs the full request URL (which carries the ?apikey= query
// param) as a "url" attribute and the *StatusError (whose message embeds that
// URL) as an "error" attribute on its retry/slow-upstream paths, so without
// redaction the api key would leak into the app's logs.
func redactedLogger(apiKey string) *slog.Logger {
	return newRedactedLogger(os.Stderr, apiKey)
}

// newRedactedLogger is redactedLogger with an injectable writer for tests.
// Redaction runs in a TextHandler ReplaceAttr hook (rather than a custom
// slog.Handler) so it never implements slog.Handler.Handle, whose by-value
// slog.Record parameter is mandated by the interface but trips gocritic's
// hugeParam check.
func newRedactedLogger(w io.Writer, apiKey string) *slog.Logger {
	secrets := []string{apiKey, url.QueryEscape(apiKey)}
	scrub := func(s string) string {
		for _, sec := range secrets {
			if sec != "" {
				s = strings.ReplaceAll(s, sec, "REDACTED")
			}
		}
		return s
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		// ReplaceAttr scrubs string attrs (e.g. "url") and any-valued attrs
		// whose stringified form carries a secret (e.g. a logged error).
		// Non-secret any values are left untouched to preserve their type.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Value.Kind() {
			case slog.KindString:
				a.Value = slog.StringValue(scrub(a.Value.String()))
			case slog.KindAny:
				s := fmt.Sprint(a.Value.Any())
				if sc := scrub(s); sc != s {
					a.Value = slog.StringValue(sc)
				}
			}
			return a
		},
	}))
}
