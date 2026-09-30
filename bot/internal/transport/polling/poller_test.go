package polling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/config"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/maxapi/mock"
	"hackatonBotMAX/internal/render"
	transport "hackatonBotMAX/internal/transport/http"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func newTestPoller(t *testing.T) (*Poller, *mock.Client, *stub.Gateway) {
	t.Helper()

	maxClient := mock.New()
	gateway := stub.New()
	service := app.NewBotService(
		maxClient,
		gateway,
		render.New(render.WithDefaultMiniAppURL("https://example.ru/app")),
		idempotency.NewMemoryStore(time.Minute),
	)

	return New(maxClient, service, Config{Logger: quietLogger()}), maxClient, gateway
}

// callbackUpdate builds a message_callback update the way MAX sends one.
func callbackUpdate(t *testing.T, action domain.ActionType, registrationID string, presserID int64) maxapi.Update {
	t.Helper()
	payload := callback.MustEncode(action, registrationID, "event_42")
	return maxapi.Update{
		Type:      maxapi.UpdateMessageCallback,
		Timestamp: time.Now().UnixMilli(),
		ChatID:    555,
		Message: &maxapi.UpdateMessage{
			Recipient: maxapi.UpdateRecipient{ChatID: 555, UserID: presserID, ChatType: "dialog"},
			Body:      maxapi.UpdateMessageBody{MID: "mid-" + registrationID},
		},
		Callback: &maxapi.UpdateCallback{
			CallbackID: fmt.Sprintf("cb-%s", registrationID),
			Payload:    payload,
			User:       maxapi.UpdateUser{UserID: presserID, Name: "Тестовый пользователь"},
		},
	}
}

// runPoller starts Run in a goroutine and returns a stop function that cancels
// it and waits for it to finish, reporting the error Run returned.
func runPoller(t *testing.T, poller *Poller) (stop func() error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- poller.Run(ctx) }()

	var once sync.Once
	var runErr error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case runErr = <-done:
			case <-time.After(5 * time.Second):
				t.Error("Run() не завершился после отмены контекста")
			}
		})
		return runErr
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

// TestPollerDeliversEveryUpdateAndAdvancesMarker проверяет главное свойство
// цикла: каждое событие доходит до BotService ровно один раз, а marker
// продвигается до конца очереди.
func TestPollerDeliversEveryUpdateAndAdvancesMarker(t *testing.T) {
	poller, maxClient, gateway := newTestPoller(t)

	maxClient.EnqueueUpdates(
		callbackUpdate(t, domain.ActionConfirmRegistration, "registration_1", 111),
		callbackUpdate(t, domain.ActionCancelRegistration, "registration_2", 222),
		callbackUpdate(t, domain.ActionAcceptWaitlist, "registration_3", 333),
	)

	stop := runPoller(t, poller)

	waitFor(t, "три действия в StubCore", func() bool { return len(gateway.Actions()) == 3 })
	waitFor(t, "marker дошёл до конца очереди", func() bool { return poller.Marker() == 3 })

	if err := stop(); err != nil {
		t.Fatalf("Run() вернул ошибку: %v", err)
	}

	// Пачка не должна обрабатываться повторно после продвижения marker.
	if got := len(gateway.Actions()); got != 3 {
		t.Fatalf("действий в StubCore: %d, ожидалось 3 (события обработаны повторно)", got)
	}

	kinds := make([]stub.ActionKind, 0, 3)
	for _, action := range gateway.Actions() {
		kinds = append(kinds, action.Kind)
	}
	want := []stub.ActionKind{stub.KindConfirmRegistration, stub.KindCancelRegistration, stub.KindAcceptWaitlist}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("действие %d = %q, ожидалось %q (порядок событий не сохранён)", i, kinds[i], want[i])
		}
	}
}

