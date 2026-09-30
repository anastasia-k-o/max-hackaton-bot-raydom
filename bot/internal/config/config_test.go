package config

import (
	"strings"
	"testing"
	"time"
)

// setEnv applies a set of variables for one test, clearing everything the
// loader reads first so tests cannot leak into each other.
func setEnv(t *testing.T, values map[string]string) {
	t.Helper()

	all := []string{
		"APP_ENV", "HTTP_PORT", "LOG_LEVEL",
		"MAX_MODE", "MAX_BOT_TOKEN", "MAX_WEBHOOK_SECRET", "MAX_BASE_URL", "MAX_TIMEOUT",
		"MAX_CA_FILE", "MAX_UPDATES_MODE", "MAX_POLL_TIMEOUT", "MAX_POLL_LIMIT", "MAX_MINIAPP_BUTTON",
		"CORE_MODE", "CORE_BASE_URL", "CORE_API_KEY", "CORE_TIMEOUT",
		"INTERNAL_API_KEY", "IDEMPOTENCY_TTL", "SHUTDOWN_TIMEOUT",
	}
	for _, key := range all {
		t.Setenv(key, "")
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

// TestMockModeNeedsNoToken is the acceptance criterion "отсутствие token не
// мешает mock mode".
func TestMockModeNeedsNoToken(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"CORE_MODE":        "stub",
		"INTERNAL_API_KEY": "dev-key",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("mock mode must load with no MAX credentials: %v", err)
	}
	if cfg.MaxMode != MaxModeMock || cfg.CoreMode != CoreModeStub {
		t.Errorf("modes = %s/%s", cfg.MaxMode, cfg.CoreMode)
	}
	if !cfg.IsDev() {
		t.Error("APP_ENV should default to dev")
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTP_PORT default = %d, want 8080", cfg.HTTPPort)
	}
}

// TestRealModeRequiresCredentials is the criterion "отсутствие token вызывает
// понятную ошибку в real mode".
func TestRealModeRequiresCredentials(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantMention string
	}{
		{
			name: "missing token",
			env: map[string]string{
				"MAX_MODE":           "real",
				"MAX_WEBHOOK_SECRET": "s3cret",
				"INTERNAL_API_KEY":   "dev-key",
			},
			wantMention: "MAX_BOT_TOKEN",
		},
		{
			name: "missing webhook secret",
			env: map[string]string{
				"MAX_MODE":         "real",
				"MAX_BOT_TOKEN":    "token",
				"INTERNAL_API_KEY": "dev-key",
			},
			wantMention: "MAX_WEBHOOK_SECRET",
		},
		{
			name: "missing both",
			env: map[string]string{
				"MAX_MODE":         "real",
				"INTERNAL_API_KEY": "dev-key",
			},
			wantMention: "MAX_BOT_TOKEN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)

			_, err := Load()
			if err == nil {
				t.Fatal("real mode must refuse to start without credentials")
			}

			validationErr, ok := AsValidationError(err)
			if !ok {
				t.Fatalf("error is %T, want *ValidationError", err)
			}
			if !strings.Contains(validationErr.Error(), tc.wantMention) {
				t.Errorf("the message should name %s, got: %s", tc.wantMention, validationErr)
			}
			// The message must be actionable, not just "invalid".
			if !strings.Contains(validationErr.Error(), "requires") {
				t.Errorf("the message should explain what is required, got: %s", validationErr)
			}
		})
	}
}

func TestInternalAPIKeyIsMandatory(t *testing.T) {
	setEnv(t, map[string]string{"MAX_MODE": "mock", "CORE_MODE": "stub"})

	_, err := Load()
	if err == nil {
		t.Fatal("an unprotected notifications endpoint must not be possible")
	}
	if !strings.Contains(err.Error(), "INTERNAL_API_KEY") {
		t.Errorf("message = %v", err)
	}
}

func TestProductionRejectsThePlaceholderKey(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":          "prod",
		"MAX_MODE":         "mock",
		"CORE_MODE":        "stub",
		"INTERNAL_API_KEY": "change-me",
	})

	if _, err := Load(); err == nil {
		t.Fatal("the .env.example placeholder must not reach production")
	}
}

