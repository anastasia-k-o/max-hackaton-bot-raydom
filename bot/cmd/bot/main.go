// Command bot runs the MAX notification bot.
//
// main.go is the composition root and the only place that knows which
// implementation of each port is in use. Every switch between mock and real,
// stub and HTTP, happens here based on configuration. Nothing downstream ever
// asks "which mode am I in", which is what makes the promised migration —
// Postman to Core Backend, MockMAX to real MAX, StubCore to a real backend —
// a change of environment variables rather than of code.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/config"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/core/httpgw"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/maxapi/mock"
	"hackatonBotMAX/internal/maxapi/real"
	"hackatonBotMAX/internal/observability"
	"hackatonBotMAX/internal/render"
	transport "hackatonBotMAX/internal/transport/http"
	"hackatonBotMAX/internal/transport/polling"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		// Configuration problems get a human-readable report on stderr and a
		// non-zero exit. Starting with a broken config and failing later,
		// once per message, is strictly worse than failing now.
		fmt.Fprintf(os.Stderr, "\nstartup failed: %v\n\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		if validationErr, ok := config.AsValidationError(err); ok {
			return fmt.Errorf("%s\n\nSee .env.example and docs/MAX_SETUP.md", validationErr)
		}
		return err
	}

	logger := observability.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	logger.Info("starting max bot", append(mapToArgs(cfg.Redacted()), "version", version)...)

	// Root context cancelled on SIGINT/SIGTERM, so a container stop drains
	// in-flight webhooks instead of dropping them.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	maxClient, mockMax, err := buildMaxClient(cfg)
	if err != nil {
		return err
	}

	gateway, stubCore, err := buildCoreGateway(cfg)
	if err != nil {
		return err
	}

	// In real mode, prove the token works before accepting any traffic. The
	// alternative is discovering an invalid token one failed notification at
	// a time, with the bot reporting itself healthy throughout.
	if cfg.MaxMode == config.MaxModeReal {
		if err := verifyMaxToken(ctx, logger, maxClient, cfg.MaxTimeout); err != nil {
			return err
		}
	}

	miniAppURL := strings.TrimSpace(os.Getenv("MINI_APP_URL"))

	renderer := render.New(
		render.WithDefaultMiniAppURL(miniAppURL),
		render.WithOpenAppButtons(cfg.UseOpenAppButtons()),
	)
	if cfg.UseOpenAppButtons() {
		// Stated loudly because the failure mode is silent from here: if the
		// mini app is not registered for this bot, MAX refuses the messages
		// and the only trace is send errors in this log.
		logger.Warn("кнопки мини-приложения: open_app. Мини-приложение должно быть привязано к этому боту в MAX, "+
			"иначе MAX может отвергать сообщения с такой кнопкой. Откат: MAX_MINIAPP_BUTTON=link",
			"max_miniapp_button", string(cfg.MaxMiniAppButton))
	}
	idempotencyStore := idempotency.NewMemoryStore(cfg.IdempotencyTTL)

	notificationService := app.NewNotificationService(maxClient, renderer, idempotencyStore)
	botService := app.NewBotService(maxClient, gateway, renderer, idempotencyStore,
		app.WithMiniAppURL(miniAppURL),
	)

	router := transport.NewRouter(transport.Deps{
		Config:              cfg,
		Logger:              logger,
		Version:             version,
		NotificationService: notificationService,
		BotService:          botService,
		MaxClient:           maxClient,
		CoreGateway:         gateway,
		MockMax:             mockMax,
		StubCore:            stubCore,
	})

	server := transport.NewServer(transport.ServerConfig{
		Addr:    cfg.Addr(),
		Handler: router,
		Logger:  logger,
	})

	if cfg.IsDev() {
		logger.Info("development endpoints enabled",
			"mock_max_messages", transport.PathDevMaxMessages,
			"stub_core_actions", transport.PathDevCoreActions,
		)
	}

	// Порт занимается ДО запуска опроса.
	//
	// Опрос имеет побочный эффект на стороне MAX: получив пачку событий и
	// передав marker в следующем запросе, мы тем самым их фиксируем. Поэтому
	// поллер, который успел бы забрать пачку и затем умереть из-за занятого
	// порта, съел бы события, которых никто не обработал. Связывание порта
	// первым превращает это в честный отказ стартовать.
	if err := server.Listen(); err != nil {
		return err
	}

	// runCtx отменяется либо сигналом, либо фатальной ошибкой опроса. Второе
	// нужно для того, чтобы неверный токен останавливал процесс целиком, а не
	// оставлял HTTP-сервер работать без единственного источника событий.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	var (
		poller     *polling.Poller
		pollerDone chan error
	)

	if cfg.IsPolling() {
		poller = polling.New(maxClient, botService, polling.Config{
			Timeout: cfg.MaxPollTimeout,
			Limit:   cfg.MaxPollLimit,
			Logger:  logger,
		})

		// Проверка подписок до старта опроса: при живой подписке на webhook
		// опрос вернёт пустоту, и без этого предупреждения причина была бы
		// совершенно неочевидной.
		if cfg.MaxMode == config.MaxModeReal {
			checkCtx, cancelCheck := context.WithTimeout(runCtx, cfg.MaxTimeout)
			poller.CheckSubscriptions(checkCtx)
			cancelCheck()
		}

		pollerDone = make(chan error, 1)
		go func() {
			err := poller.Run(runCtx)
			if err != nil {
				logger.Error("опрос событий остановлен фатальной ошибкой",
					observability.KeyUpstreamError, err.Error())
				cancelRun()
			}
			pollerDone <- err
		}()

		logger.Info("режим получения событий: polling (публичный адрес не нужен)",
			"poll_timeout_seconds", cfg.MaxPollTimeout,
			"poll_limit", cfg.MaxPollLimit,
		)
	} else {
		logger.Info("режим получения событий: webhook",
			"path", transport.PathWebhook,
		)
	}

	serverErr := server.Run(runCtx, cfg.ShutdownTimeout)

	// Дождаться завершения опроса, чтобы не оборвать событие на полпути.
	// Ограничение по времени то же, что у HTTP: зависший запрос к MAX не
	// должен удерживать процесс дольше остальных.
	var pollErr error
	if pollerDone != nil {
		cancelRun()
		select {
		case pollErr = <-pollerDone:
		case <-time.After(cfg.ShutdownTimeout):
			logger.Warn("опрос событий не завершился в отведённое время")
		}
	}

	if serverErr != nil {
		return serverErr
	}
	if pollErr != nil {
		return pollErr
	}

	logger.Info("shutdown complete")
	return nil
}

