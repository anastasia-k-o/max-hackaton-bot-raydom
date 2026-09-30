package render

import (
	"testing"
	"time"

	"hackatonBotMAX/internal/domain"
)

// buttonsOf flattens a message's keyboard.
func buttonsOf(message domain.Message) []domain.Button {
	if message.Keyboard == nil {
		return nil
	}
	var out []domain.Button
	for _, row := range message.Keyboard.Rows {
		out = append(out, row...)
	}
	return out
}

func buttonsOfKind(message domain.Message, kind domain.ButtonKind) []domain.Button {
	var out []domain.Button
	for _, b := range buttonsOf(message) {
		if b.Kind == kind {
			out = append(out, b)
		}
	}
	return out
}

// TestOpenAppIsOffByDefault: without the option nothing changes. Until the
// mini app is registered for the bot an open_app button can make MAX refuse
// the whole message, so turning it on must be a deliberate act.
func TestOpenAppIsOffByDefault(t *testing.T) {
	message, err := New().Notification(testNotification(domain.NotificationRegistrationCreated))
	if err != nil {
		t.Fatal(err)
	}
	if got := buttonsOfKind(message, domain.ButtonOpenApp); len(got) != 0 {
		t.Fatalf("open_app buttons without the option: %+v", got)
	}
	links := buttonsOfKind(message, domain.ButtonLink)
	if len(links) != 1 || links[0].URL != "https://example.ru/app/event_42" {
		t.Fatalf("link buttons = %+v", links)
	}
}

// TestOpenAppCarriesTheEventID: the event id travels as the start parameter,
// which the mini app turns into #/event/{id}; the per-event URL rides along as
// the adapter's fallback.
func TestOpenAppCarriesTheEventID(t *testing.T) {
	renderer := New(WithOpenAppButtons(true))

	for _, notificationType := range domain.AllNotificationTypes() {
		t.Run(string(notificationType), func(t *testing.T) {
			n := testNotification(notificationType)
			if notificationType == domain.NotificationWaitlistOffer {
				expiry := n.Event.StartsAt.Add(-3 * time.Hour)
				n.OfferExpiresAt = &expiry
			}
			if notificationType == domain.NotificationEventUpdated {
				n.Changes = []domain.FieldChange{{Field: domain.ChangeFieldTime, Old: "19:00", New: "20:00"}}
			}
			message, err := renderer.Notification(n)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range buttonsOfKind(message, domain.ButtonOpenApp) {
				if b.Payload != "event_42" {
					t.Errorf("payload = %q, want event_42", b.Payload)
				}
				if b.URL != "https://example.ru/app/event_42" {
					t.Errorf("fallback URL = %q", b.URL)
				}
			}
			for _, b := range buttonsOfKind(message, domain.ButtonLink) {
				// The only link left may be the route to the address.
				if b.Text != ru.Buttons.Route {
					t.Errorf("stray link button with open_app on: %+v", b)
				}
			}
		})
	}
}

// TestRouteStaysALink: "Маршрут" leads to a map, not into the mini app.
func TestRouteStaysALink(t *testing.T) {
	message, err := New(WithOpenAppButtons(true)).Notification(testNotification(domain.NotificationReminder1h))
	if err != nil {
		t.Fatal(err)
	}
	links := buttonsOfKind(message, domain.ButtonLink)
	if len(links) != 1 || links[0].Text != ru.Buttons.Route {
		t.Fatalf("links = %+v, want only the route", links)
	}
}

// TestOpenAppFallsBackForIDsMAXRejects: MAX accepts ^[\w-]{0,512}$ as a start
// parameter. An id outside that shape gets a plain link instead, so the
// notification is never lost over one button.
func TestOpenAppFallsBackForIDsMAXRejects(t *testing.T) {
	for _, id := range []string{"event.42", "event:42", "событие_42", "event 42"} {
		t.Run(id, func(t *testing.T) {
			n := testNotification(domain.NotificationRegistrationCreated)
			n.Event.ID = id
			message, err := New(WithOpenAppButtons(true)).Notification(n)
			if err != nil {
				t.Fatal(err)
			}
			if got := buttonsOfKind(message, domain.ButtonOpenApp); len(got) != 0 {
				t.Fatalf("open_app sent for id %q: %+v", id, got)
			}
			if got := buttonsOfKind(message, domain.ButtonLink); len(got) != 1 {
				t.Fatalf("no fallback link for id %q: %+v", id, buttonsOf(message))
			}
		})
	}
}

// TestOpenAppNeedsNoURL: the mini app's address is registered in MAX, not
// carried in the button. With open_app on, a missing MINI_APP_URL no longer
// costs the user the button.
func TestOpenAppNeedsNoURL(t *testing.T) {
	n := testNotification(domain.NotificationRegistrationCreated)
	n.Event.MiniAppURL = ""
	message, err := New(WithOpenAppButtons(true)).Notification(n)
	if err != nil {
		t.Fatal(err)
	}
	got := buttonsOfKind(message, domain.ButtonOpenApp)
	if len(got) != 1 || got[0].Payload != "event_42" || got[0].URL != "" {
		t.Fatalf("open_app buttons = %+v", got)
	}
}

// TestGreetingOpensTheCatalog: messages without an event open the mini app
// with an empty start parameter, which lands on the catalog.
func TestGreetingOpensTheCatalog(t *testing.T) {
	renderer := New(WithOpenAppButtons(true), WithDefaultMiniAppURL("https://example.ru/app"))
	for name, message := range map[string]domain.Message{
		"greeting":  renderer.Greeting(""),
		"free text": renderer.FreeText(""),
		"help":      renderer.Help(""),
	} {
		got := buttonsOfKind(message, domain.ButtonOpenApp)
		if len(got) != 1 || got[0].Payload != "" {
			t.Errorf("%s: open_app buttons = %+v, want one with an empty payload", name, got)
		}
	}
}
