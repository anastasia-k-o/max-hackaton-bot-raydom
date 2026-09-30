// Package mock implements maxapi.Client entirely in memory.
//
// It never opens a socket. Everything it is asked to send is appended to an
// in-process log that the /dev endpoints expose, which is what makes the whole
// notification -> button -> callback -> message-update loop demonstrable
// through Postman with no MAX token and no network.
//
// It is also the MAX client used by every automated test: the test suite never
// calls the real MAX API.
package mock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/maxapi"
)

// RecordKind distinguishes the operations recorded by the mock.
type RecordKind string

const (
	// KindSent is a message delivered via SendMessage.
	KindSent RecordKind = "sent"
	// KindEdited is a message rewritten via EditMessage.
	KindEdited RecordKind = "edited"
	// KindCallbackAnswer is an answer to a button press.
	KindCallbackAnswer RecordKind = "callback_answer"
)

// Record is one recorded MAX interaction, shaped for JSON inspection.
//
// The struct is deliberately flat and JSON-tagged: it is a debugging surface
// read by a human in Postman, not an internal model.
type Record struct {
	Seq       int64      `json:"seq"`
	Kind      RecordKind `json:"kind"`
	At        time.Time  `json:"at"`
	RequestID string     `json:"request_id,omitempty"`

	MessageID string `json:"message_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	ChatID    int64  `json:"chat_id,omitempty"`

	// CallbackID is set on KindCallbackAnswer.
	CallbackID string `json:"callback_id,omitempty"`
	// Notification is the toast shown to the user, if any.
	Notification string `json:"notification,omitempty"`

	Text     string           `json:"text,omitempty"`
	Format   string           `json:"format,omitempty"`
	Keyboard [][]RecordButton `json:"keyboard,omitempty"`
}

// RecordButton is a button as recorded for inspection.
type RecordButton struct {
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	URL     string `json:"url,omitempty"`
	Payload string `json:"payload,omitempty"`
}

// Client is the in-memory maxapi.Client.
//
// It is safe for concurrent use; the HTTP server calls it from many goroutines.
type Client struct {
	mu      sync.RWMutex
	records []Record
	seq     int64
	// maxRecords caps memory use. Older records are dropped first: this is a
	// development inspection buffer, not an audit log.
	maxRecords int

	// botInfo is what GetMe reports.
	botInfo maxapi.BotInfo

	// now is injectable so tests get deterministic timestamps.
	now func() time.Time

	// failNext, when set, makes the next call fail. Tests use it to exercise
	// the "MAX API unavailable" branch without a network.
	failNext error

	// pending — очередь событий для long polling.
	pending []maxapi.Update
	// subscriptions — то, что вернёт ListSubscriptions.
	subscriptions []maxapi.Subscription
}

// Option customises a mock client.
type Option func(*Client)

// WithClock overrides the time source.
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// WithMaxRecords overrides the retention cap.
func WithMaxRecords(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxRecords = n
		}
	}
}

// WithBotInfo overrides what GetMe returns.
func WithBotInfo(info maxapi.BotInfo) Option {
	return func(c *Client) { c.botInfo = info }
}

// New creates a mock MAX client.
func New(opts ...Option) *Client {
	client := &Client{
		records:    make([]Record, 0, 64),
		maxRecords: 500,
		now:        time.Now,
		botInfo: maxapi.BotInfo{
			UserID:    1,
			Name:      "Mock Afisha Bot",
			Username:  "mock_afisha_bot",
			IsBot:     true,
			RawSource: "mock",
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// Mode implements maxapi.Client.
func (c *Client) Mode() string { return "mock" }

// GetMe returns a synthetic bot profile. It never fails, which is what lets
// /ready report healthy in mock mode with no token configured.
func (c *Client) GetMe(ctx context.Context) (*maxapi.BotInfo, error) {
	if err := c.takeFailure(ctx, "get_me"); err != nil {
		return nil, err
	}
	info := c.botInfo
	return &info, nil
}

// SendMessage records an outbound message and returns a synthetic message id.
func (c *Client) SendMessage(ctx context.Context, req maxapi.SendMessageRequest) (*maxapi.SentMessage, error) {
	if err := c.takeFailure(ctx, "send_message"); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.seq++
	messageID := fmt.Sprintf("mock-mid-%d", c.seq)
	c.appendLocked(Record{
		Seq:       c.seq,
		Kind:      KindSent,
		At:        c.now().UTC(),
		RequestID: requestIDFrom(ctx),
		MessageID: messageID,
		UserID:    req.UserID,
		ChatID:    req.ChatID,
		Text:      req.Message.Text,
		Format:    string(req.Message.Format),
		Keyboard:  encodeKeyboard(req.Message.Keyboard),
	})

	return &maxapi.SentMessage{
		MessageID: messageID,
		ChatID:    req.ChatID,
		UserID:    req.UserID,
	}, nil
}

// EditMessage records a message rewrite.
func (c *Client) EditMessage(ctx context.Context, req maxapi.EditMessageRequest) error {
	if err := c.takeFailure(ctx, "edit_message"); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.seq++
	c.appendLocked(Record{
		Seq:       c.seq,
		Kind:      KindEdited,
		At:        c.now().UTC(),
		RequestID: requestIDFrom(ctx),
		MessageID: req.MessageID,
		Text:      req.Message.Text,
		Format:    string(req.Message.Format),
		Keyboard:  encodeKeyboard(req.Message.Keyboard),
	})
	return nil
}

// AnswerCallback records the answer to a button press, including the
// replacement message when one is supplied.
func (c *Client) AnswerCallback(ctx context.Context, req maxapi.AnswerCallbackRequest) error {
	if err := c.takeFailure(ctx, "answer_callback"); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.seq++
	record := Record{
		Seq:          c.seq,
		Kind:         KindCallbackAnswer,
		At:           c.now().UTC(),
		RequestID:    requestIDFrom(ctx),
		CallbackID:   req.CallbackID,
		Notification: req.Notification,
	}
	if req.Message != nil {
		record.Text = req.Message.Text
		record.Format = string(req.Message.Format)
		record.Keyboard = encodeKeyboard(req.Message.Keyboard)
	}
	c.appendLocked(record)
	return nil
}

// Records returns a copy of everything recorded so far, oldest first.
func (c *Client) Records() []Record {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]Record, len(c.records))
	copy(out, c.records)
	return out
}

// Reset clears the recorded history. Exposed through DELETE /dev/max/messages
// so a Postman run can start from a clean slate.
func (c *Client) Reset() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := len(c.records)
	c.records = c.records[:0]
	return n
}

// LastSent returns the most recent KindSent record, or false when there is
// none. Tests use it to assert on the message a notification produced.
func (c *Client) LastSent() (Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for i := len(c.records) - 1; i >= 0; i-- {
		if c.records[i].Kind == KindSent {
			return c.records[i], true
		}
	}
	return Record{}, false
}

// LastRecord returns the most recent record of any kind.
func (c *Client) LastRecord() (Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.records) == 0 {
		return Record{}, false
	}
	return c.records[len(c.records)-1], true
}

// FailNext makes the next operation return err, once.
//
// This is the seam for testing "MAX API unavailable" behaviour without a
// network or a fake HTTP server.
func (c *Client) FailNext(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failNext = err
}

// takeFailure consumes a queued failure and honours context cancellation.
func (c *Client) takeFailure(ctx context.Context, op string) error {
	if err := ctx.Err(); err != nil {
		return &maxapi.Error{Op: op, Temporary: true, Err: err}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		return err
	}
	return nil
}

// appendLocked adds a record, trimming the oldest when over the cap.
// The caller must hold c.mu.
func (c *Client) appendLocked(record Record) {
	c.records = append(c.records, record)
	if len(c.records) > c.maxRecords {
		overflow := len(c.records) - c.maxRecords
		c.records = append(c.records[:0], c.records[overflow:]...)
	}
}

func encodeKeyboard(keyboard *domain.Keyboard) [][]RecordButton {
	if keyboard.IsEmpty() {
		return nil
	}
	rows := make([][]RecordButton, 0, len(keyboard.Rows))
	for _, row := range keyboard.Rows {
		encoded := make([]RecordButton, 0, len(row))
		for _, button := range row {
			encoded = append(encoded, RecordButton{
				Kind:    string(button.Kind),
				Text:    button.Text,
				URL:     button.URL,
				Payload: button.Payload,
			})
		}
		rows = append(rows, encoded)
	}
	return rows
}

// --- long polling ---------------------------------------------------------
//
// MockMAX умеет отдавать заранее подготовленные события. Это позволяет
// проверить цикл опроса целиком — включая продвижение marker — не поднимая
// ни сети, ни подменного HTTP-сервера.

// EnqueueUpdates добавляет события в очередь выдачи.
//
// Каждое получает порядковый номер, и следующий за последним выданным
// становится новым marker — ровно так, как это описано в схеме MAX.
func (c *Client) EnqueueUpdates(updates ...maxapi.Update) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, updates...)
}

// GetUpdates реализует maxapi.Client.
//
// Отдаёт события, номер которых больше переданного marker. Пустая очередь —
// пустой ответ без ожидания: имитировать долгое удержание соединения смысла
// нет, а тесты от этого только замедлились бы.
func (c *Client) GetUpdates(ctx context.Context, req maxapi.GetUpdatesRequest) (*maxapi.UpdateBatch, error) {
	if err := c.takeFailure(ctx, "get_updates"); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}

	// marker указывает на СЛЕДУЮЩЕЕ ожидаемое событие, поэтому отсчёт идёт
	// от него, а не от него плюс один.
	from := int(req.Marker)
	if from < 0 || from > len(c.pending) {
		from = len(c.pending)
	}

	to := from + limit
	if to > len(c.pending) {
		to = len(c.pending)
	}

	batch := &maxapi.UpdateBatch{Marker: int64(to)}
	if from < to {
		batch.Updates = append(batch.Updates, c.pending[from:to]...)
	}
	return batch, nil
}

// ListSubscriptions реализует maxapi.Client.
//
// Возвращает то, что задали через SetSubscriptions: по умолчанию пусто, что
// для режима polling и является правильным состоянием.
func (c *Client) ListSubscriptions(ctx context.Context) ([]maxapi.Subscription, error) {
	if err := c.takeFailure(ctx, "list_subscriptions"); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]maxapi.Subscription, len(c.subscriptions))
	copy(out, c.subscriptions)
	return out, nil
}

// SetSubscriptions задаёт список подписок.
//
// Нужен, чтобы проверить предупреждение при старте в режиме polling: если
// webhook зарегистрирован, опрос событий не получит.
func (c *Client) SetSubscriptions(subscriptions ...maxapi.Subscription) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscriptions = append([]maxapi.Subscription(nil), subscriptions...)
}
