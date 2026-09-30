package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/maxapi/mock"
	"hackatonBotMAX/internal/render"
)

func newTestBotService(t *testing.T) (*BotService, *mock.Client, *stub.Gateway) {
	t.Helper()

	maxClient := mock.New()
	gateway := stub.New()
	renderer := render.New(render.WithDefaultMiniAppURL("https://example.ru/app"))
	service := NewBotService(maxClient, gateway, renderer,
		idempotency.NewMemoryStore(time.Minute),
		WithMiniAppURL("https://example.ru/app"),
	)
	return service, maxClient, gateway
}

// callbackUpdate builds a message_callback update the way MAX sends one.
//
// Note the deliberate mismatch between callback.user.user_id (who pressed the
// button) and message.recipient.user_id: the tests use it to prove the service
// attributes the action to the presser.
func callbackUpdate(payload string, presserID, recipientID int64) maxapi.Update {
	return maxapi.Update{
		Type:      maxapi.UpdateMessageCallback,
		Timestamp: time.Now().UnixMilli(),
		ChatID:    555,
		Message: &maxapi.UpdateMessage{
			Recipient: maxapi.UpdateRecipient{ChatID: 555, UserID: recipientID, ChatType: "dialog"},
			Body:      maxapi.UpdateMessageBody{MID: "mid-1", Text: "Подтвердите участие"},
		},
		Callback: &maxapi.UpdateCallback{
			CallbackID: fmt.Sprintf("cb-%d-%s", presserID, payload),
			Payload:    payload,
			User:       maxapi.UpdateUser{UserID: presserID, Name: "Тестовый пользователь"},
		},
	}
}

func TestHandleCallbackDispatchesEveryAction(t *testing.T) {
	tests := []struct {
		name     string
		action   domain.ActionType
		wantKind stub.ActionKind
	}{
		{"confirm", domain.ActionConfirmRegistration, stub.KindConfirmRegistration},
		{"cancel", domain.ActionCancelRegistration, stub.KindCancelRegistration},
		{"accept waitlist", domain.ActionAcceptWaitlist, stub.KindAcceptWaitlist},
		{"decline waitlist", domain.ActionDeclineWaitlist, stub.KindDeclineWaitlist},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, maxClient, gateway := newTestBotService(t)

			payload := callback.MustEncode(tc.action, "registration_15", "event_42")
			update := callbackUpdate(payload, 123456789, 123456789)

			outcome, err := service.HandleUpdate(context.Background(), update)
			if err != nil {
				t.Fatalf("HandleUpdate(): %v", err)
			}
			if outcome.Result != ResultOK {
				t.Fatalf("outcome = %+v, want result ok", outcome)
			}
			if outcome.Action != tc.action {
				t.Errorf("outcome action = %q, want %q", outcome.Action, tc.action)
			}

			recorded, ok := gateway.LastAction()
			if !ok {
				t.Fatal("StubCoreGateway recorded no action")
			}
			if recorded.Kind != tc.wantKind {
				t.Errorf("recorded kind = %q, want %q", recorded.Kind, tc.wantKind)
			}
			if recorded.RegistrationID != "registration_15" {
				t.Errorf("recorded registration = %q", recorded.RegistrationID)
			}
			if recorded.EventID != "event_42" {
				t.Errorf("recorded event = %q", recorded.EventID)
			}
			if recorded.MaxUserID != 123456789 {
				t.Errorf("recorded max user = %d", recorded.MaxUserID)
			}

			answer, ok := maxClient.LastRecord()
			if !ok || answer.Kind != mock.KindCallbackAnswer {
				t.Fatalf("expected a callback answer, got %+v", answer)
			}
			if answer.Text == "" {
				t.Error("the answer should carry a replacement message")
			}
			// The replacement must not offer the action again.
			for _, row := range answer.Keyboard {
				for _, button := range row {
					if button.Kind == string(domain.ButtonCallback) {
						t.Errorf("replacement message still has an action button: %+v", button)
					}
				}
			}
		})
	}
}

// TestCallbackIdentityComesFromTheUpdateNotThePayload is the security property
// the spec calls out explicitly.
func TestCallbackIdentityComesFromTheUpdateNotThePayload(t *testing.T) {
	service, _, gateway := newTestBotService(t)

	const presser = int64(999)
	const otherUser = int64(111)

	payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
	update := callbackUpdate(payload, presser, otherUser)

	if _, err := service.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate(): %v", err)
	}

	recorded, ok := gateway.LastAction()
	if !ok {
		t.Fatal("no action recorded")
	}
	if recorded.MaxUserID != presser {
		t.Fatalf("action attributed to %d, want the button presser %d", recorded.MaxUserID, presser)
	}
}

