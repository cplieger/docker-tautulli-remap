// Package config loads and validates the application configuration from
// environment variables.
package config

import (
	"cmp"
	"log/slog"
	"strings"
	"time"

	"github.com/cplieger/envx/v2"
)

// DefaultMaxHistoryRecords is the default sanity cap on the Tautulli history
// size a run will process. Histories above the cap abort the run (a count
// that large usually means a filter regression, not a real library); genuinely
// larger histories raise it via MAX_HISTORY_RECORDS.
const DefaultMaxHistoryRecords = 500_000

// Config holds the application configuration.
type Config struct {
	TautulliURL       string
	TautulliAPIKey    string
	PlexURL           string
	PlexToken         string
	RemapInterval     time.Duration // 0 = resident-idle (external trigger)
	MaxHistoryRecords int
	DryRun            bool
	FallbackTitleYear bool
	FallbackTitleOnly bool
}

// RemapInterval returns the effective REMAP_INTERVAL (0 =
// resident-idle), parsed with the same rules Load applies. Exported
// separately so the health subcommand can derive its probe max-age
// before (and without) a full config load, which would fail on missing
// secrets the probe does not need.
func RemapInterval() time.Duration {
	return parseRemapInterval(cmp.Or(envx.String("REMAP_INTERVAL"), "off"))
}

// Load parses environment variables and returns the configuration.
func Load() (*Config, error) {
	interval := RemapInterval()

	apiKey, err := requireSecret("TAUTULLI_API_KEY")
	if err != nil {
		return nil, err
	}
	token, err := requireSecret("PLEX_TOKEN")
	if err != nil {
		return nil, err
	}

	return &Config{
		TautulliURL:       cmp.Or(envx.String("TAUTULLI_URL"), "http://tautulli:8181"),
		TautulliAPIKey:    apiKey,
		PlexURL:           cmp.Or(envx.String("PLEX_URL"), "http://plex:32400"),
		PlexToken:         token,
		RemapInterval:     interval,
		MaxHistoryRecords: maxHistoryRecords(),
		DryRun:            envx.Bool("DRY_RUN", true),
		FallbackTitleYear: envx.Bool("FALLBACK_TITLE_YEAR", true),
		FallbackTitleOnly: envx.Bool("FALLBACK_TITLE_ONLY", false),
	}, nil
}

// maxHistoryRecords reads MAX_HISTORY_RECORDS, falling back to the default on
// unset or malformed values (envx warns) and on non-positive values, which
// would make every run abort at the first history page.
func maxHistoryRecords() int {
	n := envx.Int("MAX_HISTORY_RECORDS", DefaultMaxHistoryRecords)
	if n <= 0 {
		slog.Warn("non-positive MAX_HISTORY_RECORDS, using default",
			"value", n, "default", DefaultMaxHistoryRecords)
		return DefaultMaxHistoryRecords
	}
	return n
}

// parseRemapInterval parses REMAP_INTERVAL. Accepts a Go duration
// (e.g. "24h", "6h30m") or the sentinels "off"/"disabled"/"0"/"0s" which
// all map to 0 (resident-idle mode). Unparseable values warn and default to off.
func parseRemapInterval(raw string) time.Duration {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "off", "disabled", "0", "0s":
		return 0
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		slog.Warn("invalid REMAP_INTERVAL, defaulting to off (resident-idle)",
			"value", raw, "error", err)
		return 0
	}
	if d < 0 {
		slog.Warn("negative REMAP_INTERVAL, defaulting to off", "value", raw)
		return 0
	}
	return d
}

// Log logs the active configuration at startup.
// It deliberately omits TautulliAPIKey and PlexToken: per the project's hard
// "API tokens never logged" contract, no secret may be added to this (or any)
// log call. Do not add cfg.TautulliAPIKey or cfg.PlexToken here.
func (cfg *Config) Log() {
	mode := "resident-idle"
	if cfg.RemapInterval > 0 {
		mode = cfg.RemapInterval.String()
	}
	slog.Info("configuration loaded",
		"tautulli_url", cfg.TautulliURL,
		"plex_url", cfg.PlexURL,
		"dry_run", cfg.DryRun,
		"fallback_title_year", cfg.FallbackTitleYear,
		"fallback_title_only", cfg.FallbackTitleOnly,
		"remap_interval", mode,
		"max_history_records", cfg.MaxHistoryRecords,
	)
}

// requireSecret reads a required secret env var via envx.Secret, so the value
// may arrive either in the variable itself or in the file named by its
// KEY_FILE companion (the Docker/Podman secret convention), which keeps it out
// of the container environment and so out of docker inspect. KEY_FILE wins when
// set. A missing secret yields *envx.MissingError. It adds the app's
// whitespace-only warning: such a value fails upstream authentication, so it is
// worth flagging while still returning it. The secret value itself is never
// logged.
func requireSecret(key envx.Key) (string, error) {
	v, err := envx.Secret(key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		slog.Warn("required secret is set but contains only whitespace; requests will fail authentication",
			"key", key)
	}
	return v, nil
}
