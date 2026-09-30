// Package maxapi is the port through which the bot talks to MAX.
//
// The MAX SDK is an implementation detail of internal/maxapi/real. No other
// package in this project imports it, so swapping the SDK, pinning a different
// version, or hand-rolling the HTTP calls stays a change confined to one
// directory.
package maxapi

import (
	"context"
	"errors"
	"fmt"

	"hackatonBotMAX/internal/domain"
)

// BotInfo is the neutral view of "who am I" as reported by MAX.
type BotInfo struct {
	UserID    int64
	Name      string
	Username  string
	IsBot     bool
	RawSource string
}

// SendMessageRequest asks MAX to deliver a rendered message to a user.
//
// UserID addresses a private dialog with that MAX user, which is the only
// delivery mode this bot needs. ChatID is carried for completeness and is used
// when non-zero.
type SendMessageRequest struct {
	UserID  int64
	ChatID  int64
	Message domain.Message
}

// SentMessage is the result of a successful send.
type SentMessage struct {
	// MessageID is the MAX "mid" of the delivered message.
	MessageID string
	// ChatID is the chat the message landed in.
	ChatID int64
	// UserID is the recipient.
	UserID int64
}

// EditMessageRequest replaces the body of an already-sent message.
type EditMessageRequest struct {
	MessageID string
	Message   domain.Message
}

// AnswerCallbackRequest answers a button press.
//
// MAX requires an answer for every callback: without one the client keeps
// showing a spinner on the button. Setting Message replaces the original
// message (this is how stale action buttons are removed); Notification shows a
// transient toast to the user.
type AnswerCallbackRequest struct {
	CallbackID string
	// Notification is a short toast. Empty means no toast.
	Notification string
	// Message, when non-nil, replaces the message the button belonged to.
	Message *domain.Message
}

// Client is everything the bot needs from MAX.
//
// Implementations: internal/maxapi/real (the MAX Bot API) and
// internal/maxapi/mock (in-memory, used by MAX_MODE=mock and by every test).
type Client interface {
	// GetMe returns the bot's own profile. It doubles as a liveness probe
	// for the token and is what readiness uses in real mode.
	GetMe(ctx context.Context) (*BotInfo, error)
	// SendMessage delivers a rendered message.
	SendMessage(ctx context.Context, req SendMessageRequest) (*SentMessage, error)
	// EditMessage rewrites an already-delivered message.
	EditMessage(ctx context.Context, req EditMessageRequest) error
	// AnswerCallback acknowledges a button press.
	AnswerCallback(ctx context.Context, req AnswerCallbackRequest) error

	// GetUpdates забирает накопленные события методом long polling.
	//
	// Альтернатива webhook, а не дополнение к нему: в схеме MAX прямо
	// сказано, что метод предназначен для бота, НЕ подписанного на webhook.
	// Нужен там, где у машины нет публичного адреса — типичная ситуация при
	// локальной разработке за NAT.
	GetUpdates(ctx context.Context, req GetUpdatesRequest) (*UpdateBatch, error)

	// ListSubscriptions возвращает текущие подписки на webhook.
	//
	// В режиме polling используется как предупреждение при старте: если
	// подписка существует, события уйдут на неё, а опрос вернёт пустоту, и
	// причина этого совершенно неочевидна.
	ListSubscriptions(ctx context.Context) ([]Subscription, error)

	// Mode names the implementation ("mock" or "real") for logs and /ready.
	Mode() string
}

// GetUpdatesRequest — параметры одного опроса.
//
// Диапазоны заданы схемой MAX: limit 1..1000 (по умолчанию 100),
// timeout 0..90 секунд (по умолчанию 30).
type GetUpdatesRequest struct {
	// Marker — позиция в потоке событий. Ноль означает «с последнего
	// зафиксированного места»: при первом запросе параметр не передаётся.
	//
	// Важная деталь семантики MAX: передача marker ФИКСИРУЕТ все предыдущие
	// события. То есть подтверждение обработки происходит самим фактом
	// следующего запроса, а не отдельным вызовом.
	Marker int64
	// Limit — максимум событий за один ответ.
	Limit int
	// Timeout — сколько секунд сервер удерживает соединение, ожидая событий.
	// Это и есть «long» в long polling: пустой ответ приходит не сразу.
	//
	// Ноль означает «не задано» и заменяется значением по умолчанию (30), а
	// не «не удерживать». Схема MAX допускает ноль, но отличить осознанный
	// ноль от незаполненного поля в Go нечем, а цена ошибки несимметрична:
	// непрерывный обстрел MAX запросами против лишних тридцати секунд.
	Timeout int
	// Types ограничивает набор типов. Пусто — все.
	Types []string
}

// UpdateBatch — результат одного опроса.
type UpdateBatch struct {
	Updates []Update
	// Marker указывает на следующее ожидаемое событие. Его нужно передать в
	// следующий запрос.
	Marker int64
	// Skipped — сколько событий в ответе не удалось разобрать.
	//
	// Нераспознанное событие не считается ошибкой опроса, ровно как и в
	// webhook: там тело, которое не разбирается, подтверждается кодом 200 и
	// отбрасывается, потому что повторная доставка того же тела ничего не
	// изменит. Здесь — то же самое, иначе одно испорченное событие навсегда
	// заблокировало бы поток: marker не продвинулся бы, и следующий запрос
	// вернул бы ту же пачку.
	Skipped int
}

// Subscription — зарегистрированный адрес webhook.
type Subscription struct {
	URL         string
	UpdateTypes []string
	Version     string
}

// Error is a transport-level failure talking to MAX.
//
// Temporary distinguishes "retrying later could work" (network blip, 5xx, 429)
// from "this will never work" (401, malformed request). The application layer
// uses it to choose the message shown to the user.
type Error struct {
	Op         string
	StatusCode int
	Code       string
	Message    string
	Temporary  bool
	Err        error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("max %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("max %s: status=%d code=%s message=%s", e.Op, e.StatusCode, e.Code, e.Message)
}

// Unwrap exposes the underlying cause to errors.Is/As.
func (e *Error) Unwrap() error { return e.Err }

// IsTemporary reports whether err is a MAX error worth retrying.
func IsTemporary(err error) bool {
	var maxErr *Error
	if errors.As(err, &maxErr) {
		return maxErr.Temporary
	}
	return false
}

// ErrUnauthorized marks an invalid or revoked bot token.
var ErrUnauthorized = errors.New("max: unauthorized (check MAX_BOT_TOKEN)")

// ErrRecipientUnavailable marks a message MAX refused to deliver to this user:
// they stopped or blocked the bot, or never opened a dialog with it.
//
// It is permanent for this message. Retrying will not help until the user
// opens the dialog again, and the caller should treat it as "the bot cannot
// reach this person", not as "MAX is down".
var ErrRecipientUnavailable = errors.New("max: recipient unavailable (the user stopped the bot or has no dialog with it)")
