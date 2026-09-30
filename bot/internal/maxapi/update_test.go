package maxapi

import (
	"testing"
	"time"
)

// These fixtures mirror the wire shapes the MAX SDK maps in its own
// updateRaw.FromRaw (v2.4.0). If MAX changes the format, this is the file that
// should fail first.

func TestDecodeMessageCallback(t *testing.T) {
	raw := []byte(`{
		"update_type": "message_callback",
		"timestamp": 1758556800000,
		"chat_id": 555,
		"callback": {
			"timestamp": 1758556800000,
			"callback_id": "cb-abc",
			"payload": "v1|confirm|registration_15|event_42",
			"user": {"user_id": 999, "first_name": "Пресser", "name": "Пресser"}
		},
		"message": {
			"sender": {"user_id": 1, "is_bot": true},
			"recipient": {"chat_id": 555, "user_id": 111, "chat_type": "dialog"},
			"body": {"mid": "mid-1", "seq": 7, "text": "Подтвердите участие"}
		}
	}`)

	update, err := DecodeUpdate(raw)
	if err != nil {
		t.Fatalf("DecodeUpdate(): %v", err)
	}
	if update.Type != UpdateMessageCallback {
		t.Errorf("type = %q", update.Type)
	}
	if update.Callback == nil || update.Callback.Payload != "v1|confirm|registration_15|event_42" {
		t.Errorf("callback = %+v", update.Callback)
	}

	// The property that matters: identity is the presser (callback.user),
	// not the dialog recipient (message.recipient.user_id).
	if got := update.ActorUserID(); got != 999 {
		t.Errorf("ActorUserID() = %d, want 999 (the button presser)", got)
	}
	if update.MessageID() != "mid-1" {
		t.Errorf("MessageID() = %q", update.MessageID())
	}
	if update.DialogChatID() != 555 {
		t.Errorf("DialogChatID() = %d", update.DialogChatID())
	}
	if !update.OccurredAt().Equal(time.UnixMilli(1758556800000)) {
		t.Errorf("OccurredAt() = %v", update.OccurredAt())
	}
}

func TestDecodeMessageCreated(t *testing.T) {
	raw := []byte(`{
		"update_type": "message_created",
		"timestamp": 1758556800000,
		"message": {
			"sender": {"user_id": 42, "first_name": "Алексей", "name": "Алексей"},
			"recipient": {"chat_id": 555, "user_id": 42, "chat_type": "dialog"},
			"body": {"mid": "mid-2", "seq": 8, "text": "когда там йога?"}
		}
	}`)

	update, err := DecodeUpdate(raw)
	if err != nil {
		t.Fatalf("DecodeUpdate(): %v", err)
	}
	if update.ActorUserID() != 42 {
		t.Errorf("ActorUserID() = %d, want the sender", update.ActorUserID())
	}
	if update.MessageText() != "когда там йога?" {
		t.Errorf("MessageText() = %q", update.MessageText())
	}
}

func TestDecodeBotStarted(t *testing.T) {
	raw := []byte(`{
		"update_type": "bot_started",
		"timestamp": 1758556800000,
		"chat_id": 555,
		"user": {"user_id": 123456789, "first_name": "Алексей", "name": "Алексей"},
		"payload": "deeplink_event_42"
	}`)

	update, err := DecodeUpdate(raw)
	if err != nil {
		t.Fatalf("DecodeUpdate(): %v", err)
	}
	if update.ActorUserID() != 123456789 {
		t.Errorf("ActorUserID() = %d", update.ActorUserID())
	}
	if update.Payload != "deeplink_event_42" {
		t.Errorf("Payload = %q", update.Payload)
	}
}

// TestDecodeIsResilient: MAX may add fields or send events we never asked
// for, and neither may break decoding.
func TestDecodeIsResilient(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "unknown update type", raw: `{"update_type":"invented_in_2027"}`},
		{name: "unknown extra fields", raw: `{"update_type":"bot_started","brand_new_field":{"a":1},"user":{"user_id":1}}`},
		{name: "null nested objects", raw: `{"update_type":"message_callback","message":null,"callback":null}`},
		{name: "minimal body", raw: `{"update_type":"bot_stopped"}`},
		{name: "missing update_type", raw: `{"timestamp":1}`, wantErr: true},
		{name: "empty update_type", raw: `{"update_type":""}`, wantErr: true},
		{name: "not an object", raw: `[]`, wantErr: true},
		{name: "malformed json", raw: `{`, wantErr: true},
		{name: "json null", raw: `null`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			update, err := DecodeUpdate([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeUpdate(): %v", err)
			}
			// The accessors must be safe on a sparsely populated update.
			_ = update.ActorUserID()
			_ = update.DialogChatID()
			_ = update.MessageID()
			_ = update.MessageText()
			_ = update.OccurredAt()
			_ = update.IdempotencyKey()
		})
	}
}

func TestIdempotencyKey(t *testing.T) {
	tests := []struct {
		name   string
		update Update
		want   string
	}{
		{
			name: "callback id is the natural key",
			update: Update{
				Type:     UpdateMessageCallback,
				Callback: &UpdateCallback{CallbackID: "cb-abc"},
			},
			want: "cb:cb-abc",
		},
		{
			name: "message mid",
			update: Update{
				Type:    UpdateMessageCreated,
				Message: &UpdateMessage{Body: UpdateMessageBody{MID: "mid-1"}},
			},
			want: "msg:mid-1",
		},
		{
			name:   "fallback composite",
			update: Update{Type: UpdateBotStarted, Timestamp: 1000, UserID: 42},
			want:   "bot_started:42:1000",
		},
		{
			name:   "nothing stable to key on",
			update: Update{Type: UpdateBotStarted},
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.update.IdempotencyKey(); got != tc.want {
				t.Errorf("IdempotencyKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandledUpdateTypes(t *testing.T) {
	types := HandledUpdateTypes()
	want := map[string]bool{
		"bot_started": false, "message_created": false, "message_callback": false,
		"bot_stopped": false, "dialog_removed": false,
	}

	for _, updateType := range types {
		if _, known := want[updateType]; !known {
			t.Errorf("unexpected subscribed type %q", updateType)
		}
		want[updateType] = true
	}
	for updateType, seen := range want {
		if !seen {
			t.Errorf("type %q should be in the subscription list", updateType)
		}
	}
}