// TestUnknownCallbackActionDoesNotPanic covers the acceptance criterion
// "неизвестный callback action не вызывает panic".
func TestUnknownCallbackActionDoesNotPanic(t *testing.T) {
	payloads := []string{
		"",
		"Приду",
		"v1|teleport|reg_1",
		"v2|confirm|reg_1",
		"v1|confirm",
		"v1|confirm|",
		"garbage",
		strings.Repeat("x", 4000),
		"v1|confirm|reg|1|extra",
	}

	for _, payload := range payloads {
		t.Run(shortName(payload), func(t *testing.T) {
			service, maxClient, gateway := newTestBotService(t)
			update := callbackUpdate(payload, 123456789, 123456789)

			outcome, err := service.HandleUpdate(context.Background(), update)
			if err != nil {
				t.Fatalf("HandleUpdate() should not fail on a bad payload: %v", err)
			}
			if outcome.Result != ResultBadPayload {
				t.Errorf("outcome = %+v, want bad_payload", outcome)
			}
			if len(gateway.Actions()) != 0 {
				t.Errorf("a bad payload must not reach the Core Gateway: %+v", gateway.Actions())
			}

			// The user must still be answered, or the button spins forever.
			answer, ok := maxClient.LastRecord()
			if !ok || answer.Kind != mock.KindCallbackAnswer {
				t.Fatal("a bad payload must still be answered")
			}
			if !strings.Contains(answer.Text, "Эта кнопка больше не работает") {
				t.Errorf("want the stale-button explanation, got:\n%s", answer.Text)
			}
		})
	}
}

// TestBusinessConflictRetiresButtons: a cancelled event or an expired offer is
// permanent, so the buttons go away.
func TestBusinessConflictRetiresButtons(t *testing.T) {
	tests := []struct {
		name         string
		cause        error
		wantContains string
	}{
		{"event cancelled", core.NewError(core.CodeEventCancelled, "x", 409, nil), "Мероприятие отменено организатором"},
		{"registration cancelled", core.NewError(core.CodeRegistrationCancelled, "x", 409, nil), "Эта запись уже отменена."},
		{"offer expired", core.NewError(core.CodeOfferExpired, "x", 410, nil), "Срок предложения истёк"},
		{"not found", core.NewError(core.CodeNotFound, "x", 404, nil), "Не удалось найти эту запись"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, maxClient, gateway := newTestBotService(t)
			gateway.FailNext(tc.cause)

			payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
			outcome, err := service.HandleUpdate(context.Background(), callbackUpdate(payload, 1, 1))
			if err != nil {
				t.Fatalf("a business conflict is not a transport failure: %v", err)
			}
			if outcome.Result != ResultBusinessError {
				t.Errorf("outcome = %+v, want business_error", outcome)
			}

			answer, _ := maxClient.LastRecord()
			if answer.Text == "" {
				t.Fatal("expected a replacement message")
			}
			if !strings.Contains(answer.Text, tc.wantContains) {
				t.Errorf("want %q in:\n%s", tc.wantContains, answer.Text)
			}
			for _, row := range answer.Keyboard {
				for _, button := range row {
					if button.Kind == string(domain.ButtonCallback) {
						t.Error("a permanent failure should retire the action buttons")
					}
				}
			}
		})
	}
}

// TestBackendUnavailableKeepsButtons: a temporary failure leaves the buttons
// in place so the user can simply press again.
func TestBackendUnavailableKeepsButtons(t *testing.T) {
	service, maxClient, gateway := newTestBotService(t)
	gateway.FailNext(core.NewError(core.CodeUnavailable, "connection refused", 0, errors.New("dial tcp: refused")))

	payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
	outcome, err := service.HandleUpdate(context.Background(), callbackUpdate(payload, 1, 1))
	if err != nil {
		t.Fatalf("HandleUpdate(): %v", err)
	}
	if outcome.Result != ResultUnavailable {
		t.Errorf("outcome = %+v, want unavailable", outcome)
	}

	answer, _ := maxClient.LastRecord()
	if answer.Kind != mock.KindCallbackAnswer {
		t.Fatal("the user must still be answered")
	}
	if answer.Text != "" {
		t.Error("a temporary failure should leave the original message and its buttons intact")
	}
	if answer.Notification != "Сервис временно недоступен" {
		t.Errorf("notification = %q", answer.Notification)
	}
	// And nothing technical leaked into it.
	if strings.Contains(answer.Notification, "dial tcp") {
		t.Error("the toast leaked a transport error")
	}
}

