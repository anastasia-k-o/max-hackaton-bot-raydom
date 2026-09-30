// Package config loads and validates process configuration from the
// environment.
//
// The whole point of the mock/real split lives here: exactly one place decides
// which adapters get wired up, and it refuses to start with a configuration
// that cannot work. Nothing downstream re-reads the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// AppEnv is the deployment environment.
type AppEnv string

const (
	// EnvDev enables the /dev inspection endpoints.
	EnvDev AppEnv = "dev"
	// EnvProd disables everything development-only.
	EnvProd AppEnv = "prod"
)

// MaxMode selects the MaxClient implementation.
type MaxMode string

const (
	// MaxModeMock records messages in memory and never leaves the process.
	MaxModeMock MaxMode = "mock"
	// MaxModeReal talks to the MAX Bot API.
	MaxModeReal MaxMode = "real"
)

// UpdatesMode selects how events arrive from MAX.
//
// Режимы взаимоисключающие по схеме MAX: GET /updates предназначен для бота,
// который НЕ подписан на webhook.
type UpdatesMode string

const (
	// UpdatesModeWebhook — MAX присылает события на наш публичный адрес.
	// Рабочий режим, требует HTTPS-адреса, доступного снаружи.
	UpdatesModeWebhook UpdatesMode = "webhook"
	// UpdatesModePolling — бот сам забирает события методом long polling.
	// Не требует входящего адреса, поэтому работает за NAT без туннеля.
	UpdatesModePolling UpdatesMode = "polling"
)

// MiniAppButton selects how the bot's "Открыть афишу" buttons open the mini
// app.
type MiniAppButton string

const (
	// MiniAppButtonLink opens the mini app URL in a browser. It works
	// without any setup, but the mini app then gets no initData and cannot
	// tell who the user is.
	MiniAppButtonLink MiniAppButton = "link"
	// MiniAppButtonOpenApp opens the mini app inside MAX, with initData and
	// the event id as the start parameter. Requires the mini app to be
	// registered for this bot in MAX; until it is, MAX may reject every
	// message carrying such a button.
	MiniAppButtonOpenApp MiniAppButton = "open_app"
)

// CoreMode selects the CoreGateway implementation.
type CoreMode string

const (
	// CoreModeStub records actions in memory and always succeeds.
	CoreModeStub CoreMode = "stub"
	// CoreModeHTTP calls the Core Backend over HTTP.
	CoreModeHTTP CoreMode = "http"
)

// Config is the fully validated configuration of one bot process.
type Config struct {
	AppEnv   AppEnv
	HTTPPort int
	LogLevel string

	MaxMode          MaxMode
	MaxBotToken      string
	MaxWebhookSecret string
	MaxBaseURL       string
	MaxCAFile        string
	MaxTimeout       time.Duration

	// MaxUpdatesMode — откуда берутся события: webhook или polling.
	MaxUpdatesMode UpdatesMode
	// MaxPollTimeout — секунды удержания соединения при опросе (0..90).
	MaxPollTimeout int
	// MaxPollLimit — максимум событий за один ответ (1..1000).
	MaxPollLimit int

	// MaxMiniAppButton — ссылка или open_app для кнопок мини-приложения.
	MaxMiniAppButton MiniAppButton

	CoreMode    CoreMode
	CoreBaseURL string
	CoreAPIKey  string
	CoreTimeout time.Duration

	InternalAPIKey string

	// IdempotencyTTL is how long a processed webhook id is remembered.
	IdempotencyTTL time.Duration
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
}

// IsDev reports whether development-only endpoints should be mounted.
func (c Config) IsDev() bool { return c.AppEnv == EnvDev }

// UseOpenAppButtons reports whether mini app buttons open the mini app inside
// MAX rather than a browser.
func (c Config) UseOpenAppButtons() bool { return c.MaxMiniAppButton == MiniAppButtonOpenApp }

// IsPolling reports whether events are fetched by long polling.
func (c Config) IsPolling() bool { return c.MaxUpdatesMode == UpdatesModePolling }

// WebhookEnabled reports whether POST /webhooks/max should be mounted.
//
// В режиме polling с настоящим MAX маршрут не нужен: события приходят не
// туда, а MAX_WEBHOOK_SECRET в этом режиме не требуется — значит эндпойнт
// остался бы открытым без проверки подписи. Открытый эндпойнт, принимающий
// «события от MAX», позволил бы кому угодно подтвердить чужую регистрацию.
// Поэтому в этом сочетании он просто не поднимается.
//
// В режиме mock маршрут поднимается всегда: именно через него Postman и
// smoke-тест подают боту события, и это единственный способ прогнать полный
// цикл без сети.
func (c Config) WebhookEnabled() bool {
	return !(c.MaxMode == MaxModeReal && c.IsPolling())
}

