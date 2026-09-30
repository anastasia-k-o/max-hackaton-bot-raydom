package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/maxapi/mock"
	"hackatonBotMAX/internal/render"
)

var msk = time.FixedZone("MSK", 3*60*60)

func testNotification(notificationType domain.NotificationType) domain.Notification {
	return domain.Notification{
		RequestID: "req_" + string(notificationType),
		Type:      notificationType,
		Recipient: domain.Recipient{MaxUserID: 123456789},
		Event: domain.Event{
			ID:         "event_42",
			Title:      "Йога в парке",
			StartsAt:   time.Date(2026, 9, 22, 19, 0, 0, 0, msk),
			Address:    "Парк Горького",
			MiniAppURL: "https://example.ru/app/event_42",
		},
		Registration: domain.Registration{ID: "registration_15"},
	}
}

func newTestNotificationService() (*NotificationService, *mock.Client) {
	maxClient := mock.New()
	renderer := render.New(render.WithDefaultMiniAppURL("https://example.ru/app"))
	service := NewNotificationService(maxClient, renderer, idempotency.NewMemoryStore(time.Minute))
	return service, maxClient
}

// TestSendDeliversEveryNotificationType covers Scenario A: MockMAX receives a
// correct message for each supported type.
func TestSendDeliversEveryNotificationType(t *testing.T) {
	expiry := time.Date(2026, 9, 22, 16, 0, 0, 0, msk)

	for _, notificationType := range domain.AllNotificationTypes() {
		t.Run(string(notificationType), func(t *testing.T) {
			service, maxClient := newTestNotificationService()

			notification := testNotification(notificationType)
			switch notificationType {
			case domain.NotificationWaitlistOffer:
				notification.OfferExpiresAt = &expiry
			case domain.NotificationEventUpdated:
				notification.Changes = []domain.FieldChange{{Field: domain.ChangeFieldTime, Old: "19:00", New: "20:00"}}
			}

			result, err := service.Send(context.Background(), notification)
			if err != nil {
				t.Fatalf("Send(): %v", err)
			}
			if result.MessageID == "" {
				t.Error("expected a message id")
			}
			if result.Duplicate {
				t.Error("a first send is not a duplicate")
			}

			sent, ok := maxClient.LastSent()
			if !ok {
				t.Fatal("MockMAX recorded no message")
			}
			if sent.UserID != 123456789 {
				t.Errorf("message sent to %d", sent.UserID)
			}
			if strings.TrimSpace(sent.Text) == "" {
				t.Error("the message should have text")
			}
		})
	}
}

// TestSendIsIdempotentPerRequestID: a Core Backend retry must not double-send.
func TestSendIsIdempotentPerRequestID(t *testing.T) {
	service, maxClient := newTestNotificationService()
	notification := testNotification(domain.NotificationReminder24h)

	first, err := service.Send(context.Background(), notification)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	if first.Duplicate {
		t.Error("first send should not be a duplicate")
	}

	second, err := service.Send(context.Background(), notification)
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if !second.Duplicate {
		t.Error("the retry should be reported as a duplicate")
	}

	if len(maxClient.Records()) != 1 {
		t.Errorf("MockMAX got %d messages, want exactly 1", len(maxClient.Records()))
	}
}

// TestDifferentRequestIDsAreNotDeduplicated: two genuinely different
// notifications about the same event must both go out.
func TestDifferentRequestIDsAreNotDeduplicated(t *testing.T) {
	service, maxClient := newTestNotificationService()

	first := testNotification(domain.NotificationReminder24h)
	first.RequestID = "req_a"
	second := testNotification(domain.NotificationReminder1h)
	second.RequestID = "req_b"

	if _, err := service.Send(context.Background(), first); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := service.Send(context.Background(), second); err != nil {
		t.Fatalf("second: %v", err)
	}

	if len(maxClient.Records()) != 2 {
		t.Errorf("MockMAX got %d messages, want 2", len(maxClient.Records()))
	}
}

// TestFailedSendReleasesTheIdempotencyKey: reporting an error while silently
// blocking the retry would be the worst of both worlds.
func TestFailedSendReleasesTheIdempotencyKey(t *testing.T) {
	service, maxClient := newTestNotificationService()
	maxClient.FailNext(&maxapi.Error{Op: "send_message", Temporary: true, Message: "network failure"})

	notification := testNotification(domain.NotificationConfirmationRequired)

	if _, err := service.Send(context.Background(), notification); err == nil {
		t.Fatal("expected the send to fail")
	} else if !errors.Is(err, ErrSendFailed) {
		t.Errorf("error = %v, want it to wrap ErrSendFailed", err)
	}

	result, err := service.Send(context.Background(), notification)
	if err != nil {
		t.Fatalf("the retry should succeed: %v", err)
	}
	if result.Duplicate {
		t.Error("the retry after a failure must not be treated as a duplicate")
	}
	if len(maxClient.Records()) != 1 {
		t.Errorf("MockMAX got %d messages, want 1", len(maxClient.Records()))
	}
}

func TestSendRejectsUnrenderableNotification(t *testing.T) {
	service, maxClient := newTestNotificationService()

	notification := testNotification(domain.NotificationType("not_a_type"))
	if _, err := service.Send(context.Background(), notification); err == nil {
		t.Fatal("expected a render failure")
	}
	if len(maxClient.Records()) != 0 {
		t.Error("nothing should have been sent")
	}
}

// TestSendPassesRenderedButtonsThrough is the Scenario A assertion the spec
// describes: "I see the composed message and its buttons".
func TestSendPassesRenderedButtonsThrough(t *testing.T) {
	service, maxClient := newTestNotificationService()

	if _, err := service.Send(context.Background(), testNotification(domain.NotificationConfirmationRequired)); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	sent, _ := maxClient.LastSent()
	if len(sent.Keyboard) != 1 || len(sent.Keyboard[0]) != 2 {
		t.Fatalf("expected one row of two buttons, got %+v", sent.Keyboard)
	}
	for _, button := range sent.Keyboard[0] {
		if button.Kind != string(domain.ButtonCallback) {
			t.Errorf("confirmation buttons should be callbacks, got %q", button.Kind)
		}
		if !strings.HasPrefix(button.Payload, "v1|") {
			t.Errorf("button payload should be a versioned encoded action, got %q", button.Payload)
		}
		if button.Payload == button.Text {
			t.Error("button text must never be used as the command")
		}
	}
}
