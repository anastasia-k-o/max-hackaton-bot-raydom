package render

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/domain"
)

// moscow is the timezone the demo data uses. Rendering is asserted against a
// fixed clock so "сегодня"/"завтра" are deterministic.
var moscow = time.FixedZone("MSK", 3*60*60)

func fixedClock(t time.Time) Clock { return func() time.Time { return t } }

func testNotification(notificationType domain.NotificationType) domain.Notification {
	return domain.Notification{
		RequestID: "req_test",
		Type:      notificationType,
		Recipient: domain.Recipient{MaxUserID: 123456789},
		Event: domain.Event{
			ID:         "event_42",
			Title:      "Йога в парке",
			StartsAt:   time.Date(2026, 9, 22, 19, 0, 0, 0, moscow),
			Address:    "Парк Горького",
			MiniAppURL: "https://example.ru/app/event_42",
		},
		Registration: domain.Registration{ID: "registration_15"},
	}
}

// TestNotificationRendersAllTypes checks every supported type produces a
// message with the expected heading and button set. The button *payloads* are
// asserted, not the labels: labels are copy and will change, payloads are the
// contract.
func TestNotificationRendersAllTypes(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, moscow)
	offerExpiry := time.Date(2026, 9, 22, 16, 0, 0, 0, moscow)

	tests := []struct {
		name             string
		mutate           func(*domain.Notification)
		notificationType domain.NotificationType
		wantHeading      string
		wantContains     []string
		wantPayloads     []string
		wantLinkButtons  int
	}{
		{
			name:             "registration_created",
			notificationType: domain.NotificationRegistrationCreated,
			wantHeading:      "✅ Вы записаны",
			wantContains:     []string{"Йога в парке", "22 сентября, 19:00", "Парк Горького"},
			wantPayloads:     []string{"v1|cancel|registration_15|event_42"},
			wantLinkButtons:  1,
		},
		{
			name:             "reminder_24h",
			notificationType: domain.NotificationReminder24h,
			wantHeading:      "⏰ Напоминание",
			wantContains:     []string{"Сегодня в 19:00", "Йога в парке"},
			wantPayloads:     []string{"v1|cancel|registration_15|event_42"},
			wantLinkButtons:  1,
		},
		{
			name:             "confirmation_required",
			notificationType: domain.NotificationConfirmationRequired,
			wantHeading:      "❓ Подтвердите участие",
			wantContains:     []string{"«Йога в парке» начинается сегодня в 19:00."},
			wantPayloads: []string{
				"v1|confirm|registration_15|event_42",
				"v1|cancel|registration_15|event_42",
			},
		},
		{
			name:             "confirmation_retry uses the same copy",
			notificationType: domain.NotificationConfirmationRetry,
			wantHeading:      "❓ Подтвердите участие",
			wantPayloads: []string{
				"v1|confirm|registration_15|event_42",
				"v1|cancel|registration_15|event_42",
			},
		},
		{
			name:             "reminder_1h",
			notificationType: domain.NotificationReminder1h,
			wantHeading:      "📍 Скоро начало",
			wantContains:     []string{"начинается через час", "Адрес: Парк Горького"},
			wantPayloads:     []string{"v1|cancel|registration_15|event_42"},
			wantLinkButtons:  1,
		},
		{
			name:             "waitlist_offer",
			notificationType: domain.NotificationWaitlistOffer,
			mutate: func(n *domain.Notification) {
				n.OfferExpiresAt = &offerExpiry
			},
			wantHeading:  "🔥 Освободилось место",
			wantContains: []string{"Для вас освободилось место", "Предложение действует до сегодня в 16:00."},
			wantPayloads: []string{
				"v1|wl_accept|registration_15|event_42",
				"v1|wl_decline|registration_15|event_42",
			},
		},
		{
			name:             "event_updated",
			notificationType: domain.NotificationEventUpdated,
			mutate: func(n *domain.Notification) {
				n.Registration = domain.Registration{}
				n.Changes = []domain.FieldChange{
					{Field: domain.ChangeFieldTime, Old: "19:00", New: "20:00"},
					{Field: domain.ChangeFieldAddress, New: "Парк Сокольники"},
				}
				n.OrganizerMessage = "Вход со стороны главной аллеи."
			},
			wantHeading: "⚠️ Мероприятие изменено",
			wantContains: []string{
				"Что изменилось:",
				"• Время: 19:00 → 20:00",
				"• Адрес: Парк Сокольники",
				"Сообщение организатора:",
				"Вход со стороны главной аллеи.",
			},
			wantLinkButtons: 1,
		},
		{
			name:             "event_cancelled",
			notificationType: domain.NotificationEventCancelled,
			mutate: func(n *domain.Notification) {
				n.Registration = domain.Registration{}
			},
			wantHeading:     "❌ Мероприятие отменено",
			wantContains:    []string{"«Йога в парке» было отменено организатором."},
			wantLinkButtons: 1,
		},
	}

	renderer := New(WithClock(fixedClock(now)), WithDefaultMiniAppURL("https://example.ru/app"))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			notification := testNotification(tc.notificationType)
			if tc.mutate != nil {
				tc.mutate(&notification)
			}

			message, err := renderer.Notification(notification)
			if err != nil {
				t.Fatalf("Notification() unexpected error: %v", err)
			}

			if !strings.HasPrefix(message.Text, tc.wantHeading) {
				t.Errorf("message should start with %q, got:\n%s", tc.wantHeading, message.Text)
			}
			for _, fragment := range tc.wantContains {
				if !strings.Contains(message.Text, fragment) {
					t.Errorf("message should contain %q, got:\n%s", fragment, message.Text)
				}
			}

			payloads, links := collectButtons(message.Keyboard)
			if len(payloads) != len(tc.wantPayloads) {
				t.Fatalf("got %d callback buttons %v, want %d %v", len(payloads), payloads, len(tc.wantPayloads), tc.wantPayloads)
			}
			for i, want := range tc.wantPayloads {
				if payloads[i] != want {
					t.Errorf("callback button %d payload = %q, want %q", i, payloads[i], want)
				}
			}
			if tc.wantLinkButtons > 0 && links != tc.wantLinkButtons {
				t.Errorf("got %d link buttons, want %d", links, tc.wantLinkButtons)
			}

			// Every payload the renderer emits must decode back.
			for _, payload := range payloads {
				if _, err := callback.Decode(payload); err != nil {
					t.Errorf("payload %q does not decode: %v", payload, err)
				}
			}
		})
	}
}

