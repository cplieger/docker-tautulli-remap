package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// gk_tautulli_remap_u1_captureLogs swaps the default slog logger for one that
// records all output into a buffer for the duration of the (sub)test, restoring
// the previous default on cleanup. parseScheduleInterval logs via the package
// default logger, so this lets us assert on its warning side-effects.
// The returned closure yields the buffer's current contents.
func gk_tautulli_remap_u1_captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf.String
}

// TestParseScheduleInterval_negativeBoundary_gk_tautulli_remap_u1 pins
// config.go:61 `if d < 0` against a CONDITIONALS_BOUNDARY mutation to
// `if d <= 0`. The two comparisons differ only at d == 0.
//
// A zero-valued-but-non-sentinel duration (e.g. "0h") bypasses the switch
// sentinels ("", "off", "disabled", "0", "0s"), parses to 0 with no error, and
// reaches line 61. Under the original `d < 0` it is treated as a valid
// resident-idle value: returns 0 with NO "negative" warning. Under the mutant
// `d <= 0` it would wrongly emit the "negative SCHEDULE_INTERVAL" warning.
// The wantNegWarning expectation for the zero cases therefore flips under the
// mutation, killing it. A genuinely negative duration must warn under both.
func TestParseScheduleInterval_negativeBoundary_gk_tautulli_remap_u1(t *testing.T) {
	tests := []struct {
		name           string
		raw            string
		wantDur        time.Duration
		wantNegWarning bool
	}{
		// Boundary killers: d == 0 reached via a parseable non-sentinel form.
		{name: "zero hours is valid, no negative warning", raw: "0h", wantDur: 0, wantNegWarning: false},
		{name: "zero ms is valid, no negative warning", raw: "0ms", wantDur: 0, wantNegWarning: false},
		{name: "compound zero is valid, no negative warning", raw: "0m0s", wantDur: 0, wantNegWarning: false},
		// True branch (d < 0): warns and defaults to off under both original and mutant.
		{name: "negative duration warns and defaults to off", raw: "-5h", wantDur: 0, wantNegWarning: true},
		// Positive: false branch under both; passes through.
		{name: "positive duration passes through", raw: "2h", wantDur: 2 * time.Hour, wantNegWarning: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getLogs := gk_tautulli_remap_u1_captureLogs(t)

			got := parseScheduleInterval(tt.raw)

			if got != tt.wantDur {
				t.Errorf("parseScheduleInterval(%q) = %v, want %v", tt.raw, got, tt.wantDur)
			}
			gotNeg := strings.Contains(getLogs(), "negative SCHEDULE_INTERVAL")
			if gotNeg != tt.wantNegWarning {
				t.Errorf("parseScheduleInterval(%q) negative-warning-logged = %v, want %v",
					tt.raw, gotNeg, tt.wantNegWarning)
			}
		})
	}
}