// TestPollingAndWebhookAgree — то, ради чего существует общий BotService.
//
// Одно и то же событие, поданное через webhook и через опрос, должно привести
// к одному и тому же действию в Core Backend. Если два транспорта когда-нибудь
// разойдутся, этот тест упадёт раньше, чем это заметит пользователь.
func TestPollingAndWebhookAgree(t *testing.T) {
	update := callbackUpdate(t, domain.ActionConfirmRegistration, "registration_77", 999)

	// --- через опрос ---
	pollPoller, _, pollGateway := newTestPoller(t)
	pollPoller.client.(*mock.Client).EnqueueUpdates(update)

	stop := runPoller(t, pollPoller)
	waitFor(t, "действие после опроса", func() bool { return len(pollGateway.Actions()) == 1 })
	_ = stop()

	// --- через webhook ---
	webhookMax := mock.New()
	webhookGateway := stub.New()
	webhookService := app.NewBotService(
		webhookMax,
		webhookGateway,
		render.New(render.WithDefaultMiniAppURL("https://example.ru/app")),
		idempotency.NewMemoryStore(time.Minute),
	)
	router := transport.NewRouter(transport.Deps{
		Config: config.Config{
			AppEnv:         config.EnvDev,
			MaxMode:        config.MaxModeMock,
			MaxUpdatesMode: config.UpdatesModeWebhook,
			InternalAPIKey: "test-key",
		},
		Logger:     quietLogger(),
		BotService: webhookService,
		MaxClient:  webhookMax,
		// NotificationService нулевой: этот тест его не трогает.
		CoreGateway: webhookGateway,
		MockMax:     webhookMax,
		StubCore:    webhookGateway,
	})

	body, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, transport.PathWebhook, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("webhook вернул %d, ожидалось 200: %s", recorder.Code, recorder.Body.String())
	}

	// --- сравнение ---
	pollActions, webhookActions := pollGateway.Actions(), webhookGateway.Actions()
	if len(pollActions) != 1 || len(webhookActions) != 1 {
		t.Fatalf("действий: опрос %d, webhook %d, ожидалось по одному", len(pollActions), len(webhookActions))
	}
	if pollActions[0].Kind != webhookActions[0].Kind {
		t.Fatalf("вид действия разошёлся: опрос %q, webhook %q", pollActions[0].Kind, webhookActions[0].Kind)
	}
	if pollActions[0].RegistrationID != webhookActions[0].RegistrationID {
		t.Fatalf("registration_id разошёлся: опрос %q, webhook %q",
			pollActions[0].RegistrationID, webhookActions[0].RegistrationID)
	}
	if pollActions[0].MaxUserID != webhookActions[0].MaxUserID {
		t.Fatalf("max_user_id разошёлся: опрос %d, webhook %d",
			pollActions[0].MaxUserID, webhookActions[0].MaxUserID)
	}
}

// flakyClient роняет первые failures вызовов GetUpdates, дальше ведёт себя
// как обычный MockMAX.
//
// mock.Client.FailNext рассчитан на один сбой (следующий вызов перезаписывает
// предыдущий), а здесь нужна именно череда неудач подряд — иначе рост паузы
// проверить нечем.
type flakyClient struct {
	*mock.Client

	mu       sync.Mutex
	failures int
	err      error
}

func (c *flakyClient) GetUpdates(ctx context.Context, req maxapi.GetUpdatesRequest) (*maxapi.UpdateBatch, error) {
	c.mu.Lock()
	if c.failures > 0 {
		c.failures--
		c.mu.Unlock()
		return nil, c.err
	}
	c.mu.Unlock()
	return c.Client.GetUpdates(ctx, req)
}

