package contracts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/domain"
)

func validRequest() NotificationRequestV1 {
	return NotificationRequestV1{
		RequestID: "req_123",
		Type:      string(domain.NotificationConfirmationRequired),
		Recipient: RecipientV1{MaxUserID: 123456789},
		Event: EventV1{
			ID:         "event_42",
			Title:      "Йога в парке",
			StartsAt:   "2026-09-22T19:00:00+03:00",
			Address:    "Парк Горького",
			MiniAppURL: "https://example.ru/app/event_42",
		},
		Registration: &RegistrationV1{ID: "registration_15"},
	}
}

func TestValidateAcceptsEveryNotificationType(t *testing.T) {
	for _, notificationType := range domain.AllNotificationTypes() {
		t.Run(string(notificationType), func(t *testing.T) {
			request := validRequest()
			request.Type = string(notificationType)

			switch notificationType {
			case domain.NotificationWaitlistOffer:
				request.Data = &NotificationDataV1{OfferExpiresAt: "2026-09-22T16:00:00+03:00"}
			case domain.NotificationEventUpdated:
				request.Registration = nil
				request.Data = &NotificationDataV1{
					Changes: []ChangeV1{{Field: "time", Old: "19:00", New: "20:00"}},
				}
			case domain.NotificationEventCancelled:
				request.Registration = nil
			}

			if err := request.Validate(); err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*NotificationRequestV1)
		wantField string
	}{
		{
			name:      "missing request id",
			mutate:    func(r *NotificationRequestV1) { r.RequestID = "" },
			wantField: "request_id",
		},
		{
			name:      "missing type",
			mutate:    func(r *NotificationRequestV1) { r.Type = "" },
			wantField: "type",
		},
		{
			name:      "unknown type",
			mutate:    func(r *NotificationRequestV1) { r.Type = "send_fireworks" },
			wantField: "type",
		},
		{
			name:      "missing max user id",
			mutate:    func(r *NotificationRequestV1) { r.Recipient.MaxUserID = 0 },
			wantField: "recipient.max_user_id",
		},
		{
			name:      "negative max user id",
			mutate:    func(r *NotificationRequestV1) { r.Recipient.MaxUserID = -5 },
			wantField: "recipient.max_user_id",
		},
		{
			name:      "missing event id",
			mutate:    func(r *NotificationRequestV1) { r.Event.ID = "" },
			wantField: "event.id",
		},
		{
			name:      "event id containing the payload separator",
			mutate:    func(r *NotificationRequestV1) { r.Event.ID = "event|42" },
			wantField: "event.id",
		},
		{
			name:      "missing title",
			mutate:    func(r *NotificationRequestV1) { r.Event.Title = "" },
			wantField: "event.title",
		},
		{
			name:      "missing starts_at",
			mutate:    func(r *NotificationRequestV1) { r.Event.StartsAt = "" },
			wantField: "event.starts_at",
		},
		{
			name:      "localised date is rejected",
			mutate:    func(r *NotificationRequestV1) { r.Event.StartsAt = "22 сентября, 19:00" },
			wantField: "event.starts_at",
		},
		{
			name:      "starts_at without an offset is rejected",
			mutate:    func(r *NotificationRequestV1) { r.Event.StartsAt = "2026-09-22 19:00" },
			wantField: "event.starts_at",
		},
		{
			name:      "starts_at in UTC (Z) is rejected",
			mutate:    func(r *NotificationRequestV1) { r.Event.StartsAt = "2026-09-22T16:00:00Z" },
			wantField: "event.starts_at",
		},
		{
			name:      "starts_at with a zero offset is rejected",
			mutate:    func(r *NotificationRequestV1) { r.Event.StartsAt = "2026-09-22T16:00:00+00:00" },
			wantField: "event.starts_at",
		},
		{
			name: "waitlist offer expiring in UTC is rejected",
			mutate: func(r *NotificationRequestV1) {
				r.Type = string(domain.NotificationWaitlistOffer)
				r.Data = &NotificationDataV1{OfferExpiresAt: "2026-09-22T13:00:00Z"}
			},
			wantField: "data.offer_expires_at",
		},
		{
			name:      "relative mini app url is rejected",
			mutate:    func(r *NotificationRequestV1) { r.Event.MiniAppURL = "/app/event_42" },
			wantField: "event.mini_app_url",
		},
		{
			name:      "missing registration for an actionable type",
			mutate:    func(r *NotificationRequestV1) { r.Registration = nil },
			wantField: "registration.id",
		},
		{
			name: "registration id containing the payload separator",
			mutate: func(r *NotificationRequestV1) {
				r.Registration = &RegistrationV1{ID: "reg|15"}
			},
			wantField: "registration.id",
		},
		{
			name: "waitlist offer without an expiry",
			mutate: func(r *NotificationRequestV1) {
				r.Type = string(domain.NotificationWaitlistOffer)
			},
			wantField: "data.offer_expires_at",
		},
		{
			name: "waitlist offer with a malformed expiry",
			mutate: func(r *NotificationRequestV1) {
				r.Type = string(domain.NotificationWaitlistOffer)
				r.Data = &NotificationDataV1{OfferExpiresAt: "tomorrow"}
			},
			wantField: "data.offer_expires_at",
		},
		{
			name: "event_updated with nothing changed",
			mutate: func(r *NotificationRequestV1) {
				r.Type = string(domain.NotificationEventUpdated)
				r.Registration = nil
			},
			wantField: "data",
		},
		{
			name: "change entry without a field name",
			mutate: func(r *NotificationRequestV1) {
				r.Type = string(domain.NotificationEventUpdated)
				r.Registration = nil
				r.Data = &NotificationDataV1{Changes: []ChangeV1{{New: "20:00"}}}
			},
			wantField: "data.changes[0].field",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validRequest()
			tc.mutate(&request)

			err := request.Validate()
			if err == nil {
				t.Fatal("Validate() should have failed")
			}

			fieldErrs, ok := AsFieldErrors(err)
			if !ok {
				t.Fatalf("Validate() returned %T, want FieldErrors", err)
			}
			if _, reported := fieldErrs[tc.wantField]; !reported {
				t.Fatalf("expected a problem on %q, got %v", tc.wantField, fieldErrs.Details())
			}
		})
	}
}

