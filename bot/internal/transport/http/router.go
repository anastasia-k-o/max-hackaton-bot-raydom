package http

import (
	"log/slog"
	"net/http"
	"time"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/config"
	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/maxapi/mock"
)

// Route paths. Constants rather than literals so the tests, the OpenAPI
// document and the Postman collection all refer to the same strings.
const (
	// PathHealth is the liveness probe.
	PathHealth = "/health"
	// PathReady is the readiness probe.
	PathReady = "/ready"
	// PathNotifications is the Core Backend -> Bot API.
	PathNotifications = "/api/v1/notifications"
	// PathWebhook is the MAX -> Bot webhook.
	PathWebhook = "/webhooks/max"
	// PathDevMaxMessages inspects MockMAX (dev only).
	PathDevMaxMessages = "/dev/max/messages"
	// PathDevCoreActions inspects StubCoreGateway (dev only).
	PathDevCoreActions = "/dev/core/actions"
)

// Deps is everything the router needs.
//
// Passing concrete *mock.Client and *stub.Gateway alongside the interfaces is
// deliberate: the dev endpoints need to read their buffers, and that
// requirement should be visible in the signature rather than hidden behind a
// type assertion on the interface.
type Deps struct {
	Config              config.Config
	Logger              *slog.Logger
	Version             string
	NotificationService *app.NotificationService
	BotService          *app.BotService
	MaxClient           maxapi.Client
	CoreGateway         core.Gateway

	// MockMax is non-nil only when MAX_MODE=mock.
	MockMax *mock.Client
	// StubCore is non-nil only when CORE_MODE=stub.
	StubCore *stub.Gateway
}

// NewRouter builds the HTTP handler for the whole bot.
//
// Authentication is applied per route group, because the three surfaces have
// three different callers:
//
//	/api/v1/*    Core Backend  -> internal API key
//	/webhooks/*  MAX           -> webhook secret
//	/health,/ready,/dev/*       -> unauthenticated
//
// Conflating the two secrets would be a real security bug: the webhook secret
// is handed to MAX, while the internal key is a credential shared only with
// our own backend.
func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	started := time.Now()

	mux.Handle(PathHealth, &healthHandler{version: deps.Version, started: started})
	mux.Handle(PathReady, &readyHandler{
		max:          deps.MaxClient,
		gateway:      deps.CoreGateway,
		updatesMode:  string(deps.Config.MaxUpdatesMode),
		checkTimeout: 3 * time.Second,
	})

	mux.Handle(PathNotifications, chain(
		&notificationsHandler{service: deps.NotificationService},
		internalAPIKeyMiddleware(deps.Config.InternalAPIKey),
	))

	// В режиме polling с настоящим MAX маршрут не поднимается: см.
	// config.Config.WebhookEnabled. Отсутствие маршрута даёт обычный 404 в
	// общем формате ошибок, а не молчаливо принимающий всё эндпойнт.
	if deps.Config.WebhookEnabled() {
		mux.Handle(PathWebhook, chain(
			&webhookHandler{service: deps.BotService},
			maxWebhookSecretMiddleware(deps.Config.MaxWebhookSecret),
		))
	}

	// Dev endpoints are registered only in dev, and only for the adapters
	// that are actually in use.
	if deps.Config.IsDev() {
		if deps.MockMax != nil {
			mux.Handle(PathDevMaxMessages, &mockMessagesHandler{client: deps.MockMax})
		}
		if deps.StubCore != nil {
			mux.Handle(PathDevCoreActions, &stubActionsHandler{gateway: deps.StubCore})
		}
	}

	// Anything unrouted gets the standard error envelope rather than Go's
	// plain-text 404, so every response from this service has one shape.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, contracts.CodeNotFound, "no route matches "+r.URL.Path)
	}))

	return chain(mux,
		requestIDMiddleware(deps.Logger),
		recoverMiddleware,
		loggingMiddleware,
	)
}