// TestPollerBacksOffAndKeepsMarkerOnFailure проверяет, что неудачный опрос не
// двигает marker и что пауза между попытками растёт.
func TestPollerBacksOffAndKeepsMarkerOnFailure(t *testing.T) {
	const failures = 3

	maxClient := &flakyClient{
		Client:   mock.New(),
		failures: failures,
		err:      &maxapi.Error{Op: "get_updates", StatusCode: 503, Temporary: true},
	}
	gateway := stub.New()
	service := app.NewBotService(
		maxClient,
		gateway,
		render.New(render.WithDefaultMiniAppURL("https://example.ru/app")),
		idempotency.NewMemoryStore(time.Minute),
	)
	poller := New(maxClient, service, Config{Logger: quietLogger()})

	var (
		mu     sync.Mutex
		delays []time.Duration
	)
	poller.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		// Пауза записывается только пока идут сбои: после них те же вызовы
		// sleep приходят от страховки от горячего цикла, и смешивать их с
		// задержками повтора значило бы проверять не то.
		if len(delays) < failures {
			delays = append(delays, d)
		}
		mu.Unlock()
		// Реального сна в тесте не нужно: проверяется решение, а не пауза.
		return ctx.Err()
	}

	maxClient.EnqueueUpdates(callbackUpdate(t, domain.ActionConfirmRegistration, "registration_1", 111))

	stop := runPoller(t, poller)
	waitFor(t, "событие всё-таки обработано после повторов", func() bool { return len(gateway.Actions()) == 1 })
	_ = stop()

	mu.Lock()
	defer mu.Unlock()

	if len(delays) != failures {
		t.Fatalf("пауз между попытками: %d, ожидалось %d (%v)", len(delays), failures, delays)
	}
	want := []time.Duration{defaultBackoff, 2 * defaultBackoff, 4 * defaultBackoff}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("паузы = %v, ожидались %v", delays, want)
		}
	}

	// Marker дошёл до единственного события: временные сбои его не сдвинули и
	// не съели.
	if poller.Marker() != 1 {
		t.Fatalf("marker = %d, ожидался 1", poller.Marker())
	}
}

// TestBackoffIsCapped — пауза не растёт бесконечно.
func TestBackoffIsCapped(t *testing.T) {
	current := defaultBackoff
	for i := 0; i < 100; i++ {
		current = nextBackoff(current)
	}
	if current != maxBackoff {
		t.Fatalf("пауза после ста неудач = %v, ожидался потолок %v", current, maxBackoff)
	}
}