// buildMaxClient selects the MAX adapter.
//
// It returns the concrete *mock.Client alongside the interface so the router
// can mount the dev inspection endpoint. In real mode that value is nil and
// the endpoint is simply not registered.
func buildMaxClient(cfg config.Config) (maxapi.Client, *mock.Client, error) {
	switch cfg.MaxMode {
	case config.MaxModeMock:
		client := mock.New()
		return client, client, nil
	case config.MaxModeReal:
		client, err := real.New(real.Config{
			Token:   cfg.MaxBotToken,
			BaseURL: cfg.MaxBaseURL,
			CAFile:  cfg.MaxCAFile,
			Timeout: cfg.MaxTimeout,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("build real max client: %w", err)
		}
		return client, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported MAX_MODE %q", cfg.MaxMode)
	}
}

// buildCoreGateway selects the Core Backend adapter.
func buildCoreGateway(cfg config.Config) (core.Gateway, *stub.Gateway, error) {
	switch cfg.CoreMode {
	case config.CoreModeStub:
		gateway := stub.New()
		return gateway, gateway, nil
	case config.CoreModeHTTP:
		gateway, err := httpgw.New(httpgw.Config{
			BaseURL: cfg.CoreBaseURL,
			APIKey:  cfg.CoreAPIKey,
			Timeout: cfg.CoreTimeout,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("build http core gateway: %w", err)
		}
		return gateway, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported CORE_MODE %q", cfg.CoreMode)
	}
}

// verifyMaxToken calls GetMe once at startup.
//
// An invalid token is a configuration error, and the process refuses to start
// rather than running in a state where every notification fails. A network
// blip is treated differently: the bot starts and lets /ready report the
// problem, because refusing to boot during a transient outage would be its own
// kind of failure.
func verifyMaxToken(ctx context.Context, logger *slog.Logger, client maxapi.Client, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	info, err := client.GetMe(checkCtx)
	if err != nil {
		if errors.Is(err, maxapi.ErrUnauthorized) {
			return fmt.Errorf(
				"MAX rejected the bot token.\n"+
					"  Check MAX_BOT_TOKEN in your .env: it should be the token @MasterBot sent you.\n"+
					"  Underlying error: %w", err)
		}
		if errors.Is(err, real.ErrUntrustedCertificate) {
			// Недоверенный сертификат — проблема этой машины, а не MAX, и
			// сама она не рассосётся. Стартовать в таком состоянии значит
			// получать ту же ошибку на каждом уведомлении.
			return fmt.Errorf(
				"TLS-соединение с MAX не устанавливается.\n"+
					"  Это проблема доверенных сертификатов на этой машине, а не MAX.\n"+
					"  Диагностика:  bash scripts/max-tls-check.sh\n"+
					"  Подробности:  docs/MAX_SETUP.md, раздел «TLS и сертификаты»\n"+
					"  Исходная ошибка: %w", err)
		}
		if maxapi.IsTemporary(err) {
			// Reachability, not validity. Start and let /ready say so.
			logger.Warn("MAX API not reachable at startup; continuing, /ready will report it",
				observability.KeyUpstreamError, err.Error())
			return nil
		}
		return fmt.Errorf("MAX API check failed: %w", err)
	}

	logger.Info("max token verified",
		"bot_user_id", info.UserID,
		"bot_username", info.Username,
		"bot_name", info.Name,
	)
	return nil
}

// mapToArgs flattens a map into the key/value pairs slog expects.
func mapToArgs(values map[string]any) []any {
	args := make([]any, 0, len(values)*2)
	for key, value := range values {
		args = append(args, key, value)
	}
	return args
}
