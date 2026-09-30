package real

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/maxapi"
)

// fakeMax answers /me with a fixed identity and records the body of every
// POST /messages, replying with messageStatus.
type fakeMax struct {
	messageStatus int
	lastBody      map[string]any
}

func (f *fakeMax) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/me":
		_, _ = io.WriteString(w, `{"user_id": 425847267, "first_name": "Бот", "username": "t645_hakaton_max_bot", "is_bot": true}`)
	case "/messages":
		f.lastBody = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&f.lastBody)
		if f.messageStatus != 0 && f.messageStatus != http.StatusOK {
			w.WriteHeader(f.messageStatus)
			_, _ = io.WriteString(w, `{"code": "some.code", "message": "refused"}`)
			return
		}
		_, _ = io.WriteString(w, `{"message":{"recipient":{"chat_id":1,"user_id":2},"body":{"mid":"mid-1"}}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// sentButtons returns the buttons of the single keyboard row in the last body.
func (f *fakeMax) sentButtons(t *testing.T) []map[string]any {
	t.Helper()
	attachments, _ := f.lastBody["attachments"].([]any)
	if len(attachments) == 0 {
		return nil
	}
	payload := attachments[0].(map[string]any)["payload"].(map[string]any)
	rows := payload["buttons"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	var out []map[string]any
	for _, b := range rows[0].([]any) {
		out = append(out, b.(map[string]any))
	}
	return out
}

func openAppMessage(fallbackURL string) domain.Message {
	return domain.Message{
		Text: "Вы записаны",
		Keyboard: &domain.Keyboard{Rows: []domain.ButtonRow{{
			{Kind: domain.ButtonOpenApp, Text: "Открыть афишу", Payload: "event_42", URL: fallbackURL},
		}}},
	}
}

// TestOpenAppButtonShape pins the request MAX gets once the bot knows who it
// is: an open_app button naming the bot by id and public name, with the event
// id as the start parameter. Both identifiers come from GetMe, so nothing
// about the bot has to be configured by hand.
func TestOpenAppButtonShape(t *testing.T) {
	max := &fakeMax{}
	client, _ := newTestClient(t, max)

	if _, err := client.GetMe(context.Background()); err != nil {
		t.Fatalf("GetMe(): %v", err)
	}
	if _, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
		UserID: 2, Message: openAppMessage("https://example.ru/app/event_42"),
	}); err != nil {
		t.Fatalf("SendMessage(): %v", err)
	}

	buttons := max.sentButtons(t)
	if len(buttons) != 1 {
		t.Fatalf("buttons = %v", buttons)
	}
	b := buttons[0]
	if b["type"] != "open_app" {
		t.Fatalf("type = %v, want open_app", b["type"])
	}
	if b["payload"] != "event_42" {
		t.Errorf("payload = %v, want event_42 (the mini app's start parameter)", b["payload"])
	}
	if b["web_app"] != "t645_hakaton_max_bot" {
		t.Errorf("web_app = %v, want the bot's username", b["web_app"])
	}
	if id, _ := b["contact_id"].(float64); int64(id) != 425847267 {
		t.Errorf("contact_id = %v, want the bot's user id", b["contact_id"])
	}
	if _, hasURL := b["url"]; hasURL {
		t.Errorf("open_app button carries a url: %v", b)
	}
}

// TestOpenAppDegradesToLinkBeforeGetMe: until the bot knows its own id it
// cannot name itself in an open_app button, so the fallback link is sent
// instead of a button MAX would reject.
func TestOpenAppDegradesToLinkBeforeGetMe(t *testing.T) {
	max := &fakeMax{}
	client, _ := newTestClient(t, max)

	if _, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
		UserID: 2, Message: openAppMessage("https://example.ru/app/event_42"),
	}); err != nil {
		t.Fatalf("SendMessage(): %v", err)
	}

	buttons := max.sentButtons(t)
	if len(buttons) != 1 || buttons[0]["type"] != "link" || buttons[0]["url"] != "https://example.ru/app/event_42" {
		t.Fatalf("buttons = %v, want one link to the fallback URL", buttons)
	}
}

// TestOpenAppWithoutFallbackIsDropped: no identity and no URL means there is
// nothing valid to send, and an empty row must not reach MAX either.
func TestOpenAppWithoutFallbackIsDropped(t *testing.T) {
	max := &fakeMax{}
	client, _ := newTestClient(t, max)

	if _, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
		UserID: 2, Message: openAppMessage(""),
	}); err != nil {
		t.Fatalf("SendMessage(): %v", err)
	}
	if attachments, _ := max.lastBody["attachments"].([]any); len(attachments) != 0 {
		t.Fatalf("attachments = %v, want none", attachments)
	}
}

// TestForbiddenMeansRecipientUnavailable: MAX documents 403 on /messages as
// "user suspended bot or it doesn't have access to chat". That must come out
// as ErrRecipientUnavailable and permanent, not as "MAX is down, retry".
func TestForbiddenMeansRecipientUnavailable(t *testing.T) {
	client, _ := newTestClient(t, &fakeMax{messageStatus: http.StatusForbidden})

	_, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
		UserID: 2, Message: domain.Message{Text: "hi"},
	})
	if !errors.Is(err, maxapi.ErrRecipientUnavailable) {
		t.Fatalf("error = %v, want ErrRecipientUnavailable", err)
	}
	if maxapi.IsTemporary(err) {
		t.Error("recipient unavailable is marked temporary; retrying cannot fix it")
	}
	var maxErr *maxapi.Error
	if !errors.As(err, &maxErr) || maxErr.StatusCode != http.StatusForbidden {
		t.Errorf("status code not carried: %#v", err)
	}
}

// TestOtherFailuresAreNotRecipientUnavailable: only 403 means "this user".
func TestOtherFailuresAreNotRecipientUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError} {
		client, _ := newTestClient(t, &fakeMax{messageStatus: status})
		_, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
			UserID: 2, Message: domain.Message{Text: "hi"},
		})
		if err == nil {
			t.Fatalf("HTTP %d: no error", status)
		}
		if errors.Is(err, maxapi.ErrRecipientUnavailable) {
			t.Errorf("HTTP %d reported as recipient unavailable", status)
		}
	}
}

// TestStatusRecorderIsTransparent: requests without a slot pass through, and
// a slot sees the status of its own request only.
func TestStatusRecorderIsTransparent(t *testing.T) {
	recorder := statusRecorder{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody}, nil
	})}

	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	resp, err := recorder.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusTeapot {
		t.Fatalf("pass-through broke: %v %v", resp, err)
	}

	ctx, slot := withStatusSlot(context.Background())
	if _, err := recorder.RoundTrip(req.WithContext(ctx)); err != nil {
		t.Fatal(err)
	}
	if slot.code() != http.StatusTeapot {
		t.Errorf("slot = %d, want %d", slot.code(), http.StatusTeapot)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