// Addr is the listen address for the HTTP server.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.HTTPPort) }

// ValidationError aggregates every configuration problem found, so a
// misconfigured deployment reports all of its mistakes in one go instead of
// one restart per typo.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Load reads configuration from the process environment and validates it.
//
// It returns a *ValidationError describing every problem. The caller is
// expected to print that message and exit non-zero: starting with a broken
// configuration and failing later, per message, is strictly worse.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:   AppEnv(getEnvDefault("APP_ENV", string(EnvDev))),
		LogLevel: getEnvDefault("LOG_LEVEL", "info"),

		MaxMode:          MaxMode(getEnvDefault("MAX_MODE", string(MaxModeMock))),
		MaxBotToken:      strings.TrimSpace(os.Getenv("MAX_BOT_TOKEN")),
		MaxWebhookSecret: strings.TrimSpace(os.Getenv("MAX_WEBHOOK_SECRET")),
		MaxBaseURL:       strings.TrimSpace(os.Getenv("MAX_BASE_URL")),
		MaxCAFile:        strings.TrimSpace(os.Getenv("MAX_CA_FILE")),

		MaxUpdatesMode: UpdatesMode(getEnvDefault("MAX_UPDATES_MODE", string(UpdatesModeWebhook))),

		MaxMiniAppButton: MiniAppButton(getEnvDefault("MAX_MINIAPP_BUTTON", string(MiniAppButtonLink))),

		CoreMode:    CoreMode(getEnvDefault("CORE_MODE", string(CoreModeStub))),
		CoreBaseURL: strings.TrimSpace(os.Getenv("CORE_BASE_URL")),
		CoreAPIKey:  strings.TrimSpace(os.Getenv("CORE_API_KEY")),

		InternalAPIKey: strings.TrimSpace(os.Getenv("INTERNAL_API_KEY")),
	}

	problems := make([]string, 0, 4)

	port, err := getEnvInt("HTTP_PORT", 8080)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.HTTPPort = port

	cfg.MaxTimeout, err = getEnvDuration("MAX_TIMEOUT", 10*time.Second)
	if err != nil {
		problems = append(problems, err.Error())
	}

	cfg.MaxPollTimeout, err = getEnvInt("MAX_POLL_TIMEOUT", 30)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.MaxPollLimit, err = getEnvInt("MAX_POLL_LIMIT", 100)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.CoreTimeout, err = getEnvDuration("CORE_TIMEOUT", 5*time.Second)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.IdempotencyTTL, err = getEnvDuration("IDEMPOTENCY_TTL", 30*time.Minute)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.ShutdownTimeout, err = getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		problems = append(problems, err.Error())
	}

	problems = append(problems, cfg.validate()...)

	if len(problems) > 0 {
		return Config{}, &ValidationError{Problems: problems}
	}
	return cfg, nil
}