// TestValidateReportsEveryProblemAtOnce: making a Postman user fix one field
// per round-trip is a poor way to learn a contract.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	request := NotificationRequestV1{}

	err := request.Validate()
	fieldErrs, ok := AsFieldErrors(err)
	if !ok {
		t.Fatalf("Validate() returned %T, want FieldErrors", err)
	}
	if len(fieldErrs) < 5 {
		t.Fatalf("expected several problems at once, got %v", fieldErrs.Details())
	}

	// Details must be stable so the response is deterministic.
	first := fieldErrs.Details()
	second := fieldErrs.Details()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Details() is not deterministic: %v vs %v", first, second)
		}
	}
}

func TestToDomain(t *testing.T) {
	request := validRequest()
	request.Type = string(domain.NotificationWaitlistOffer)
	request.Data = &NotificationDataV1{
		OfferExpiresAt:   "2026-09-22T16:00:00+03:00",
		OrganizerMessage: "  Вход со стороны аллеи.  ",
		Changes: []ChangeV1{
			{Field: " time ", Old: " 19:00 ", New: " 20:00 "},
		},
	}

	notification := request.ToDomain()

	if notification.RequestID != "req_123" {
		t.Errorf("RequestID = %q", notification.RequestID)
	}
	if notification.Type != domain.NotificationWaitlistOffer {
		t.Errorf("Type = %q", notification.Type)
	}
	if notification.Recipient.MaxUserID != 123456789 {
		t.Errorf("MaxUserID = %d", notification.Recipient.MaxUserID)
	}
	if notification.Registration.ID != "registration_15" {
		t.Errorf("Registration.ID = %q", notification.Registration.ID)
	}

	// The offset from the wire must survive: the renderer localises using the
	// event's own timezone, not the server's.
	wantStart := time.Date(2026, 9, 22, 19, 0, 0, 0, time.FixedZone("", 3*60*60))
	if !notification.Event.StartsAt.Equal(wantStart) {
		t.Errorf("StartsAt = %v, want %v", notification.Event.StartsAt, wantStart)
	}
	if _, offset := notification.Event.StartsAt.Zone(); offset != 3*60*60 {
		t.Errorf("StartsAt offset = %d seconds, want 10800", offset)
	}

	if notification.OfferExpiresAt == nil {
		t.Fatal("OfferExpiresAt should be set")
	}
	if notification.OrganizerMessage != "Вход со стороны аллеи." {
		t.Errorf("OrganizerMessage = %q (should be trimmed)", notification.OrganizerMessage)
	}
	if len(notification.Changes) != 1 || notification.Changes[0].Field != "time" || notification.Changes[0].New != "20:00" {
		t.Errorf("Changes = %+v (should be trimmed)", notification.Changes)
	}
}