func TestNotificationRejectsUnknownType(t *testing.T) {
	renderer := New()
	_, err := renderer.Notification(testNotification(domain.NotificationType("fireworks")))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("error = %v, want ErrUnsupportedType", err)
	}
}

// TestActionSucceededHasNoActionButtons is the guard for the spec's
// requirement that old action buttons must not trigger the action again: the
// replacement message carries only a link.
func TestActionSucceededHasNoActionButtons(t *testing.T) {
	renderer := New(WithDefaultMiniAppURL("https://example.ru/app"))
	event := domain.Event{
		ID:       "event_42",
		Title:    "Йога в парке",
		StartsAt: time.Date(2026, 9, 22, 19, 0, 0, 0, moscow),
	}

	for _, action := range domain.AllActionTypes() {
		message := renderer.ActionSucceeded(action, event)

		payloads, links := collectButtons(message.Keyboard)
		if len(payloads) != 0 {
			t.Errorf("%s: replacement message still has callback buttons %v", action, payloads)
		}
		if links == 0 {
			t.Errorf("%s: replacement message should keep a mini app link", action)
		}
	}
}

func TestActionSucceededHeadings(t *testing.T) {
	renderer := New(WithDefaultMiniAppURL("https://example.ru/app"))
	event := domain.Event{Title: "Йога в парке", StartsAt: time.Date(2026, 9, 22, 19, 0, 0, 0, moscow)}

	tests := []struct {
		action       domain.ActionType
		wantHeading  string
		wantContains string
	}{
		{domain.ActionConfirmRegistration, "✅ Участие подтверждено", "Йога в парке"},
		{domain.ActionCancelRegistration, "✅ Запись отменена", "Йога в парке"},
		{domain.ActionAcceptWaitlist, "✅ Место ваше", "Вы записаны на «Йога в парке»."},
		{domain.ActionDeclineWaitlist, "👌 Предложение отклонено", "Йога в парке"},
	}

	for _, tc := range tests {
		t.Run(string(tc.action), func(t *testing.T) {
			message := renderer.ActionSucceeded(tc.action, event)
			if !strings.HasPrefix(message.Text, tc.wantHeading) {
				t.Errorf("want heading %q, got:\n%s", tc.wantHeading, message.Text)
			}
			if !strings.Contains(message.Text, tc.wantContains) {
				t.Errorf("want text containing %q, got:\n%s", tc.wantContains, message.Text)
			}
		})
	}
}

