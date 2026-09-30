// Package polling — входящий адаптер, забирающий события у MAX методом long
// polling вместо webhook.
//
// Зачем он нужен. Webhook требует, чтобы MAX мог постучаться к нам, то есть
// публичный HTTPS-адрес. На машине разработчика за NAT такого адреса нет, и
// обычный выход — туннель (ngrok, cloudflared). Туннель — это внешний сервис,
// аккаунт и ещё одна точка отказа ровно там, где её не ждут. Long polling
// разворачивает направление: соединение устанавливаем мы, и никакого входящего
// адреса не требуется вовсе.
//
// Режимы взаимоисключающие. В схеме MAX про GET /updates сказано прямо: метод
// предназначен для бота, который НЕ подписан на webhook. Если подписка
// существует, события уйдут на неё, а опрос будет возвращать пустоту — и
// причина этого совершенно неочевидна снаружи. Поэтому Poller при старте
// проверяет подписки и громко предупреждает.
//
// Что здесь важно с точки зрения архитектуры: Poller не содержит ни грамма
// бизнес-логики. Он вызывает тот же app.BotService.HandleUpdate, что и
// обработчик webhook, с тем же maxapi.Update на входе. Смена транспорта
// событий не меняет поведения бота — это свойство по построению, а не по
// договорённости.
package polling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/observability"
)

// Значения по умолчанию и границы.
//
// Границы limit и timeout заданы схемой MAX; здесь они продублированы, чтобы
// ошибка в конфигурации превращалась в исправление, а не в HTTP 400 на каждой
// итерации.
const (
	// DefaultTimeout — сколько секунд MAX удерживает соединение, ожидая
	// событий. Это и есть «long» в long polling.
	DefaultTimeout = 30
	// DefaultLimit — максимум событий в одном ответе.
	DefaultLimit = 100

	// MaxTimeout и MaxLimit — потолки из схемы MAX.
	MaxTimeout = 90
	MaxLimit   = 1000

	// defaultBackoff — пауза после первой неудачи.
	defaultBackoff = 1 * time.Second
	// maxBackoff — потолок паузы. Больше смысла нет: MAX может вернуться в
	// любой момент, и полминуты — уже заметная задержка для пользователя,
	// который ждёт ответа на нажатие кнопки.
	maxBackoff = 30 * time.Second

	// idleDelay — страховка от горячего цикла.
	//
	// Штатно пустой ответ приходит через timeout секунд, и пауза не нужна.
	// Но если сервер (или подменный сервер в тесте) отвечает пустотой
	// мгновенно, цикл без паузы съест ядро. Пауза применяется только тогда,
	// когда ответ пришёл быстро И оказался пустым.
	idleDelay = 1 * time.Second
	// idleThreshold — что считать «быстро».
	idleThreshold = 500 * time.Millisecond
)

// Config настраивает опрос.
type Config struct {
	// Timeout — секунды удержания соединения (0..90). Ноль — значение по
	// умолчанию.
	Timeout int
	// Limit — максимум событий за ответ (1..1000). Ноль — по умолчанию.
	Limit int
	// Types ограничивает набор типов событий. Пусто — все.
	Types []string
	// Logger обязателен.
	Logger *slog.Logger
}

// Poller опрашивает MAX и передаёт события в BotService.
type Poller struct {
	client  maxapi.Client
	service *app.BotService
	logger  *slog.Logger

	timeout int
	limit   int
	types   []string

	// marker — позиция в потоке событий.
	//
	// Семантика MAX: marker указывает на СЛЕДУЮЩЕЕ ожидаемое событие, и сам
	// факт передачи marker ФИКСИРУЕТ все предыдущие. Отдельного «подтвердить
	// обработку» в API нет — подтверждением служит следующий запрос.
	//
	// Атомарный, потому что пишет его цикл в своей горутине, а читает
	// Marker() — кто угодно снаружи: тест, диагностика, будущий /ready.
	marker atomic.Int64

	// Точки подмены для тестов.
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

// New собирает Poller.
func New(client maxapi.Client, service *app.BotService, cfg Config) *Poller {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}

	limit := cfg.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	return &Poller{
		client:  client,
		service: service,
		logger:  logger.With(observability.KeyComponent, "polling"),
		timeout: timeout,
		limit:   limit,
		types:   cfg.Types,
		sleep:   sleepCtx,
		now:     time.Now,
	}
}

// Marker возвращает текущую позицию в потоке событий. Для тестов и диагностики.
func (p *Poller) Marker() int64 { return p.marker.Load() }

