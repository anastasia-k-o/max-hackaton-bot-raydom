package real

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/maxapi"
)

// Эти тесты фиксируют форму двух запросов, которые адаптер строит сам, минуя
// SDK: GET /updates и GET /subscriptions. Именно потому, что SDK здесь не
// прикрывает, форма должна быть проверена, а не подразумеваться.

func TestGetUpdatesRequestShape(t *testing.T) {
	var got *http.Request

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"updates":[],"marker":0}`))
	}))

	_, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{
		Marker:  42,
		Limit:   50,
		Timeout: 5,
		Types:   []string{"message_callback", "bot_started"},
	})
	if err != nil {
		t.Fatalf("GetUpdates(): %v", err)
	}

	if got.Method != http.MethodGet {
		t.Errorf("method = %s, ожидался GET", got.Method)
	}
	if got.URL.Path != "/updates" {
		t.Errorf("path = %s, ожидался /updates", got.URL.Path)
	}

	query := got.URL.Query()
	for key, want := range map[string]string{
		"marker":  "42",
		"limit":   "50",
		"timeout": "5",
		"types":   "message_callback,bot_started",
	} {
		if query.Get(key) != want {
			t.Errorf("%s = %q, ожидалось %q", key, query.Get(key), want)
		}
	}

	// Токен идёт голым, без префикса Bearer — как и во всех остальных вызовах.
	if auth := got.Header.Get("Authorization"); auth != "test-token" {
		t.Errorf("Authorization = %q, ожидался голый токен", auth)
	}
}

// TestFirstPollOmitsMarker.
//
// Нулевой marker и отсутствие marker — не одно и то же. По схеме MAX
// отсутствие параметра означает «отдай всё с последней фиксации», и именно это
// нужно при старте. Передать marker=0 значило бы попросить перечитать поток с
// начала.
func TestFirstPollOmitsMarker(t *testing.T) {
	var query string

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"updates":[],"marker":7}`))
	}))

	if _, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{Marker: 0}); err != nil {
		t.Fatalf("GetUpdates(): %v", err)
	}

	if strings.Contains(query, "marker") {
		t.Fatalf("в запросе есть marker при нулевом значении: %q", query)
	}
}

// TestGetUpdatesClampsToSchemaLimits — значения за границами схемы обрезаются
// здесь, а не отдаются MAX на растерзание в виде HTTP 400.
func TestGetUpdatesClampsToSchemaLimits(t *testing.T) {
	tests := []struct {
		name                   string
		in                     maxapi.GetUpdatesRequest
		wantLimit, wantTimeout string
	}{
		{"нули дают значения по умолчанию", maxapi.GetUpdatesRequest{}, "100", "30"},
		{"потолки схемы", maxapi.GetUpdatesRequest{Limit: 9999, Timeout: 9999}, "1000", "90"},
		// Ноль — это «не задано», а не «не удерживать соединение»: см.
		// комментарий у GetUpdatesRequest.Timeout.
		{"нулевой timeout означает значение по умолчанию", maxapi.GetUpdatesRequest{Timeout: 0}, "100", "30"},
		{"отрицательный timeout тоже", maxapi.GetUpdatesRequest{Timeout: -5}, "100", "30"},
		{"допустимые значения сохраняются", maxapi.GetUpdatesRequest{Limit: 250, Timeout: 45}, "250", "45"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var query map[string][]string

			client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				_, _ = w.Write([]byte(`{"updates":[],"marker":0}`))
			}))

			if _, err := client.GetUpdates(context.Background(), tc.in); err != nil {
				t.Fatalf("GetUpdates(): %v", err)
			}

			if got := query["limit"][0]; got != tc.wantLimit {
				t.Errorf("limit = %q, ожидалось %q", got, tc.wantLimit)
			}
			if got := query["timeout"][0]; got != tc.wantTimeout {
				t.Errorf("timeout = %q, ожидалось %q", got, tc.wantTimeout)
			}
		})
	}
}

