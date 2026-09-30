package real

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hackatonBotMAX/internal/maxapi"
)

// Long polling и чтение подписок.
//
// Здесь HTTP-вызовы выполняются напрямую, а не через SDK, и это осознанное
// решение, а не обход неудобства.
//
// У SDK есть Subscriptions.GetUpdates, но он возвращает уже разобранный
// model.Update — собственное представление SDK. Чтобы использовать его,
// пришлось бы написать второе отображение model.Update → maxapi.Update рядом
// с существующим разбором JSON из webhook. Две независимые схемы разбора
// одного и того же события неизбежно разойдутся, и разойдутся тихо: webhook
// и polling начнут вести себя по-разному в каком-нибудь углу, а заметит это
// пользователь, у которого не сработала кнопка.
//
// Прямой вызов отдаёт сырой JSON, который проходит через тот же
// maxapi.DecodeUpdate, что и тело webhook. Один путь разбора — одинаковое
// поведение в обоих режимах по построению, а не по договорённости.
//
// Цена решения: мы сами формируем два запроса и зависим от их формы. Формы
// зафиксированы в schema.yaml и продублированы в комментариях ниже.

const (
	// Границы из schema.yaml, параметры GET /updates.
	maxUpdatesLimit   = 1000
	maxUpdatesTimeout = 90

	defaultUpdatesLimit   = 100
	defaultUpdatesTimeout = 30

	// Потолок на размер ответа: 1000 событий — это заметный объём, но не
	// безграничный. Защищает от испорченного или враждебного ответа.
	maxUpdatesBody = 8 << 20
)

// GetUpdates реализует maxapi.Client.
//
//	GET /updates?marker=&limit=&timeout=&types=
//	→ {"updates": [...], "marker": 123}
func (c *Client) GetUpdates(ctx context.Context, req maxapi.GetUpdatesRequest) (*maxapi.UpdateBatch, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultUpdatesLimit
	}
	if limit > maxUpdatesLimit {
		limit = maxUpdatesLimit
	}

	// Ноль трактуется как «не задано», а не как «не удерживать соединение».
	//
	// Схема MAX допускает timeout=0, но у нулевого значения в Go нет способа
	// отличить «осознанно ноль» от «поле не заполнили», а цена ошибки
	// несимметрична: незаполненное поле превратило бы цикл опроса в
	// непрерывный обстрел MAX запросами. Один смысл на значение надёжнее.
	timeoutSec := req.Timeout
	if timeoutSec <= 0 {
		timeoutSec = defaultUpdatesTimeout
	}
	if timeoutSec > maxUpdatesTimeout {
		timeoutSec = maxUpdatesTimeout
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	query.Set("timeout", strconv.Itoa(timeoutSec))
	// Marker не передаётся при первом запросе: по схеме MAX это означает
	// «отдай всё, что накопилось с последней фиксации». Передать ноль явно
	// было бы не тем же самым.
	if req.Marker > 0 {
		query.Set("marker", strconv.FormatInt(req.Marker, 10))
	}
	if len(req.Types) > 0 {
		query.Set("types", strings.Join(req.Types, ","))
	}

	// Клиентский таймаут должен быть заметно больше серверного, иначе мы
	// будем обрывать соединение ровно тогда, когда сервер держит его штатно,
	// ожидая событий. Запас в 15 секунд покрывает и сеть, и разброс.
	callTimeout := time.Duration(timeoutSec+15) * time.Second

	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	body, err := c.rawGet(callCtx, "/updates", query, callTimeout)
	if err != nil {
		return nil, translateError("get_updates", err)
	}

	var parsed struct {
		Updates []json.RawMessage `json:"updates"`
		Marker  int64             `json:"marker"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &maxapi.Error{
			Op:      "get_updates",
			Err:     err,
			Message: "ответ MAX не разбирается как список событий",
		}
	}

	batch := &maxapi.UpdateBatch{Marker: parsed.Marker}
	for _, raw := range parsed.Updates {
		// Тот же разбор, что и для webhook. Событие, которое там будет
		// отброшено как нераспознанное, здесь ведёт себя ровно так же.
		update, err := maxapi.DecodeUpdate(raw)
		if err != nil {
			// Одно неразбираемое событие не роняет пачку и не считается
			// ошибкой опроса. Вернуть здесь ошибку значило бы не продвинуть
			// marker, получить ту же пачку снова — и встать навсегда.
			batch.Skipped++
			continue
		}
		batch.Updates = append(batch.Updates, update)
	}

	return batch, nil
}

// ListSubscriptions реализует maxapi.Client.
//
//	GET /subscriptions → {"subscriptions": [{"url": ..., "update_types": [...]}]}
func (c *Client) ListSubscriptions(ctx context.Context) ([]maxapi.Subscription, error) {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	body, err := c.rawGet(callCtx, "/subscriptions", nil, c.timeout)
	if err != nil {
		return nil, translateError("list_subscriptions", err)
	}

	var parsed struct {
		Subscriptions []struct {
			URL         string   `json:"url"`
			UpdateTypes []string `json:"update_types"`
			Version     string   `json:"version"`
		} `json:"subscriptions"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &maxapi.Error{
			Op:      "list_subscriptions",
			Err:     err,
			Message: "ответ MAX не разбирается как список подписок",
		}
	}

	out := make([]maxapi.Subscription, 0, len(parsed.Subscriptions))
	for _, item := range parsed.Subscriptions {
		out = append(out, maxapi.Subscription{
			URL:         item.URL,
			UpdateTypes: item.UpdateTypes,
			Version:     item.Version,
		})
	}
	return out, nil
}

// rawGet выполняет GET к API MAX и возвращает тело ответа.
//
// Авторизация здесь ровно та же, что использует SDK: заголовок Authorization
// с голым токеном, без префикса Bearer.
func (c *Client) rawGet(ctx context.Context, path string, query url.Values, timeout time.Duration) ([]byte, error) {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("не удалось построить запрос: %w", err)
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Accept", "application/json")

	// Long polling держит соединение дольше обычного вызова, поэтому здесь
	// нужен отдельный клиент с большим таймаутом: общий клиент настроен на
	// короткие запросы вроде SendMessage.
	client := *c.httpClient
	client.Timeout = timeout

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdatesBody))
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать ответ: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w (HTTP 401)", maxapi.ErrUnauthorized)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &maxapi.Error{
			Op:         "GET " + path,
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body[:min(len(body), 300)])),
			// 429 и 5xx имеет смысл повторить, остальное — нет.
			Temporary: resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
		}
	}

	return body, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
