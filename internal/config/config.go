// Package config loads and validates the application configuration from
// environment variables.
package config

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds the application configuration.
type Config struct {
	TautulliURL       string
	TautulliAPIKey    string
	PlexURL           string
	PlexToken         string
	ScheduleInterval  time.Duration // 0 = resident-idle (external trigger)
	DryRun            bool
	FallbackTitleYear bool
	FallbackTitleOnly bool
}

// Load parses environment variables and returns the configuration.
func Load() (*Config, error) {
	interval := parseScheduleInterval(getEnv("SCHEDULE_INTERVAL", "off"))

	apiKey := requireEnv("TAUTULLI_APIKEY")
	if apiKey == "" {
		return nil, &MissingEnvError{Key: "TAUTULLI_APIKEY"}
	}
	token := requireEnv("PLEX_TOKEN")
	if token == "" {
		return nil, &MissingEnvError{Key: "PLEX_TOKEN"}
	}

	return &Config{
		TautulliURL:       getEnv("TAUTULLI_URL", "http://tautulli:8181"),
		TautulliAPIKey:    apiKey,
		PlexURL:           getEnv("PLEX_URL", "http://plex:32400"),
		PlexToken:         token,
		ScheduleInterval:  interval,
		DryRun:            GetEnvBool("DRY_RUN", true),
		FallbackTitleYear: GetEnvBool("FALLBACK_TITLE_YEAR", true),
		FallbackTitleOnly: GetEnvBool("FALLBACK_TITLE_ONLY", false),
	}, nil
}

// parseScheduleInterval parses SCHEDULE_INTERVAL. Accepts a Go duration
// (e.g. "24h", "6h30m") or the sentinels "off"/"disabled"/"0"/"0s" which
// all map to 0 (resident-idle mode). Unparseable values warn and default to off.
func parseScheduleInterval(raw string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "off", "disabled", "0", "0s":
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid SCHEDULE_INTERVAL, defaulting to off (resident-idle)",
			"value", raw, "error", err)
		return 0
	}
	if d < 0 {
		slog.Warn("negative SCHEDULE_INTERVAL, defaulting to off", "value", raw)
		return 0
	}
	return d
}

// LogConfig logs the active configuration at startup.
func LogConfig(cfg *Config) {
	mode := "resident-idle"
	if cfg.ScheduleInterval > 0 {
		mode = cfg.ScheduleInterval.String()
	}
	slog.Info("configuration loaded",
		"tautulli_url", cfg.TautulliURL,
		"plex_url", cfg.PlexURL,
		"dry_run", cfg.DryRun,
		"fallback_title_year", cfg.FallbackTitleYear,
		"fallback_title_only", cfg.FallbackTitleOnly,
		"schedule_interval", mode,
	)
}

// MissingEnvError indicates a required environment variable is not set.
type MissingEnvError struct {
	Key string
}

func (e *MissingEnvError) Error() string {
	return "required environment variable is missing: " + e.Key
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetEnvBool parses a boolean env var with tolerant semantics.
func GetEnvBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		slog.Warn("unrecognized boolean value, using default",
			"key", key, "value", v, "default", defaultVal)
		return defaultVal
	}
}

func requireEnv(key string) string {
	return os.Getenv(key)
}