func TestHTTPCoreModeRequiresABaseURL(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"CORE_MODE":        "http",
		"INTERNAL_API_KEY": "dev-key",
	})

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "CORE_BASE_URL") {
		t.Fatalf("error = %v, want it to name CORE_BASE_URL", err)
	}
}

func TestHTTPCoreModeRejectsARelativeBaseURL(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"CORE_MODE":        "http",
		"CORE_BASE_URL":    "api.example.ru/v1",
		"INTERNAL_API_KEY": "dev-key",
	})

	if _, err := Load(); err == nil {
		t.Fatal("a scheme-less base url should be rejected")
	}
}

func TestUnknownModesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"bad app env", map[string]string{"APP_ENV": "staging"}},
		{"bad max mode", map[string]string{"MAX_MODE": "semi-real"}},
		{"bad core mode", map[string]string{"CORE_MODE": "grpc"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"INTERNAL_API_KEY": "dev-key"}
			for key, value := range tc.env {
				env[key] = value
			}
			setEnv(t, env)

			if _, err := Load(); err == nil {
				t.Fatal("an unknown mode should be rejected rather than silently defaulted")
			}
		})
	}
}

// TestAllProblemsAreReportedAtOnce: one restart per typo is a poor experience.
func TestAllProblemsAreReportedAtOnce(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":   "staging",
		"MAX_MODE":  "real",
		"CORE_MODE": "http",
		"HTTP_PORT": "not-a-number",
	})

	_, err := Load()
	validationErr, ok := AsValidationError(err)
	if !ok {
		t.Fatalf("error is %T, want *ValidationError", err)
	}
	if len(validationErr.Problems) < 5 {
		t.Errorf("expected every problem at once, got %d:\n%s", len(validationErr.Problems), validationErr)
	}
}

func TestDurationParsing(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"CORE_MODE":        "stub",
		"INTERNAL_API_KEY": "dev-key",
		"MAX_TIMEOUT":      "3s",
		"CORE_TIMEOUT":     "1500ms",
		"IDEMPOTENCY_TTL":  "5m",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.MaxTimeout != 3*time.Second {
		t.Errorf("MaxTimeout = %v", cfg.MaxTimeout)
	}
	if cfg.CoreTimeout != 1500*time.Millisecond {
		t.Errorf("CoreTimeout = %v", cfg.CoreTimeout)
	}
	if cfg.IdempotencyTTL != 5*time.Minute {
		t.Errorf("IdempotencyTTL = %v", cfg.IdempotencyTTL)
	}
}

func TestInvalidDurationsAreRejected(t *testing.T) {
	for _, value := range []string{"soon", "-5s", "0"} {
		t.Run(value, func(t *testing.T) {
			setEnv(t, map[string]string{
				"MAX_MODE":         "mock",
				"CORE_MODE":        "stub",
				"INTERNAL_API_KEY": "dev-key",
				"MAX_TIMEOUT":      value,
			})
			if _, err := Load(); err == nil {
				t.Fatalf("MAX_TIMEOUT=%q should be rejected", value)
			}
		})
	}
}

func TestInvalidPortsAreRejected(t *testing.T) {
	for _, value := range []string{"0", "70000", "-1"} {
		t.Run(value, func(t *testing.T) {
			setEnv(t, map[string]string{
				"MAX_MODE":         "mock",
				"CORE_MODE":        "stub",
				"INTERNAL_API_KEY": "dev-key",
				"HTTP_PORT":        value,
			})
			if _, err := Load(); err == nil {
				t.Fatalf("HTTP_PORT=%q should be rejected", value)
			}
		})
	}
}

// TestRedactedNeverLeaksSecrets is the "никогда не логируй token/secret/key"
// requirement made executable.
func TestRedactedNeverLeaksSecrets(t *testing.T) {
	cfg := Config{
		AppEnv:           EnvDev,
		MaxBotToken:      "super-secret-max-token",
		MaxWebhookSecret: "super-secret-webhook",
		CoreAPIKey:       "super-secret-core",
		InternalAPIKey:   "super-secret-internal",
	}

	redacted := cfg.Redacted()

	secrets := []string{
		"super-secret-max-token",
		"super-secret-webhook",
		"super-secret-core",
		"super-secret-internal",
	}
	for key, value := range redacted {
		text, ok := value.(string)
		if !ok {
			continue
		}
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("Redacted()[%q] leaks a secret: %q", key, text)
			}
		}
	}

	// It must still say whether a value was configured at all.
	if redacted["max_token"] != "<set>" {
		t.Errorf("max_token = %v, want <set>", redacted["max_token"])
	}

	empty := Config{}.Redacted()
	if empty["max_token"] != "<empty>" {
		t.Errorf("max_token = %v, want <empty>", empty["max_token"])
	}
}

