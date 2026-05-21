package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config holds the application configuration.
type Config struct {
	TautulliURL       string
	TautulliAPIKey    string
	PlexURL           string
	PlexToken         string
	ScheduleHours     int
	DryRun            bool
	FallbackTitleYear bool
	FallbackTitleOnly bool
}

// Load parses environment variables and returns the configuration.
func Load() (*Config, error) {
	rawHours := getEnv("SCHEDULE_HOURS", "0")
	hours, err := strconv.Atoi(rawHours)
	if err != nil {
		slog.Warn("invalid SCHEDULE_HOURS, defaulting to one-shot mode",
			"value", rawHours, "error", err)
		hours = 0
	}

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
		ScheduleHours:     hours,
		DryRun:            GetEnvBool("DRY_RUN", true),
		FallbackTitleYear: GetEnvBool("FALLBACK_TITLE_YEAR", true),
		FallbackTitleOnly: GetEnvBool("FALLBACK_TITLE_ONLY", false),
	}, nil
}

// LogConfig logs the active configuration at startup.
func LogConfig(cfg *Config) {
	slog.Info("configuration loaded",
		"tautulli_url", cfg.TautulliURL,
		"plex_url", cfg.PlexURL,
		"dry_run", cfg.DryRun,
		"fallback_title_year", cfg.FallbackTitleYear,
		"fallback_title_only", cfg.FallbackTitleOnly,
		"schedule_hours", cfg.ScheduleHours,
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