// TestActionFailedNeverLeaksTechnicalDetail is the spec's "no stack traces,
// no technical errors" requirement made executable.
func TestActionFailedNeverLeaksTechnicalDetail(t *testing.T) {
	renderer := New(WithDefaultMiniAppURL("https://example.ru/app"))

	causes := []error{
		core.NewError(core.CodeEventCancelled, "event 42 is cancelled", 409, nil),
		core.NewError(core.CodeRegistrationCancelled, "registration gone", 409, nil),
		core.NewError(core.CodeOfferExpired, "offer expired at 16:00", 410, nil),
		core.NewError(core.CodeNotFound, "no such registration", 404, nil),
		core.NewError(core.CodeUnavailable, "dial tcp 10.0.0.1:8080: connect: connection refused", 0, errors.New("boom")),
		callback.ErrUnknownAction,
	}

	leaks := []string{"dial tcp", "connection refused", "goroutine", "0x", "core ", "registration gone", "panic"}

	for _, cause := range causes {
		message := renderer.ActionFailed(domain.ActionConfirmRegistration, cause)
		for _, leak := range leaks {
			if strings.Contains(message.Text, leak) {
				t.Errorf("message for %v leaks %q:\n%s", cause, leak, message.Text)
			}
		}
		if !strings.HasPrefix(message.Text, "⚠️ Не получилось") {
			t.Errorf("failure message should use the failure heading, got:\n%s", message.Text)
		}
	}
}

func TestActionFailedMessagesAreSpecific(t *testing.T) {
	renderer := New()

	tests := []struct {
		name         string
		cause        error
		wantContains string
	}{
		{"event cancelled", core.NewError(core.CodeEventCancelled, "x", 409, nil), "Мероприятие отменено организатором"},
		{"registration cancelled", core.NewError(core.CodeRegistrationCancelled, "x", 409, nil), "Эта запись уже отменена."},
		{"offer expired", core.NewError(core.CodeOfferExpired, "x", 410, nil), "Срок предложения истёк"},
		{"not found", core.NewError(core.CodeNotFound, "x", 404, nil), "Не удалось найти эту запись"},
		{"unavailable", core.NewError(core.CodeUnavailable, "x", 0, nil), "Сервис временно недоступен"},
		{"stale button", callback.ErrUnsupportedVersion, "Эта кнопка больше не работает"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message := renderer.ActionFailed(domain.ActionConfirmRegistration, tc.cause)
			if !strings.Contains(message.Text, tc.wantContains) {
				t.Errorf("want text containing %q, got:\n%s", tc.wantContains, message.Text)
			}
		})
	}
}

