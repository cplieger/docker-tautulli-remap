package config

import (
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
