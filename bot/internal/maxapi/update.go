package maxapi

import (
	"encoding/json"
	"fmt"
	"time"
)

// UpdateType is a MAX webhook event type.
//
// Values mirror the MAX API (see the SDK's model.UpdateType). The bot handles
// three of them and must not crash on the rest: MAX may add event types, and a
// subscription may deliver more than was asked for.
type UpdateType string

const (
	// UpdateBotStarted fires when a user opens a dialog with the bot.
	UpdateBotStarted UpdateType = "bot_started"
	// UpdateMessageCreated fires on an incoming text message.
	UpdateMessageCreated UpdateType = "message_created"
	// UpdateMessageCallback fires when an inline button is pressed.
	UpdateMessageCallback UpdateType = "message_callback"
	// UpdateBotStopped fires when the user stops (blocks) the bot.
	UpdateBotStopped UpdateType = "bot_stopped"
	// UpdateDialogRemoved fires when the user deletes the dialog.
	UpdateDialogRemoved UpdateType = "dialog_removed"
)

// HandledUpdateTypes lists the update types this bot subscribes to and acts
// on. It is what MAX_SETUP.md tells you to pass to POST /subscriptions.
func HandledUpdateTypes() []string {
	return []string{
		string(UpdateBotStarted),
		string(UpdateMessageCreated),
		string(UpdateMessageCallback),
		string(UpdateBotStopped),
		string(UpdateDialogRemoved),
	}
}

// Update is the decoded MAX webhook payload, limited to the fields the bot
// reads.
//
// Unknown fields are ignored by encoding/json, so a MAX-side addition cannot
// break decoding. Unknown update types decode into a valid Update with an
// unrecognised Type, which the webhook handler acknowledges and drops.
type Update struct {
	Type      UpdateType `json:"update_type"`
	Timestamp int64      `json:"timestamp"`
	ChatID    int64      `json:"chat_id"`
	// UserID is populated on top-level events such as bot_started.
	UserID     int64           `json:"user_id"`
	UserLocale string          `json:"user_locale"`
	User       *UpdateUser     `json:"user,omitempty"`
	Message    *UpdateMessage  `json:"message,omitempty"`
	Callback   *UpdateCallback `json:"callback,omitempty"`
	// Payload carries the deep-link start parameter on bot_started.
	Payload string `json:"payload,omitempty"`
}

// UpdateUser is a MAX user as it appears in an update.
type UpdateUser struct {
	UserID    int64  `json:"user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	IsBot     bool   `json:"is_bot"`
}

// UpdateMessage is the message an update refers to.
type UpdateMessage struct {
	Sender    UpdateUser        `json:"sender"`
	Recipient UpdateRecipient   `json:"recipient"`
	Timestamp int64             `json:"timestamp"`
	Body      UpdateMessageBody `json:"body"`
}

// UpdateRecipient identifies where a message was delivered.
type UpdateRecipient struct {
	ChatID   int64  `json:"chat_id"`
	ChatType string `json:"chat_type"`
	UserID   int64  `json:"user_id"`
}

// UpdateMessageBody is the content of a message.
type UpdateMessageBody struct {
	MID  string `json:"mid"`
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// UpdateCallback describes a pressed inline button.
//
// User here is the person who actually pressed the button. On a
// message_callback update, message.recipient.user_id is the *dialog
// recipient*, which is not necessarily the presser. The bot therefore takes
// identity from callback.user.user_id and nowhere else.
type UpdateCallback struct {
	Timestamp  int64      `json:"timestamp"`
	CallbackID string     `json:"callback_id"`
	Payload    string     `json:"payload"`
	User       UpdateUser `json:"user"`
}

// DecodeUpdate parses a raw MAX webhook body.
//
// It fails only on syntactically invalid JSON or a missing update_type.
// Anything else — including unknown event types — decodes successfully so the
// caller can acknowledge and drop it.
func DecodeUpdate(raw []byte) (Update, error) {
	var update Update
	if err := json.Unmarshal(raw, &update); err != nil {
		return Update{}, fmt.Errorf("decode max update: %w", err)
	}
	if update.Type == "" {
		return Update{}, fmt.Errorf("decode max update: update_type is missing")
	}
	return update, nil
}

// ActorUserID returns the MAX user id to attribute this update to.
//
// This is the single trusted source of user identity for the whole webhook
// path; the callback payload is never consulted for it.
func (u Update) ActorUserID() int64 {
	switch u.Type {
	case UpdateMessageCallback:
		if u.Callback != nil && u.Callback.User.UserID != 0 {
			return u.Callback.User.UserID
		}
		// Fall back to the dialog recipient: in a private dialog with the
		// bot the two coincide, and having an id beats having none.
		if u.Message != nil && u.Message.Recipient.UserID != 0 {
			return u.Message.Recipient.UserID
		}
	case UpdateMessageCreated:
		if u.Message != nil && u.Message.Sender.UserID != 0 {
			return u.Message.Sender.UserID
		}
	default:
		if u.User != nil && u.User.UserID != 0 {
			return u.User.UserID
		}
	}
	if u.UserID != 0 {
		return u.UserID
	}
	if u.User != nil {
		return u.User.UserID
	}
	return 0
}

// DialogChatID returns the chat to reply in, or 0 when unknown.
func (u Update) DialogChatID() int64 {
	if u.ChatID != 0 {
		return u.ChatID
	}
	if u.Message != nil && u.Message.Recipient.ChatID != 0 {
		return u.Message.Recipient.ChatID
	}
	return 0
}

// MessageID returns the mid of the message this update refers to, or "".
func (u Update) MessageID() string {
	if u.Message != nil {
		return u.Message.Body.MID
	}
	return ""
}

// MessageText returns the text of the message this update refers to, or "".
func (u Update) MessageText() string {
	if u.Message != nil {
		return u.Message.Body.Text
	}
	return ""
}

// OccurredAt converts the MAX millisecond timestamp to a time.Time.
func (u Update) OccurredAt() time.Time {
	if u.Timestamp == 0 {
		return time.Time{}
	}
	return time.UnixMilli(u.Timestamp)
}

// IdempotencyKey derives a stable key identifying this delivery.
//
// MAX may redeliver a webhook (for example when our response was slow), and
// processing a confirmation twice would send the user two answers. A callback
// id is globally unique and is the natural key; other events fall back to a
// composite of type, actor and timestamp.
//
// Returns "" when no stable key can be derived, which the caller treats as
// "cannot deduplicate, process anyway".
func (u Update) IdempotencyKey() string {
	switch u.Type {
	case UpdateMessageCallback:
		if u.Callback != nil && u.Callback.CallbackID != "" {
			return "cb:" + u.Callback.CallbackID
		}
	case UpdateMessageCreated:
		if mid := u.MessageID(); mid != "" {
			return "msg:" + mid
		}
	}
	if u.Timestamp != 0 {
		return fmt.Sprintf("%s:%d:%d", u.Type, u.ActorUserID(), u.Timestamp)
	}
	return ""
}