// CheckSubscriptions предупреждает о конфликте режимов.
//
// Вызывается при старте отдельно от Run, чтобы результат попал в лог раньше,
// чем начнётся опрос, и чтобы неудача самой проверки не мешала работе:
// невозможность прочитать список подписок — не причина не запускаться.
func (p *Poller) CheckSubscriptions(ctx context.Context) {
	subscriptions, err := p.client.ListSubscriptions(ctx)
	if err != nil {
		p.logger.Warn("не удалось проверить подписки на webhook; опрос всё равно запускается",
			observability.KeyUpstreamError, err.Error())
		return
	}

	if len(subscriptions) == 0 {
		p.logger.Info("подписок на webhook нет — режим polling применим")
		return
	}

	urls := make([]string, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		urls = append(urls, subscription.URL)
	}

	// Это предупреждение, а не ошибка: подписка могла остаться от другой
	// команды, и удалять её молча мы точно не будем. Но молчать тоже нельзя —
	// пустой опрос при живой подписке выглядит как сломанный бот.
	p.logger.Warn("у бота есть активные подписки на webhook, а режим — polling; "+
		"события будут уходить на webhook, и опрос вернёт пустоту",
		"subscriptions", urls,
		"hint", "удалите подписку (DELETE /subscriptions?url=...) или переключитесь на MAX_UPDATES_MODE=webhook",
	)
}

// Run ведёт опрос до отмены контекста.
//
// Возвращает nil при штатной остановке и ошибку только тогда, когда
// продолжать бессмысленно: единственный такой случай — MAX не принял токен.
// Всё остальное (сеть, 5xx, таймауты) — повод подождать и попробовать снова,
// а не повод уронить процесс.
func (p *Poller) Run(ctx context.Context) error {
	p.logger.Info("polling started",
		"timeout_seconds", p.timeout,
		"limit", p.limit,
	)
	defer func() { p.logger.Info("polling stopped", "marker", p.marker.Load()) }()

	backoff := defaultBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		started := p.now()
		batch, err := p.client.GetUpdates(ctx, maxapi.GetUpdatesRequest{
			Marker:  p.marker.Load(),
			Limit:   p.limit,
			Timeout: p.timeout,
			Types:   p.types,
		})
		elapsed := p.now().Sub(started)

		if err != nil {
			// Отмена во время ожидания — не сбой, а штатная остановка.
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, maxapi.ErrUnauthorized) {
				return fmt.Errorf(
					"MAX не принял токен при опросе событий.\n"+
						"  Проверьте MAX_BOT_TOKEN в .env.\n"+
						"  Исходная ошибка: %w", err)
			}

			p.logger.Warn("опрос событий не удался, повтор после паузы",
				observability.KeyUpstreamError, err.Error(),
				"retry_in", backoff.String(),
			)
			if sleepErr := p.sleep(ctx, backoff); sleepErr != nil {
				return nil
			}
			backoff = nextBackoff(backoff)
			// Marker НЕ продвигается: пачка не получена, фиксировать нечего.
			continue
		}

		backoff = defaultBackoff

		if batch == nil {
			batch = &maxapi.UpdateBatch{}
		}
		if batch.Skipped > 0 {
			p.logger.Warn("часть событий не разобралась и отброшена",
				"skipped", batch.Skipped,
			)
		}

		for _, update := range batch.Updates {
			p.handle(ctx, update)
		}

		// Marker продвигается после обработки пачки, даже если какое-то
		// событие обработать не удалось.
		//
		// Соблазнительная альтернатива — не двигать marker до успеха — на
		// деле хуже: сбой на одном событии остановил бы весь поток, а
		// повторная выдача всё равно была бы отброшена как дубликат
		// (idempotency.Store помнит уже виденные id). Получился бы бот,
		// который встал навсегда и ничего при этом не переобработал.
		if batch.Marker > 0 {
			p.marker.Store(batch.Marker)
		}

		// Страховка от горячего цикла, см. idleDelay.
		if len(batch.Updates) == 0 && elapsed < idleThreshold {
			if err := p.sleep(ctx, idleDelay); err != nil {
				return nil
			}
		}
	}
}

// handle передаёт одно событие в BotService.
//
// Ошибка обработки логируется и не прерывает пачку: у webhook в этом месте
// есть код ответа, которым можно попросить повтор, у опроса такого рычага нет.
func (p *Poller) handle(ctx context.Context, update maxapi.Update) {
	logger := p.logger.With(
		observability.KeyUpdateType, string(update.Type),
		observability.KeyMaxUserID, update.ActorUserID(),
	)
	updateCtx := observability.WithLogger(ctx, logger)

	outcome, err := p.service.HandleUpdate(updateCtx, update)
	if err != nil {
		logger.Error("событие не обработано",
			observability.KeyUpstreamError, err.Error(),
		)
		return
	}

	logger.Debug("событие обработано",
		observability.KeyResult, outcome.Result,
	)
}

// nextBackoff удваивает паузу до потолка.
func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}

// sleepCtx спит, но просыпается на отмене контекста.
//
// Возвращает ошибку контекста, если сон был прерван, и nil, если истёк
// полностью. Без этого остановка процесса ждала бы конца паузы — до тридцати
// секунд в худшем случае.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
