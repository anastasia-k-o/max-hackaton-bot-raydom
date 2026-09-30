package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/config"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi/mock"
	"hackatonBotMAX/internal/observability"
	"hackatonBotMAX/internal/render"
)

// Здесь проверяется то, что меняет в HTTP-поверхности переключение источника
// событий: наличие маршрута webhook и то, что /ready называет режим.

func routerFor(t *testing.T, maxMode config.MaxMode, updatesMode config.UpdatesMode) http.Handler {
	t.Helper()

	cfg := config.Config{
		AppEnv:         config.EnvDev,
		HTTPPort:       8080,
		LogLevel:       "error",
		MaxMode:        maxMode,
		MaxUpdatesMode: updatesMode,
		CoreMode:       config.CoreModeStub,
		InternalAPIKey: testInternalKey,
		IdempotencyTTL: time.Minute,
	}
	// Секрет задаётся только там, где он вообще требуется конфигурацией.
	if cfg.MaxMode == config.MaxModeReal && updatesMode == config.UpdatesModeWebhook {
		cfg.MaxWebhookSecret = testWebhookSecret
	}

	maxClient := mock.New()
	gateway := stub.New()
	renderer := render.New()
	store := idempotency.NewMemoryStore(time.Minute)

	return NewRouter(Deps{
		Config:              cfg,
		Logger:              observability.NewLogger("error"),
		Version:             "test",
		NotificationService: app.NewNotificationService(maxClient, renderer, store),
		BotService:          app.NewBotService(maxClient, gateway, renderer, store),
		MaxClient:           maxClient,
		CoreGateway:         gateway,
		MockMax:             maxClient,
		StubCore:            gateway,
	})
}

// TestWebhookRouteIsAbsentInRealPollingMode.
//
// В этом сочетании MAX_WEBHOOK_SECRET не требуется, поэтому проверять подпись
// было бы нечем. Открытый эндпойнт, принимающий «события от MAX», позволил бы
// кому угодно подтвердить чужую регистрацию — поэтому маршрута просто нет.
func TestWebhookRouteIsAbsentInRealPollingMode(t *testing.T) {
	router := routerFor(t, config.MaxModeReal, config.UpdatesModePolling)

	request := httptest.NewRequest(http.MethodPost, PathWebhook, strings.NewReader(`{"update_type":"bot_started"}`))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("POST %s вернул %d, ожидался 404: %s", PathWebhook, recorder.Code, recorder.Body.String())
	}

	// Ответ должен быть в общем формате ошибок, а не голым текстом Go.
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не в общем формате ошибок: %s", recorder.Body.String())
	}
	if body.Error.Code == "" {
		t.Fatalf("в ответе нет кода ошибки: %s", recorder.Body.String())
	}
}

// TestWebhookRouteStaysInEveryOtherCombination.
//
// В режиме mock маршрут нужен всегда: через него Postman и smoke-тест подают
// боту события, и это единственный способ прогнать полный цикл без сети.
func TestWebhookRouteStaysInEveryOtherCombination(t *testing.T) {
	tests := []struct {
		name    string
		max     config.MaxMode
		updates config.UpdatesMode
	}{
		{"mock + webhook", config.MaxModeMock, config.UpdatesModeWebhook},
		{"mock + polling", config.MaxModeMock, config.UpdatesModePolling},
		{"real + webhook", config.MaxModeReal, config.UpdatesModeWebhook},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := routerFor(t, tc.max, tc.updates)

			request := httptest.NewRequest(http.MethodPost, PathWebhook,
				strings.NewReader(`{"update_type":"bot_started","timestamp":1,"user":{"user_id":1}}`))
			if tc.max == config.MaxModeReal {
				request.Header.Set(HeaderMaxSecret, testWebhookSecret)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusNotFound {
				t.Fatalf("маршрут %s отсутствует, хотя должен быть", PathWebhook)
			}
		})
	}
}

// TestReadyReportsTheUpdatesMode.
//
// Чаще всего «бот не отвечает на кнопки» — это расхождение между тем, как бот
// настроен, и тем, как он подписан. /ready должен отвечать на первый из двух
// вопросов без чтения логов.
func TestReadyReportsTheUpdatesMode(t *testing.T) {
	for _, mode := range []config.UpdatesMode{config.UpdatesModeWebhook, config.UpdatesModePolling} {
		t.Run(string(mode), func(t *testing.T) {
			router := routerFor(t, config.MaxModeMock, mode)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, PathReady, nil))

			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s вернул %d: %s", PathReady, recorder.Code, recorder.Body.String())
			}

			var body struct {
				Status      string `json:"status"`
				UpdatesMode string `json:"updates_mode"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("разбор ответа: %v", err)
			}
			if body.UpdatesMode != string(mode) {
				t.Fatalf("updates_mode = %q, ожидался %q", body.UpdatesMode, mode)
			}
			if body.Status != "ready" {
				t.Fatalf("status = %q, ожидался ready", body.Status)
			}
		})
	}
}
