// Package config reads the backend's settings from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/notify"
)

// Config is everything the backend needs to start.
type Config struct {
	HTTPAddr string
	DBPath   string

	// DevMode turns on /dev/* (dev login, clock travel, demo participants).
	// Never on a server reachable by strangers: it lets anyone act as anyone.
	DevMode bool

	// BotAPIKey is what the bot sends in X-Api-Key when a user presses a
	// button. Equal to CORE_API_KEY in the bot's .env.
	BotAPIKey string

	Notify notify.Config

	MiniAppURL string

	TimingPreset      string
	Timing            domain.Timing
	SchedulerInterval time.Duration

	SeedDemo bool

	// MaxBotToken checks the mini app's initData (POST /auth/max). The same
	// token the bot uses. InitDataMaxAge rejects stale initData.
	MaxBotToken    string
	InitDataMaxAge time.Duration

	// DemoMaxUserID: with DEV_MODE, the mini app opened in a browser signs
	// in as this MAX account (see service.AuthConfig.DevMaxUserID).
	DemoMaxUserID int64
}

// Load reads and validates the configuration.
func Load() (Config, error) {
	var errs []error
	c := Config{
		HTTPAddr: env("HTTP_ADDR", ":8090"),
		DBPath:   env("DB_PATH", "data/core.db"),
		// Each secret has one name in the shared .env at the repository
		// root; the BOT_* names are kept for a backend-only .env.
		BotAPIKey:  firstEnv("BOT_API_KEY", "CORE_API_KEY"),
		MiniAppURL: strings.TrimSpace(os.Getenv("MINI_APP_URL")),
		Notify: notify.Config{
			Mode:           notify.Mode(env("NOTIFY_MODE", string(notify.ModeHTTP))),
			BotBaseURL:     env("BOT_BASE_URL", "http://localhost:8080"),
			InternalAPIKey: firstEnv("BOT_INTERNAL_API_KEY", "INTERNAL_API_KEY"),
		},
		TimingPreset: env("TIMING_PRESET", "real"),
	}
	c.DevMode = boolEnv("DEV_MODE", false, &errs)
	c.SeedDemo = boolEnv("SEED_DEMO", true, &errs)
	c.MaxBotToken = strings.TrimSpace(os.Getenv("MAX_BOT_TOKEN"))
	c.InitDataMaxAge = durationEnv("INITDATA_MAX_AGE", 24*time.Hour, &errs)
	if raw := strings.TrimSpace(os.Getenv("DEMO_MAX_USER_ID")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			errs = append(errs, fmt.Errorf("DEMO_MAX_USER_ID must be your numeric MAX id, got %q", raw))
		}
		c.DemoMaxUserID = id
	}
	c.Notify.Timeout = durationEnv("BOT_TIMEOUT", 10*time.Second, &errs)

	switch c.TimingPreset {
	case "real":
		c.Timing = domain.RealTiming()
	case "fast":
		c.Timing = domain.FastTiming()
	default:
		errs = append(errs, fmt.Errorf("TIMING_PRESET must be real or fast, got %q", c.TimingPreset))
		c.Timing = domain.RealTiming()
	}
	// Individual overrides on top of the preset.
	c.Timing.Reminder24h = durationEnv("REMINDER_24H_BEFORE", c.Timing.Reminder24h, &errs)
	c.Timing.Confirmation = durationEnv("CONFIRMATION_BEFORE", c.Timing.Confirmation, &errs)
	c.Timing.ConfirmationRetry = durationEnv("CONFIRMATION_RETRY_BEFORE", c.Timing.ConfirmationRetry, &errs)
	c.Timing.Reminder1h = durationEnv("REMINDER_1H_BEFORE", c.Timing.Reminder1h, &errs)
	c.Timing.RegistrationCloses = durationEnv("REGISTRATION_CLOSES_BEFORE", c.Timing.RegistrationCloses, &errs)
	c.Timing.OfferTTL = durationEnv("OFFER_TTL", c.Timing.OfferTTL, &errs)
	c.Timing.OfferTTLSoon = durationEnv("OFFER_TTL_SOON", c.Timing.OfferTTLSoon, &errs)
	if err := c.Timing.Validate(); err != nil {
		errs = append(errs, err)
	}
	defaultInterval := 15 * time.Second
	if c.TimingPreset == "fast" {
		defaultInterval = 5 * time.Second
	}
	c.SchedulerInterval = durationEnv("SCHEDULER_INTERVAL", defaultInterval, &errs)

	switch c.Notify.Mode {
	case notify.ModeHTTP:
		if c.Notify.BotBaseURL == "" {
			errs = append(errs, errors.New("BOT_BASE_URL is required with NOTIFY_MODE=http (or set NOTIFY_MODE=log to run without the bot)"))
		}
		if c.Notify.InternalAPIKey == "" {
			errs = append(errs, errors.New("INTERNAL_API_KEY is required with NOTIFY_MODE=http: the bot accepts notifications only with it"))
		}
	case notify.ModeLog:
	default:
		errs = append(errs, fmt.Errorf("NOTIFY_MODE must be http or log, got %q", c.Notify.Mode))
	}
	if c.BotAPIKey == "" && !c.DevMode {
		errs = append(errs, errors.New("CORE_API_KEY is required outside DEV_MODE: without it the bot's button presses are refused"))
	}
	if c.MiniAppURL != "" && !strings.HasPrefix(c.MiniAppURL, "http://") && !strings.HasPrefix(c.MiniAppURL, "https://") {
		errs = append(errs, fmt.Errorf("MINI_APP_URL must be an absolute http(s) URL, got %q", c.MiniAppURL))
	}
	return c, errors.Join(errs...)
}

// Redacted is the configuration safe to log: secrets reduced to set/unset.
func (c Config) Redacted() []any {
	set := func(s string) string {
		if s == "" {
			return "unset"
		}
		return "set"
	}
	return []any{
		"http_addr", c.HTTPAddr, "db_path", c.DBPath, "dev_mode", c.DevMode,
		"notify_mode", c.Notify.Mode, "bot_base_url", c.Notify.BotBaseURL,
		"bot_internal_api_key", set(c.Notify.InternalAPIKey), "bot_api_key", set(c.BotAPIKey),
		"mini_app_url", c.MiniAppURL, "timing_preset", c.TimingPreset,
		"scheduler_interval", c.SchedulerInterval.String(), "seed_demo", c.SeedDemo,
		"max_bot_token", set(c.MaxBotToken), "initdata_max_age", c.InitDataMaxAge.String(),
		"demo_max_user_id", c.DemoMaxUserID,
	}
}

// firstEnv returns the first non-empty variable among keys.
func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func boolEnv(key string, def bool, errs *[]error) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be true or false, got %q", key, raw))
		return def
	}
	return v
}

func durationEnv(key string, def time.Duration, errs *[]error) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive duration like 30s, 5m, 24h; got %q", key, raw))
		return def
	}
	return v
}