// validate returns a list of human-readable configuration problems.
func (c Config) validate() []string {
	var problems []string

	switch c.AppEnv {
	case EnvDev, EnvProd:
	default:
		problems = append(problems, fmt.Sprintf("APP_ENV must be %q or %q, got %q", EnvDev, EnvProd, c.AppEnv))
	}

	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		problems = append(problems, fmt.Sprintf("HTTP_PORT must be 1..65535, got %d", c.HTTPPort))
	}

	switch c.MaxUpdatesMode {
	case UpdatesModeWebhook, UpdatesModePolling:
	default:
		problems = append(problems, fmt.Sprintf(
			"MAX_UPDATES_MODE must be %q or %q, got %q",
			UpdatesModeWebhook, UpdatesModePolling, c.MaxUpdatesMode))
	}

	// Границы из схемы MAX для GET /updates. Проверяются всегда, а не только
	// в режиме polling: молча исправленное значение прячет опечатку до того
	// момента, когда режим переключат.
	if c.MaxPollTimeout < 0 || c.MaxPollTimeout > 90 {
		problems = append(problems, fmt.Sprintf("MAX_POLL_TIMEOUT must be 0..90 seconds, got %d", c.MaxPollTimeout))
	}
	if c.MaxPollLimit < 1 || c.MaxPollLimit > 1000 {
		problems = append(problems, fmt.Sprintf("MAX_POLL_LIMIT must be 1..1000, got %d", c.MaxPollLimit))
	}

	switch c.MaxMiniAppButton {
	case MiniAppButtonLink, MiniAppButtonOpenApp:
	default:
		problems = append(problems, fmt.Sprintf(
			"MAX_MINIAPP_BUTTON must be %q or %q, got %q",
			MiniAppButtonLink, MiniAppButtonOpenApp, c.MaxMiniAppButton))
	}

	switch c.MaxMode {
	case MaxModeMock:
		// Mock mode must work with nothing configured at all: that is the
		// whole promise of the demo path.
	case MaxModeReal:
		if c.MaxBotToken == "" {
			problems = append(problems, "MAX_MODE=real requires MAX_BOT_TOKEN (get it from @MasterBot in MAX)")
		}
		// Секрет webhook нужен только тогда, когда webhook вообще
		// используется. В режиме polling входящего эндпойнта нет, требовать
		// для него секрет было бы требованием ради требования.
		if c.MaxWebhookSecret == "" && c.MaxUpdatesMode == UpdatesModeWebhook {
			problems = append(problems, "MAX_MODE=real with MAX_UPDATES_MODE=webhook requires MAX_WEBHOOK_SECRET "+
				"(the same value you pass to POST /subscriptions); "+
				"if you have no public address, set MAX_UPDATES_MODE=polling instead")
		}
	default:
		problems = append(problems, fmt.Sprintf("MAX_MODE must be %q or %q, got %q", MaxModeMock, MaxModeReal, c.MaxMode))
	}

	switch c.CoreMode {
	case CoreModeStub:
	case CoreModeHTTP:
		if c.CoreBaseURL == "" {
			problems = append(problems, "CORE_MODE=http requires CORE_BASE_URL")
		} else if !strings.HasPrefix(c.CoreBaseURL, "http://") && !strings.HasPrefix(c.CoreBaseURL, "https://") {
			problems = append(problems, "CORE_BASE_URL must start with http:// or https://")
		}
	default:
		problems = append(problems, fmt.Sprintf("CORE_MODE must be %q or %q, got %q", CoreModeStub, CoreModeHTTP, c.CoreMode))
	}

	// Несуществующий файл сертификата лучше заметить при старте, чем на
	// первом же уведомлении.
	if c.MaxCAFile != "" {
		if _, err := os.Stat(c.MaxCAFile); err != nil {
			problems = append(problems, fmt.Sprintf(
				"MAX_CA_FILE указывает на недоступный файл %q (%v)", c.MaxCAFile, err))
		}
	}

	if c.InternalAPIKey == "" {
		problems = append(problems, "INTERNAL_API_KEY is required (it protects POST /api/v1/notifications)")
	}

	if c.AppEnv == EnvProd && c.InternalAPIKey == "change-me" {
		problems = append(problems, "INTERNAL_API_KEY must not be the placeholder value in APP_ENV=prod")
	}

	return problems
}

// Redacted returns a copy safe to log: every secret is replaced by a marker
// that reveals only whether it was set.
//
// Secrets never reach the logs. This helper exists so that "log the config on
// boot" stays a safe reflex.
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"app_env":            string(c.AppEnv),
		"http_port":          c.HTTPPort,
		"log_level":          c.LogLevel,
		"max_mode":           string(c.MaxMode),
		"max_updates_mode":   string(c.MaxUpdatesMode),
		"max_poll_timeout":   c.MaxPollTimeout,
		"max_poll_limit":     c.MaxPollLimit,
		"max_miniapp_button": string(c.MaxMiniAppButton),
		"max_base_url":       c.MaxBaseURL,
		"max_ca_file":        c.MaxCAFile,
		"max_token":          redactedMarker(c.MaxBotToken),
		"max_webhook_secret": redactedMarker(c.MaxWebhookSecret),
		"core_mode":          string(c.CoreMode),
		"core_base_url":      c.CoreBaseURL,
		"core_api_key":       redactedMarker(c.CoreAPIKey),
		"internal_api_key":   redactedMarker(c.InternalAPIKey),
		"idempotency_ttl":    c.IdempotencyTTL.String(),
	}
}

func redactedMarker(secret string) string {
	if secret == "" {
		return "<empty>"
	}
	return "<set>"
}

func getEnvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return value, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a duration such as 5s or 1m, got %q", key, raw)
	}
	if value <= 0 {
		return fallback, fmt.Errorf("%s must be positive, got %q", key, raw)
	}
	return value, nil
}

// AsValidationError extracts a *ValidationError from err, if present.
func AsValidationError(err error) (*ValidationError, bool) {
	var target *ValidationError
	ok := errors.As(err, &target)
	return target, ok
}