// TestPollerStopsOnUnauthorized: неверный токен — не повод повторять.
//
// Повтор при 401 — это бесконечный цикл с гарантированно одним и тем же
// результатом, поэтому Run возвращает ошибку, а main останавливает процесс.
func TestPollerStopsOnUnauthorized(t *testing.T) {
	poller, maxClient, _ := newTestPoller(t)

	slept := false
	poller.sleep = func(ctx context.Context, d time.Duration) error {
		slept = true
		return nil
	}

	maxClient.FailNext(fmt.Errorf("%w (HTTP 401)", maxapi.ErrUnauthorized))

	done := make(chan error, 1)
	go func() { done <- poller.Run(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() вернул nil, ожидалась ошибка при неверном токене")
		}
		if !errors.Is(err, maxapi.ErrUnauthorized) {
			t.Fatalf("Run() вернул %v, ожидалась обёртка над maxapi.ErrUnauthorized", err)
		}
		if !strings.Contains(err.Error(), "MAX_BOT_TOKEN") {
			t.Fatalf("сообщение не подсказывает, что проверять: %q", err.Error())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() не вернулся при 401: похоже, ушёл в повторы")
	}

	if slept {
		t.Fatal("Run() уснул перед выходом: при 401 повторять нечего")
	}
}

// TestPollerStopsOnContextCancel: отмена останавливает цикл, а не «когда-нибудь».
func TestPollerStopsOnContextCancel(t *testing.T) {
	poller, maxClient, _ := newTestPoller(t)
	maxClient.EnqueueUpdates(callbackUpdate(t, domain.ActionConfirmRegistration, "registration_1", 111))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- poller.Run(ctx) }()

	waitFor(t, "первая пачка обработана", func() bool { return poller.Marker() == 1 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() вернул %v, при штатной остановке ожидался nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() не завершился в течение трёх секунд после отмены")
	}
}

// TestPollerDoesNotSpinOnEmptyResponses.
//
// Настоящий MAX удерживает соединение timeout секунд, и пауза не нужна. Но
// подменный клиент отвечает мгновенно, и без страховки цикл съел бы ядро.
// Проверяется именно страховка.
func TestPollerDoesNotSpinOnEmptyResponses(t *testing.T) {
	poller, _, _ := newTestPoller(t)

	var mu sync.Mutex
	idleSleeps := 0
	poller.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		idleSleeps++
		mu.Unlock()
		return sleepCtx(ctx, 10*time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run(): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if idleSleeps == 0 {
		t.Fatal("пустые ответы не вызвали ни одной паузы: цикл крутится вхолостую")
	}
	// Верхняя граница — грубая: за 100 мс при паузе 10 мс итераций должно
	// быть около десятка, а не тысячи.
	if idleSleeps > 50 {
		t.Fatalf("пауз %d за 100 мс: страховка не работает", idleSleeps)
	}
}

// TestCheckSubscriptionsWarnsWhenWebhookIsRegistered.
//
// Живая подписка на webhook при включённом опросе — самый неочевидный способ
// получить «бот молчит»: события уходят на подписку, опрос возвращает пустоту,
// и ни одной ошибки нигде нет.
func TestCheckSubscriptionsWarnsWhenWebhookIsRegistered(t *testing.T) {
	maxClient := mock.New()
	maxClient.SetSubscriptions(maxapi.Subscription{
		URL:         "https://someone-else.example.com/webhooks/max",
		UpdateTypes: maxapi.HandledUpdateTypes(),
	})

	var logs bytes.Buffer
	poller := New(maxClient, nil, Config{
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	poller.CheckSubscriptions(context.Background())

	output := logs.String()
	if !strings.Contains(output, "level=WARN") {
		t.Fatalf("предупреждения нет в логе:\n%s", output)
	}
	if !strings.Contains(output, "someone-else.example.com") {
		t.Fatalf("в предупреждении нет адреса подписки:\n%s", output)
	}
}

// TestCheckSubscriptionsIsQuietWhenThereAreNone — обратная сторона: при
// пустом списке предупреждать не о чем.
func TestCheckSubscriptionsIsQuietWhenThereAreNone(t *testing.T) {
	maxClient := mock.New()

	var logs bytes.Buffer
	poller := New(maxClient, nil, Config{
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	poller.CheckSubscriptions(context.Background())

	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("предупреждение без подписок:\n%s", logs.String())
	}
}

// TestCheckSubscriptionsSurvivesFailure — невозможность прочитать подписки не
// должна мешать боту работать.
func TestCheckSubscriptionsSurvivesFailure(t *testing.T) {
	maxClient := mock.New()
	maxClient.FailNext(errors.New("сеть недоступна"))

	var logs bytes.Buffer
	poller := New(maxClient, nil, Config{
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	// Главное — что вызов не паникует и возвращает управление.
	poller.CheckSubscriptions(context.Background())

	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("сбой проверки подписок не отмечен в логе:\n%s", logs.String())
	}
}

// TestConfigLimitsAreClamped — значения за границами схемы MAX приводятся к
// допустимым, а не уезжают в запрос.
func TestConfigLimitsAreClamped(t *testing.T) {
	tests := []struct {
		name                   string
		in                     Config
		wantTimeout, wantLimit int
	}{
		{"нули дают значения по умолчанию", Config{}, DefaultTimeout, DefaultLimit},
		{"слишком большие обрезаются", Config{Timeout: 5000, Limit: 50000}, MaxTimeout, MaxLimit},
		{"отрицательные дают значения по умолчанию", Config{Timeout: -1, Limit: -1}, DefaultTimeout, DefaultLimit},
		{"допустимые сохраняются", Config{Timeout: 45, Limit: 250}, 45, 250},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Logger = quietLogger()
			poller := New(mock.New(), nil, tc.in)
			if poller.timeout != tc.wantTimeout {
				t.Errorf("timeout = %d, ожидалось %d", poller.timeout, tc.wantTimeout)
			}
			if poller.limit != tc.wantLimit {
				t.Errorf("limit = %d, ожидалось %d", poller.limit, tc.wantLimit)
			}
		})
	}
}

// TestSleepCtxWakesOnCancel — без этого остановка процесса ждала бы конца
// паузы, до тридцати секунд.
func TestSleepCtxWakesOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- sleepCtx(ctx, time.Hour) }()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sleepCtx вернул %v, ожидался context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sleepCtx не проснулся по отмене")
	}
}