func TestAddr(t *testing.T) {
	if got := (Config{HTTPPort: 9000}).Addr(); got != ":9000" {
		t.Errorf("Addr() = %q", got)
	}
}

// --- режим получения событий ---------------------------------------------

// TestUpdatesModeDefaultsToWebhook — смена значения по умолчанию сломала бы
// работающие развёртывания, поэтому она зафиксирована тестом.
func TestUpdatesModeDefaultsToWebhook(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"INTERNAL_API_KEY": "dev-key",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.MaxUpdatesMode != UpdatesModeWebhook {
		t.Errorf("MAX_UPDATES_MODE по умолчанию = %q, ожидался %q", cfg.MaxUpdatesMode, UpdatesModeWebhook)
	}
	if cfg.IsPolling() {
		t.Error("IsPolling() истинно при режиме по умолчанию")
	}
	if cfg.MaxPollTimeout != 30 || cfg.MaxPollLimit != 100 {
		t.Errorf("значения опроса по умолчанию = %d/%d, ожидались 30/100", cfg.MaxPollTimeout, cfg.MaxPollLimit)
	}
}

// TestPollingModeNeedsNoWebhookSecret.
//
// Это и есть причина, по которой режим polling существует: боту не нужен ни
// публичный адрес, ни секрет для входящего эндпойнта, которого нет.
func TestPollingModeNeedsNoWebhookSecret(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "real",
		"MAX_BOT_TOKEN":    "token-from-organizers",
		"MAX_UPDATES_MODE": "polling",
		"INTERNAL_API_KEY": "dev-key",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("режим polling должен загружаться без MAX_WEBHOOK_SECRET: %v", err)
	}
	if !cfg.IsPolling() {
		t.Error("IsPolling() ложно при MAX_UPDATES_MODE=polling")
	}
}

// TestRealWebhookModeStillRequiresASecret — послабление выше не должно
// распространяться на режим webhook: там открытый эндпойнт принимал бы
// «события от MAX» от кого угодно.
func TestRealWebhookModeStillRequiresASecret(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "real",
		"MAX_BOT_TOKEN":    "token-from-organizers",
		"MAX_UPDATES_MODE": "webhook",
		"INTERNAL_API_KEY": "dev-key",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("real + webhook без секрета загрузился")
	}
	if !strings.Contains(err.Error(), "MAX_WEBHOOK_SECRET") {
		t.Errorf("сообщение не называет недостающую переменную: %v", err)
	}
	// Сообщение должно подсказывать выход, а не только констатировать проблему.
	if !strings.Contains(err.Error(), "polling") {
		t.Errorf("сообщение не упоминает polling как выход при отсутствии публичного адреса: %v", err)
	}
}

// TestWebhookRouteIsNotMountedInRealPolling.
//
// В этом сочетании секрет не требуется, значит проверять подпись было бы
// нечем, и открытый эндпойнт позволил бы кому угодно подтвердить чужую
// регистрацию. Маршрут не поднимается вовсе.
func TestWebhookRouteIsNotMountedInRealPolling(t *testing.T) {
	tests := []struct {
		name    string
		max     MaxMode
		updates UpdatesMode
		want    bool
	}{
		{"mock + webhook", MaxModeMock, UpdatesModeWebhook, true},
		{"mock + polling (нужен Postman)", MaxModeMock, UpdatesModePolling, true},
		{"real + webhook", MaxModeReal, UpdatesModeWebhook, true},
		{"real + polling", MaxModeReal, UpdatesModePolling, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{MaxMode: tc.max, MaxUpdatesMode: tc.updates}
			if got := cfg.WebhookEnabled(); got != tc.want {
				t.Errorf("WebhookEnabled() = %v, ожидалось %v", got, tc.want)
			}
		})
	}
}