// TestGetUpdatesDecodesTheSameWayAsTheWebhook.
//
// Свойство, ради которого эти вызовы сделаны напрямую, а не через SDK: событие
// проходит через тот же maxapi.DecodeUpdate, что и тело webhook. Разбор один —
// значит и поведение в двух режимах одно.
func TestGetUpdatesDecodesTheSameWayAsTheWebhook(t *testing.T) {
	const body = `{
		"updates": [
			{
				"update_type": "message_callback",
				"timestamp": 1700000000000,
				"chat_id": 555,
				"callback": {
					"callback_id": "cb-1",
					"payload": "v1|confirm_registration|registration_15|event_42",
					"user": {"user_id": 123456789, "name": "Пользователь"}
				}
			}
		],
		"marker": 991
	}`

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))

	batch, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{})
	if err != nil {
		t.Fatalf("GetUpdates(): %v", err)
	}

	if len(batch.Updates) != 1 {
		t.Fatalf("событий: %d, ожидалось 1", len(batch.Updates))
	}
	if batch.Marker != 991 {
		t.Errorf("marker = %d, ожидался 991", batch.Marker)
	}

	update := batch.Updates[0]
	if update.Type != maxapi.UpdateMessageCallback {
		t.Errorf("тип = %q, ожидался message_callback", update.Type)
	}
	// Личность берётся из callback.user.user_id — так же, как в webhook.
	if update.ActorUserID() != 123456789 {
		t.Errorf("ActorUserID() = %d, ожидалось 123456789", update.ActorUserID())
	}
	if update.Callback.Payload != "v1|confirm_registration|registration_15|event_42" {
		t.Errorf("payload = %q", update.Callback.Payload)
	}

	// То же тело, поданное как webhook, должно разобраться идентично.
	raw := []byte(`{"update_type":"message_callback","timestamp":1700000000000,"chat_id":555,` +
		`"callback":{"callback_id":"cb-1","payload":"v1|confirm_registration|registration_15|event_42",` +
		`"user":{"user_id":123456789,"name":"Пользователь"}}}`)
	viaWebhook, err := maxapi.DecodeUpdate(raw)
	if err != nil {
		t.Fatalf("DecodeUpdate(): %v", err)
	}
	if viaWebhook.ActorUserID() != update.ActorUserID() ||
		viaWebhook.IdempotencyKey() != update.IdempotencyKey() {
		t.Fatalf("разбор разошёлся: опрос %+v, webhook %+v", update, viaWebhook)
	}
}

// TestUndecodableUpdateIsSkippedNotFatal.
//
// Одно испорченное событие не должно останавливать поток. Вернуть здесь ошибку
// значило бы не продвинуть marker, получить ту же пачку снова — и встать
// навсегда. Webhook в том же случае отвечает 200 и отбрасывает тело; здесь
// поведение такое же.
func TestUndecodableUpdateIsSkippedNotFatal(t *testing.T) {
	const body = `{
		"updates": [
			{"update_type": "bot_started", "timestamp": 1700000000000, "user": {"user_id": 1}},
			{"no_update_type_at_all": true},
			{"update_type": "bot_started", "timestamp": 1700000000001, "user": {"user_id": 2}}
		],
		"marker": 3
	}`

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))

	batch, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{})
	if err != nil {
		t.Fatalf("GetUpdates() вернул ошибку из-за одного испорченного события: %v", err)
	}

	if len(batch.Updates) != 2 {
		t.Fatalf("разобранных событий: %d, ожидалось 2", len(batch.Updates))
	}
	if batch.Skipped != 1 {
		t.Fatalf("Skipped = %d, ожидалось 1", batch.Skipped)
	}
	if batch.Marker != 3 {
		t.Fatalf("marker = %d, ожидался 3: поток не должен застревать", batch.Marker)
	}
}

