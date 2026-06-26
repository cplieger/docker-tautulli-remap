package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("TAUTULLI_URL", "http://localhost:8181")
	t.Setenv("TAUTULLI_APIKEY", "test-key")
	t.Setenv("PLEX_URL", "http://localhost:32400")
	t.Setenv("PLEX_TOKEN", "test-token")
	t.Setenv("DRY_RUN", "false")
	t.Setenv("FALLBACK_TITLE_YEAR", "true")
	t.Setenv("FALLBACK_TITLE_ONLY", "true")
	t.Setenv("SCHEDULE_INTERVAL", "12h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.TautulliURL != "http://localhost:8181" {
		t.Errorf("TautulliURL = %q", cfg.TautulliURL)
	}
	if cfg.DryRun {
		t.Error("DryRun should be false")
	}
	if !cfg.FallbackTitleYear {
		t.Error("FallbackTitleYear should be true")
	}
	if !cfg.FallbackTitleOnly {
		t.Error("FallbackTitleOnly should be true")
	}
	if cfg.ScheduleInterval != 12*time.Hour {
		t.Errorf("ScheduleInterval = %v, want 12h", cfg.ScheduleInterval)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("TAUTULLI_APIKEY", "key")
	t.Setenv("PLEX_TOKEN", "token")
	t.Setenv("DRY_RUN", "")
	t.Setenv("SCHEDULE_INTERVAL", "")
	t.Setenv("FALLBACK_TITLE_YEAR", "")
	t.Setenv("FALLBACK_TITLE_ONLY", "")
	t.Setenv("TAUTULLI_URL", "")
	t.Setenv("PLEX_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.TautulliURL != "http://tautulli:8181" {
		t.Errorf("TautulliURL = %q, want default", cfg.TautulliURL)
	}
	if cfg.PlexURL != "http://plex:32400" {
		t.Errorf("PlexURL = %q, want default", cfg.PlexURL)
	}
	if !cfg.DryRun {
		t.Error("DryRun should default to true")
	}
	if !cfg.FallbackTitleYear {
		t.Error("FallbackTitleYear should default to true")
	}
	if cfg.FallbackTitleOnly {
		t.Error("FallbackTitleOnly should default to false")
	}
}

func TestLoadMissingAPIKey(t *testing.T) {
	t.Setenv("TAUTULLI_APIKEY", "")
	t.Setenv("PLEX_TOKEN", "token")

	_, err := Load()
	if err == nil {
		t.Error("expected error for missing TAUTULLI_APIKEY")
	}
}

func TestLoadMissingPlexToken(t *testing.T) {
	t.Setenv("TAUTULLI_APIKEY", "key")
	t.Setenv("PLEX_TOKEN", "")

	_, err := Load()
	if err == nil {
		t.Error("expected error for missing PLEX_TOKEN")
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		value      string
		defaultVal bool
		want       bool
	}{
		{"true", false, true},
		{"1", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"false", true, false},
		{"0", true, false},
		{"no", true, false},
		{"off", true, false},
		{"", true, true},
		{"", false, false},
		{"maybe", true, true},
		{"maybe", false, false},
	}
	for _, tt := range tests {
		t.Setenv("TEST_BOOL", tt.value)
		got := GetEnvBool("TEST_BOOL", tt.defaultVal)
		if got != tt.want {
			t.Errorf("GetEnvBool(%q, %v) = %v, want %v", tt.value, tt.defaultVal, got, tt.want)
		}
	}
}

func TestLoadInvalidScheduleInterval(t *testing.T) {
	t.Setenv("TAUTULLI_APIKEY", "key")
	t.Setenv("PLEX_TOKEN", "token")
	t.Setenv("SCHEDULE_INTERVAL", "notaduration")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ScheduleInterval != 0 {
		t.Errorf("ScheduleInterval = %v, want 0", cfg.ScheduleInterval)
	}
}

// captureLogs redirects the default slog logger into a buffer for the duration
// of the test, restoring the previous default on cleanup. parseScheduleInterval
// emits its warnings through the package default logger, so this lets a test
// assert on those side-effects. The returned closure yields the buffer's
// current contents.
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf.String
}

// TestParseScheduleInterval covers the values parseScheduleInterval accepts
// silently: the sentinels that select resident-idle mode (0), parseable
// positive durations that pass through unchanged, and parseable zero-valued
// durations. None of these may emit a SCHEDULE_INTERVAL warning. Asserting the
// absence of a warning is what pins the sentinel switch: drop "off" from it and
// "off" falls through to time.ParseDuration, which fails and warns.
func TestParseScheduleInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"empty is resident-idle", "", 0},
		{"off sentinel", "off", 0},
		{"disabled sentinel", "disabled", 0},
		{"zero sentinel", "0", 0},
		{"zero-seconds sentinel", "0s", 0},
		{"uppercase OFF with surrounding spaces", "  OFF  ", 0},
		{"mixed-case Disabled", "Disabled", 0},
		{"whitespace only normalizes to empty", "   ", 0},
		{"positive duration passes through", "24h", 24 * time.Hour},
		{"compound duration passes through", "6h30m", 6*time.Hour + 30*time.Minute},
		{"parseable zero hours accepted silently", "0h", 0},
		{"parseable zero ms accepted silently", "0ms", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getLogs := captureLogs(t)
			got := parseScheduleInterval(tt.raw)
			if got != tt.want {
				t.Errorf("parseScheduleInterval(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			if logs := getLogs(); strings.Contains(logs, "SCHEDULE_INTERVAL") {
				t.Errorf("parseScheduleInterval(%q) emitted an unexpected warning: %q", tt.raw, logs)
			}
		})
	}
}

// TestParseScheduleInterval_rejectsInvalid covers the values parseScheduleInterval
// rejects: unparseable input and negative durations both default to off (0) and
// must warn. The negative case is the boundary partner of the silent "0h" case
// above: it pins `d < 0` against a `d <= 0` mutation, which would wrongly warn
// on and reject a zero-valued duration. The specific warning phrase
// distinguishes the two rejection reasons.
func TestParseScheduleInterval_rejectsInvalid(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantWarning string
	}{
		{"unparseable value", "notaduration", "invalid SCHEDULE_INTERVAL"},
		{"garbage with digits", "12parsecs", "invalid SCHEDULE_INTERVAL"},
		{"negative hours", "-5h", "negative SCHEDULE_INTERVAL"},
		{"negative compound", "-1h30m", "negative SCHEDULE_INTERVAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getLogs := captureLogs(t)
			got := parseScheduleInterval(tt.raw)
			if got != 0 {
				t.Errorf("parseScheduleInterval(%q) = %v, want 0 (off)", tt.raw, got)
			}
			if logs := getLogs(); !strings.Contains(logs, tt.wantWarning) {
				t.Errorf("parseScheduleInterval(%q) missing %q warning; logs=%q", tt.raw, tt.wantWarning, logs)
			}
		})
	}
}