func TestUnknownUpdatesModeIsRejected(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"MAX_UPDATES_MODE": "long-polling",
		"INTERNAL_API_KEY": "dev-key",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("неизвестный MAX_UPDATES_MODE принят")
	}
	if !strings.Contains(err.Error(), "MAX_UPDATES_MODE") {
		t.Errorf("сообщение не называет переменную: %v", err)
	}
}

// TestPollingBoundsFollowTheMaxSchema — границы взяты из schema.yaml для
// GET /updates. Значение за границей отвергается, а не исправляется молча:
// тихая правка прячет опечатку до момента переключения режима.
func TestPollingBoundsFollowTheMaxSchema(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		mention string
	}{
		{"timeout больше 90", map[string]string{"MAX_POLL_TIMEOUT": "120"}, "MAX_POLL_TIMEOUT"},
		{"timeout отрицательный", map[string]string{"MAX_POLL_TIMEOUT": "-1"}, "MAX_POLL_TIMEOUT"},
		{"limit больше 1000", map[string]string{"MAX_POLL_LIMIT": "5000"}, "MAX_POLL_LIMIT"},
		{"limit нулевой", map[string]string{"MAX_POLL_LIMIT": "0"}, "MAX_POLL_LIMIT"},
		{"limit не число", map[string]string{"MAX_POLL_LIMIT": "сто"}, "MAX_POLL_LIMIT"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"MAX_MODE": "mock", "INTERNAL_API_KEY": "dev-key"}
			for key, value := range tc.env {
				env[key] = value
			}
			setEnv(t, env)

			_, err := Load()
			if err == nil {
				t.Fatalf("значение за границей схемы принято: %v", tc.env)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("сообщение не называет переменную %s: %v", tc.mention, err)
			}
		})
	}
}

// TestPollingBoundsAcceptTheEdges — сами границы допустимы.
func TestPollingBoundsAcceptTheEdges(t *testing.T) {
	setEnv(t, map[string]string{
		"MAX_MODE":         "mock",
		"INTERNAL_API_KEY": "dev-key",
		"MAX_POLL_TIMEOUT": "90",
		"MAX_POLL_LIMIT":   "1000",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("границы схемы отвергнуты: %v", err)
	}
	if cfg.MaxPollTimeout != 90 || cfg.MaxPollLimit != 1000 {
		t.Errorf("значения = %d/%d, ожидались 90/1000", cfg.MaxPollTimeout, cfg.MaxPollLimit)
	}
}

// --- кнопка мини-приложения ------------------------------------------------

// TestMiniAppButtonDefaultsToLink: open_app can make MAX reject whole messages
// until the mini app is registered for the bot, so it must never be on by
// accident.
func TestMiniAppButtonDefaultsToLink(t *testing.T) {
	setEnv(t, map[string]string{"MAX_MODE": "mock", "INTERNAL_API_KEY": "dev-key"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.MaxMiniAppButton != MiniAppButtonLink || cfg.UseOpenAppButtons() {
		t.Fatalf("default = %q, want link", cfg.MaxMiniAppButton)
	}
}

func TestMiniAppButtonOpenApp(t *testing.T) {
	setEnv(t, map[string]string{"MAX_MODE": "mock", "INTERNAL_API_KEY": "dev-key", "MAX_MINIAPP_BUTTON": "open_app"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !cfg.UseOpenAppButtons() {
		t.Fatal("MAX_MINIAPP_BUTTON=open_app not honoured")
	}
	if cfg.Redacted()["max_miniapp_button"] != "open_app" {
		t.Errorf("the mode is missing from the startup log: %v", cfg.Redacted())
	}
}

func TestUnknownMiniAppButtonIsRejected(t *testing.T) {
	setEnv(t, map[string]string{"MAX_MODE": "mock", "INTERNAL_API_KEY": "dev-key", "MAX_MINIAPP_BUTTON": "web_app"})

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MAX_MINIAPP_BUTTON") {
		t.Fatalf("error = %v, want one naming MAX_MINIAPP_BUTTON", err)
	}
}