func TestBotStartedSendsGreeting(t *testing.T) {
	service, maxClient, _ := newTestBotService(t)

	update := maxapi.Update{
		Type:      maxapi.UpdateBotStarted,
		Timestamp: time.Now().UnixMilli(),
		ChatID:    555,
		UserID:    123456789,
		User:      &maxapi.UpdateUser{UserID: 123456789, Name: "Тест"},
	}

	outcome, err := service.HandleUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("HandleUpdate(): %v", err)
	}
	if !outcome.Handled {
		t.Fatal("bot_started should be handled")
	}

	sent, ok := maxClient.LastSent()
	if !ok {
		t.Fatal("no greeting sent")
	}
	if sent.UserID != 123456789 {
		t.Errorf("greeting sent to %d", sent.UserID)
	}
	if !strings.HasPrefix(sent.Text, "Привет!") {
		t.Errorf("unexpected greeting:\n%s", sent.Text)
	}
}

func TestMessageCreatedFallbacks(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantContains string
	}{
		{"free text", "когда там йога?", "Для выбора мероприятий откройте мини-приложение."},
		{"start command", "/start", "Привет!"},
		{"help command", "/help", "Я присылаю напоминания"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, maxClient, _ := newTestBotService(t)

			update := maxapi.Update{
				Type:      maxapi.UpdateMessageCreated,
				Timestamp: time.Now().UnixMilli(),
				Message: &maxapi.UpdateMessage{
					Sender:    maxapi.UpdateUser{UserID: 42},
					Recipient: maxapi.UpdateRecipient{ChatID: 555, UserID: 42},
					Body:      maxapi.UpdateMessageBody{MID: "mid-" + tc.name, Text: tc.text},
				},
			}

			if _, err := service.HandleUpdate(context.Background(), update); err != nil {
				t.Fatalf("HandleUpdate(): %v", err)
			}

			sent, ok := maxClient.LastSent()
			if !ok {
				t.Fatal("no reply sent")
			}
			if !strings.Contains(sent.Text, tc.wantContains) {
				t.Errorf("want %q in:\n%s", tc.wantContains, sent.Text)
			}
		})
	}
}

func TestBotOwnMessagesAreIgnored(t *testing.T) {
	service, maxClient, _ := newTestBotService(t)

	update := maxapi.Update{
		Type:      maxapi.UpdateMessageCreated,
		Timestamp: time.Now().UnixMilli(),
		Message: &maxapi.UpdateMessage{
			Sender:    maxapi.UpdateUser{UserID: 1, IsBot: true},
			Recipient: maxapi.UpdateRecipient{ChatID: 555, UserID: 42},
			Body:      maxapi.UpdateMessageBody{MID: "mid-bot", Text: "hello"},
		},
	}

	outcome, err := service.HandleUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("HandleUpdate(): %v", err)
	}
	if outcome.Handled {
		t.Error("the bot must not reply to its own messages")
	}
	if len(maxClient.Records()) != 0 {
		t.Error("nothing should have been sent")
	}
}

// TestUnknownUpdateTypesAreAcknowledgedNotFailed: MAX can deliver event types
// the bot never asked for, and failing them would produce a retry loop.
func TestUnknownUpdateTypesAreAcknowledgedNotFailed(t *testing.T) {
	unknownTypes := []string{
		"dialog_muted", "user_added", "chat_title_changed",
		"message_removed", "dialog_cleared", "something_invented_in_2027",
	}

	for _, updateType := range unknownTypes {
		t.Run(updateType, func(t *testing.T) {
			service, maxClient, gateway := newTestBotService(t)

			update := maxapi.Update{
				Type:      maxapi.UpdateType(updateType),
				Timestamp: time.Now().UnixMilli(),
				UserID:    42,
			}

			outcome, err := service.HandleUpdate(context.Background(), update)
			if err != nil {
				t.Fatalf("an unhandled update type must not produce an error: %v", err)
			}
			if outcome.Handled {
				t.Error("outcome should report the update as unhandled")
			}
			if len(maxClient.Records()) != 0 || len(gateway.Actions()) != 0 {
				t.Error("an unhandled update must have no side effects")
			}
		})
	}
}

// TestDuplicateCallbackIsSuppressed: MAX redelivers webhooks, and confirming
// twice would send the user two answers.
func TestDuplicateCallbackIsSuppressed(t *testing.T) {
	service, _, gateway := newTestBotService(t)

	payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
	update := callbackUpdate(payload, 1, 1)

	if _, err := service.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	outcome, err := service.HandleUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if !outcome.Duplicate {
		t.Error("the redelivery should be reported as a duplicate")
	}
	if len(gateway.Actions()) != 1 {
		t.Errorf("the Core Gateway saw %d actions, want exactly 1", len(gateway.Actions()))
	}
}