// TestUnknownUpdateTypeIsNotSkipped — нераспознанный ТИП события разбирается
// успешно и отбрасывается уже в BotService. Это разные вещи: MAX может
// добавить новый тип в любой момент, и это не повод считать ответ испорченным.
func TestUnknownUpdateTypeIsNotSkipped(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"updates":[{"update_type":"something_new_in_2027","timestamp":1}],"marker":1}`))
	}))

	batch, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{})
	if err != nil {
		t.Fatalf("GetUpdates(): %v", err)
	}
	if len(batch.Updates) != 1 || batch.Skipped != 0 {
		t.Fatalf("событий %d, пропущено %d — ожидалось 1 и 0", len(batch.Updates), batch.Skipped)
	}
}

func TestGetUpdatesUnauthorizedIsPermanent(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized"}`))
	}))

	_, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{})
	if !errors.Is(err, maxapi.ErrUnauthorized) {
		t.Fatalf("error = %v, ожидался ErrUnauthorized", err)
	}
	if maxapi.IsTemporary(err) {
		t.Fatal("401 помечен как временный: повторять его бессмысленно")
	}
}

func TestGetUpdatesServerErrorIsTemporary(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable} {
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))

		_, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{})
		if err == nil {
			t.Fatalf("HTTP %d: ошибки нет", status)
		}
		if !maxapi.IsTemporary(err) {
			t.Fatalf("HTTP %d не помечен как временный: %v", status, err)
		}
	}
}

func TestGetUpdatesBadRequestIsPermanent(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid.query.limit"}`))
	}))

	_, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{})
	if err == nil {
		t.Fatal("ошибки нет")
	}
	if maxapi.IsTemporary(err) {
		t.Fatalf("400 помечен как временный: повтор даст тот же ответ (%v)", err)
	}
}

// TestGetUpdatesRespectsContextCancellation — без этого остановка процесса
// ждала бы конца удержания соединения, до полутора минут.
func TestGetUpdatesRespectsContextCancellation(t *testing.T) {
	release := make(chan struct{})
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"updates":[],"marker":0}`))
	}))
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.GetUpdates(ctx, maxapi.GetUpdatesRequest{Timeout: 90})
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("GetUpdates() вернул nil после отмены контекста")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetUpdates() не отреагировал на отмену контекста")
	}
}

// TestListSubscriptions фиксирует форму второго самостоятельного запроса.
func TestListSubscriptions(t *testing.T) {
	var path string

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"subscriptions":[
			{"url":"https://example.ru/webhooks/max","update_types":["message_callback"],"version":"0.0.1"}
		]}`))
	}))

	subscriptions, err := client.ListSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("ListSubscriptions(): %v", err)
	}

	if path != "/subscriptions" {
		t.Errorf("path = %s, ожидался /subscriptions", path)
	}
	if len(subscriptions) != 1 {
		t.Fatalf("подписок: %d, ожидалась 1", len(subscriptions))
	}
	if subscriptions[0].URL != "https://example.ru/webhooks/max" {
		t.Errorf("url = %q", subscriptions[0].URL)
	}
	if len(subscriptions[0].UpdateTypes) != 1 || subscriptions[0].UpdateTypes[0] != "message_callback" {
		t.Errorf("update_types = %v", subscriptions[0].UpdateTypes)
	}
}

func TestListSubscriptionsEmpty(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"subscriptions":[]}`))
	}))

	subscriptions, err := client.ListSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("ListSubscriptions(): %v", err)
	}
	if len(subscriptions) != 0 {
		t.Fatalf("подписок: %d, ожидалось 0", len(subscriptions))
	}
}

// TestPollingResponseSizeIsBounded — ответ читается с потолком, иначе
// испорченный или враждебный ответ съел бы память процесса.
func TestPollingResponseSizeIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Заведомо больше потолка.
		chunk := strings.Repeat("a", 1<<20)
		_, _ = w.Write([]byte(`{"updates":[],"marker":0,"junk":"`))
		for i := 0; i < 12; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	t.Cleanup(server.Close)

	client, err := New(Config{Token: "test-token", BaseURL: server.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	// Тело обрезано на потолке, поэтому JSON не разберётся — и это ожидаемый
	// исход: важно, что процесс не прочитал двенадцать мегабайт в память и не
	// притворился, что всё в порядке.
	if _, err := client.GetUpdates(context.Background(), maxapi.GetUpdatesRequest{}); err == nil {
		t.Fatal("ответ размером больше потолка разобрался без ошибки")
	}
}