// TestEnvelopeShapeIsStable pins the JSON field names. They are the contract
// with the Mini App team and with api/openapi.yaml; renaming one silently is
// exactly the breakage this project is meant to avoid.
func TestEnvelopeShapeIsStable(t *testing.T) {
	raw := `{
	  "request_id": "req_123",
	  "type": "confirmation_required",
	  "recipient": {"max_user_id": 123456789},
	  "event": {
	    "id": "event_42",
	    "title": "Йога в парке",
	    "starts_at": "2026-09-22T19:00:00+03:00",
	    "address": "Парк Горького",
	    "mini_app_url": "https://example.ru/app/event_42"
	  },
	  "registration": {"id": "registration_15"},
	  "data": {}
	}`

	var request NotificationRequestV1
	decoder := json.NewDecoder(newStringReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("the documented envelope must decode: %v", err)
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("the documented envelope must validate: %v", err)
	}
}

func TestErrorCodeHTTPStatus(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want int
	}{
		{CodeInvalidRequest, 400},
		{CodeUnauthorized, 401},
		{CodeNotFound, 404},
		{CodeMethodNotAllowed, 405},
		{CodeConflict, 409},
		{CodePayloadTooLarge, 413},
		{CodeUpstreamUnavailable, 502},
		{CodeInternalError, 500},
		{ErrorCode("something_new"), 500},
	}
	for _, tc := range tests {
		if got := tc.code.HTTPStatus(); got != tc.want {
			t.Errorf("%s.HTTPStatus() = %d, want %d", tc.code, got, tc.want)
		}
	}
}

func TestSanitizeMessage(t *testing.T) {
	got := SanitizeMessage("line one\nline two\ttabbed", 0)
	if got != "line one line two tabbed" {
		t.Errorf("SanitizeMessage() = %q", got)
	}
	if got := SanitizeMessage("abcdefghij", 5); got != "abcde…" {
		t.Errorf("SanitizeMessage() truncation = %q", got)
	}
}

// TestUTCRejectionExplainsTheFix: the error must say what to send instead,
// otherwise the Core Backend team is left guessing why a valid RFC3339 value
// was refused.
func TestUTCRejectionExplainsTheFix(t *testing.T) {
	request := validRequest()
	request.Event.StartsAt = "2026-09-22T16:00:00Z"

	err := request.Validate()
	fieldErrs, ok := AsFieldErrors(err)
	if !ok {
		t.Fatalf("Validate() = %v, want field errors", err)
	}
	message, found := fieldErrs["event.starts_at"]
	if !found {
		t.Fatalf("no error for event.starts_at: %v", fieldErrs)
	}
	if !strings.Contains(message, "+03:00") || !strings.Contains(message, "offset") {
		t.Fatalf("message does not explain the fix: %q", message)
	}
}

// TestCityOffsetsAreAccepted: every Russian offset, from Kaliningrad to
// Kamchatka, must pass. The rule targets UTC, not "anything but Moscow".
func TestCityOffsetsAreAccepted(t *testing.T) {
	for _, offset := range []string{"+02:00", "+03:00", "+05:00", "+07:00", "+10:00", "+12:00"} {
		t.Run(offset, func(t *testing.T) {
			request := validRequest()
			request.Event.StartsAt = "2026-09-22T19:00:00" + offset
			if err := request.Validate(); err != nil {
				t.Fatalf("offset %s rejected: %v", offset, err)
			}
			got := request.ToDomain().Event.StartsAt
			if got.Format("15:04") != "19:00" {
				t.Fatalf("time shifted: got %s, want 19:00 in the city's own offset", got.Format("15:04"))
			}
		})
	}
}
