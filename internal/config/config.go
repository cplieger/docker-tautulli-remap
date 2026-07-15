// Package config loads and validates the application configuration from
// environment variables.
package config

import (
	"log/slog"
	"strings"
	"time"

	"github.com/cplieger/envx"
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
	interval := parseScheduleInterval(envx.String("SCHEDULE_INTERVAL", "off"))

	apiKey, err := requireSecret("TAUTULLI_APIKEY")
	if err != nil {
		return nil, err
	}
	token, err := requireSecret("PLEX_TOKEN")
	if err != nil {
		return nil, err
	}

	return &Config{
		TautulliURL:       envx.String("TAUTULLI_URL", "http://tautulli:8181"),
		TautulliAPIKey:    apiKey,
		PlexURL:           envx.String("PLEX_URL", "http://plex:32400"),
		PlexToken:         token,
		ScheduleInterval:  interval,
		DryRun:            envx.Bool("DRY_RUN", true),
		FallbackTitleYear: envx.Bool("FALLBACK_TITLE_YEAR", true),
		FallbackTitleOnly: envx.Bool("FALLBACK_TITLE_ONLY", false),
	}, nil
}

// parseScheduleInterval parses SCHEDULE_INTERVAL. Accepts a Go duration
// (e.g. "24h", "6h30m") or the sentinels "off"/"disabled"/"0"/"0s" which
// all map to 0 (resident-idle mode). Unparseable values warn and default to off.
func parseScheduleInterval(raw string) time.Duration {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "off", "disabled", "0", "0s":
		return 0
	}
	d, err := time.ParseDuration(trimmed)
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
// It deliberately omits TautulliAPIKey and PlexToken: per the project's hard
// "API tokens never logged" contract, no secret may be added to this (or any)
// log call. Do not add cfg.TautulliAPIKey or cfg.PlexToken here.
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

// requireSecret reads a required secret env var via envx.Require (unset or
// empty yields *envx.MissingError), adding the app's whitespace-only
// warning: such a value fails upstream authentication, so it is worth
// flagging while still returning it. The secret value itself is never
// logged.
func requireSecret(key string) (string, error) {
	v, err := envx.Require(key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		slog.Warn("required secret is set but contains only whitespace; requests will fail authentication",
			"key", key)
	}
	return v, nil
}