func TestCallbackWithoutCallbackIDIsIgnored(t *testing.T) {
	service, maxClient, _ := newTestBotService(t)

	update := maxapi.Update{
		Type:      maxapi.UpdateMessageCallback,
		Timestamp: time.Now().UnixMilli(),
		Callback:  &maxapi.UpdateCallback{Payload: "v1|confirm|reg_1"},
	}

	outcome, err := service.HandleUpdate(context.Background(), update)
	if err != nil {
		t.Fatalf("HandleUpdate(): %v", err)
	}
	if outcome.Result != ResultBadPayload {
		t.Errorf("outcome = %+v", outcome)
	}
	if len(maxClient.Records()) != 0 {
		t.Error("there is no callback to answer without a callback_id")
	}
}

// TestMaxUnavailableOnAnswerIsReported: the business action already happened,
// so the service reports the failure without undoing anything.
func TestMaxUnavailableOnAnswerIsReported(t *testing.T) {
	service, maxClient, gateway := newTestBotService(t)
	maxClient.FailNext(&maxapi.Error{Op: "answer_callback", Temporary: true, Message: "network failure"})

	payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
	outcome, err := service.HandleUpdate(context.Background(), callbackUpdate(payload, 1, 1))

	if err == nil {
		t.Fatal("a failed answer should be reported to the caller so MAX can retry")
	}
	if outcome.Result != ResultUnavailable {
		t.Errorf("outcome = %+v", outcome)
	}
	if len(gateway.Actions()) != 1 {
		t.Error("the core action already happened and must not be rolled back")
	}
}

// TestCoreResultEventEnrichesTheReplacement proves the bot uses fresh backend
// data when it is offered, which is how the HTTP gateway will improve the
// message without any change to BotService.
func TestCoreResultEventEnrichesTheReplacement(t *testing.T) {
	maxClient := mock.New()
	renderer := render.New(render.WithDefaultMiniAppURL("https://example.ru/app"))
	startsAt := time.Date(2026, 9, 22, 19, 0, 0, 0, time.FixedZone("MSK", 3*60*60))

	gateway := &enrichingGateway{event: &core.EventSummary{
		ID:       "event_42",
		Title:    "Йога в парке",
		StartsAt: &startsAt,
		Address:  "Парк Горького",
	}}

	service := NewBotService(maxClient, gateway, renderer, idempotency.NoopStore{})

	payload := callback.MustEncode(domain.ActionAcceptWaitlist, "registration_15", "event_42")
	if _, err := service.HandleUpdate(context.Background(), callbackUpdate(payload, 1, 1)); err != nil {
		t.Fatalf("HandleUpdate(): %v", err)
	}

	answer, _ := maxClient.LastRecord()
	if !strings.Contains(answer.Text, "Вы записаны на «Йога в парке».") {
		t.Errorf("replacement should use the backend's event data:\n%s", answer.Text)
	}
	if !strings.Contains(answer.Text, "22 сентября, 19:00") {
		t.Errorf("replacement should show the backend's start time:\n%s", answer.Text)
	}
}

// enrichingGateway is a core.Gateway that returns event details.
type enrichingGateway struct {
	event *core.EventSummary
}

func (g *enrichingGateway) result() (*core.ActionResult, error) {
	return &core.ActionResult{Status: core.StatusAccepted, Event: g.event}, nil
}

func (g *enrichingGateway) ConfirmRegistration(context.Context, core.ActionRequest) (*core.ActionResult, error) {
	return g.result()
}
func (g *enrichingGateway) CancelRegistration(context.Context, core.ActionRequest) (*core.ActionResult, error) {
	return g.result()
}
func (g *enrichingGateway) AcceptWaitlistOffer(context.Context, core.ActionRequest) (*core.ActionResult, error) {
	return g.result()
}
func (g *enrichingGateway) DeclineWaitlistOffer(context.Context, core.ActionRequest) (*core.ActionResult, error) {
	return g.result()
}
func (g *enrichingGateway) ReportBotStatus(context.Context, core.BotStatusRequest) error {
	return nil
}
func (g *enrichingGateway) Mode() string               { return "test" }
func (g *enrichingGateway) Ping(context.Context) error { return nil }

func shortName(payload string) string {
	if payload == "" {
		return "empty"
	}
	if len(payload) > 24 {
		return payload[:24]
	}
	return payload
}