func TestActionNotice(t *testing.T) {
	renderer := New()

	if got := renderer.ActionNotice(domain.ActionConfirmRegistration, nil); got != "Участие подтверждено" {
		t.Errorf("success notice = %q", got)
	}
	if got := renderer.ActionNotice(domain.ActionAcceptWaitlist, core.NewError(core.CodeOfferExpired, "x", 410, nil)); got != "Срок предложения истёк" {
		t.Errorf("expired notice = %q", got)
	}
	if got := renderer.ActionNotice(domain.ActionConfirmRegistration, core.NewError(core.CodeUnavailable, "x", 0, nil)); got != "Сервис временно недоступен" {
		t.Errorf("unavailable notice = %q", got)
	}
}

// TestGreetingDoesNotPromiseACatalogue guards the product boundary stated in
// the spec: the bot is a notification channel, not a second front end.
func TestGreetingDoesNotPromiseACatalogue(t *testing.T) {
	renderer := New(WithDefaultMiniAppURL("https://example.ru/app"))
	message := renderer.Greeting("")

	for _, forbidden := range []string{"поиск", "каталог", "найти мероприятия", "рекоменд"} {
		if strings.Contains(strings.ToLower(message.Text), forbidden) {
			t.Errorf("greeting should not advertise %q:\n%s", forbidden, message.Text)
		}
	}
	if !strings.Contains(message.Text, "мини-приложении") {
		t.Errorf("greeting should point at the mini app:\n%s", message.Text)
	}
}

func TestFreeTextAndHelp(t *testing.T) {
	renderer := New(WithDefaultMiniAppURL("https://example.ru/app"))

	if text := renderer.FreeText("").Text; !strings.Contains(text, "мини-приложение") {
		t.Errorf("free text reply should point at the mini app:\n%s", text)
	}
	if text := renderer.Help("").Text; !strings.Contains(text, "мини-приложении") {
		t.Errorf("help reply should point at the mini app:\n%s", text)
	}
}

// TestLinkButtonsAreSkippedWhenNoURL: a missing mini app URL must not produce
// a button MAX would reject and lose the whole message over.
func TestLinkButtonsAreSkippedWhenNoURL(t *testing.T) {
	renderer := New() // no default mini app URL
	notification := testNotification(domain.NotificationRegistrationCreated)
	notification.Event.MiniAppURL = ""

	message, err := renderer.Notification(notification)
	if err != nil {
		t.Fatalf("Notification(): %v", err)
	}

	payloads, links := collectButtons(message.Keyboard)
	if links != 0 {
		t.Errorf("expected no link buttons without a URL, got %d", links)
	}
	if len(payloads) != 1 {
		t.Errorf("the cancel button should survive, got %v", payloads)
	}
}

func TestLinkButtonsRejectMalformedURLs(t *testing.T) {
	renderer := New()
	notification := testNotification(domain.NotificationEventCancelled)
	notification.Event.MiniAppURL = "not a url"

	message, err := renderer.Notification(notification)
	if err != nil {
		t.Fatalf("Notification(): %v", err)
	}
	if _, links := collectButtons(message.Keyboard); links != 0 {
		t.Errorf("a malformed URL should not become a button")
	}
}

// TestRenderFailsOnUnsafeRegistrationID: an id carrying the payload separator
// must fail loudly at render time, not silently produce a corrupt button.
func TestRenderFailsOnUnsafeRegistrationID(t *testing.T) {
	renderer := New()
	notification := testNotification(domain.NotificationConfirmationRequired)
	notification.Registration.ID = "reg|evil"

	if _, err := renderer.Notification(notification); err == nil {
		t.Fatal("expected an error for a registration id containing the separator")
	}
}

func collectButtons(keyboard *domain.Keyboard) (payloads []string, linkCount int) {
	if keyboard == nil {
		return nil, 0
	}
	for _, row := range keyboard.Rows {
		for _, button := range row {
			switch button.Kind {
			case domain.ButtonCallback:
				payloads = append(payloads, button.Payload)
			case domain.ButtonLink, domain.ButtonOpenApp:
				linkCount++
			}
		}
	}
	return payloads, linkCount
}
