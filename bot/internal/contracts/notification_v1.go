// Package contracts holds the versioned wire DTOs of the bot's public HTTP
// API, together with their validation and their mapping onto the domain.
//
// Keeping the wire format in its own package is what makes "the contract must
// not change when Postman is replaced by the Core Backend" enforceable: the
// DTOs here are the contract, they are mirrored in api/openapi.yaml, and the
// application layer never sees them. Adding a v2 means adding a file here and
// a mapper, not touching any service.
package contracts

import (
	"fmt"
	"strings"
	"time"

	"hackatonBotMAX/internal/domain"
)

// NotificationRequestV1 is the body of POST /api/v1/notifications.
//
// Timestamps are RFC3339 with an explicit offset. Localised, human-readable
// dates are never accepted as input: the bot formats them at render time, so
// the caller cannot accidentally pin the message to one locale.
type NotificationRequestV1 struct {
	// RequestID is the caller's correlation id. It is echoed in responses,
	// appears in every log line for this notification, and is the
	// idempotency key.
	RequestID string `json:"request_id"`
	// Type is one of the supported notification types.
	Type string `json:"type"`

	Recipient    RecipientV1     `json:"recipient"`
	Event        EventV1         `json:"event"`
	Registration *RegistrationV1 `json:"registration,omitempty"`

	// Data carries type-specific fields. Keeping them in a nested object
	// rather than at the top level means adding a field for one type cannot
	// widen the envelope every other type has to ignore.
	Data *NotificationDataV1 `json:"data,omitempty"`
}

// RecipientV1 identifies the addressee.
type RecipientV1 struct {
	// MaxUserID is the numeric MAX user id. The Core Backend owns the
	// mapping from application user to MAX user.
	MaxUserID int64 `json:"max_user_id"`
}

// EventV1 is the event data needed to render a message.
type EventV1 struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// StartsAt is RFC3339 with an offset, e.g. 2026-09-22T19:00:00+03:00.
	StartsAt   string `json:"starts_at"`
	Address    string `json:"address,omitempty"`
	MiniAppURL string `json:"mini_app_url,omitempty"`
}

// RegistrationV1 identifies the user's registration.
type RegistrationV1 struct {
	ID string `json:"id"`
}

// NotificationDataV1 holds the type-specific payload.
type NotificationDataV1 struct {
	// OfferExpiresAt is required for waitlist_offer. RFC3339.
	OfferExpiresAt string `json:"offer_expires_at,omitempty"`
	// Changes lists the organiser's edits for event_updated.
	Changes []ChangeV1 `json:"changes,omitempty"`
	// OrganizerMessage is free-form organiser text, shown verbatim.
	OrganizerMessage string `json:"organizer_message,omitempty"`
}

// ChangeV1 is one changed field.
//
// Field is machine-readable ("time", "address", "title", "other"); Old and New
// are already human-readable strings, because only the organiser knows how to
// phrase "moved to the east entrance".
type ChangeV1 struct {
	Field string `json:"field"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
	Note  string `json:"note,omitempty"`
}

// NotificationResponseV1 is the 202 body.
type NotificationResponseV1 struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	// MessageID is the MAX message id, or the mock's synthetic id.
	MessageID string `json:"message_id,omitempty"`
	// Duplicate is true when this request_id was already processed and the
	// bot did not send a second message.
	Duplicate bool `json:"duplicate,omitempty"`
}

// Status values for NotificationResponseV1.
const (
	// StatusSent means the message was handed to MAX.
	StatusSent = "sent"
	// StatusDuplicate means the request_id was seen before.
	StatusDuplicate = "duplicate"
)

// maxLen caps free-form fields so a caller cannot make the bot build a message
// MAX will reject, or fill the logs with megabytes of text.
const (
	maxTitleLen            = 512
	maxAddressLen          = 512
	maxIDLen               = 256
	maxOrganizerMessageLen = 2000
	maxChanges             = 20
)

// Validate checks the request and returns a FieldErrors describing every
// problem found.
//
// All problems are reported at once: making a Postman user fix one field per
// round-trip is a poor way to learn a contract.
func (r NotificationRequestV1) Validate() error {
	errs := FieldErrors{}

	if strings.TrimSpace(r.RequestID) == "" {
		errs.Add("request_id", "is required")
	} else if len(r.RequestID) > maxIDLen {
		errs.Add("request_id", fmt.Sprintf("must be at most %d characters", maxIDLen))
	}

	notificationType := domain.NotificationType(strings.TrimSpace(r.Type))
	if r.Type == "" {
		errs.Add("type", "is required")
	} else if !notificationType.IsValid() {
		errs.Add("type", fmt.Sprintf("must be one of: %s", joinTypes()))
	}

	if r.Recipient.MaxUserID <= 0 {
		errs.Add("recipient.max_user_id", "is required and must be a positive integer")
	}

	if strings.TrimSpace(r.Event.ID) == "" {
		errs.Add("event.id", "is required")
	} else if len(r.Event.ID) > maxIDLen {
		errs.Add("event.id", fmt.Sprintf("must be at most %d characters", maxIDLen))
	} else if strings.Contains(r.Event.ID, "|") {
		// The id is embedded in a pipe-separated callback payload.
		errs.Add("event.id", "must not contain the '|' character")
	}

	if strings.TrimSpace(r.Event.Title) == "" {
		errs.Add("event.title", "is required")
	} else if len([]rune(r.Event.Title)) > maxTitleLen {
		errs.Add("event.title", fmt.Sprintf("must be at most %d characters", maxTitleLen))
	}

	if len([]rune(r.Event.Address)) > maxAddressLen {
		errs.Add("event.address", fmt.Sprintf("must be at most %d characters", maxAddressLen))
	}

	if _, err := parseTimestamp(r.Event.StartsAt); err != nil {
		errs.Add("event.starts_at", err.Error())
	}

	if r.Event.MiniAppURL != "" && !isHTTPURL(r.Event.MiniAppURL) {
		errs.Add("event.mini_app_url", "must be an absolute http(s) URL")
	}

	// Registration is required exactly for the types whose messages carry
	// action buttons: without it there is nothing to confirm or cancel.
	if notificationType.IsValid() && notificationType.NeedsRegistration() {
		switch {
		case r.Registration == nil || strings.TrimSpace(r.Registration.ID) == "":
			errs.Add("registration.id", fmt.Sprintf("is required for type %q", r.Type))
		case len(r.Registration.ID) > maxIDLen:
			errs.Add("registration.id", fmt.Sprintf("must be at most %d characters", maxIDLen))
		case strings.Contains(r.Registration.ID, "|"):
			errs.Add("registration.id", "must not contain the '|' character")
		}
	}

	if notificationType == domain.NotificationWaitlistOffer {
		if r.Data == nil || strings.TrimSpace(r.Data.OfferExpiresAt) == "" {
			errs.Add("data.offer_expires_at", "is required for type \"waitlist_offer\"")
		} else if _, err := parseTimestamp(r.Data.OfferExpiresAt); err != nil {
			errs.Add("data.offer_expires_at", err.Error())
		}
	}

	if r.Data != nil {
		if len(r.Data.Changes) > maxChanges {
			errs.Add("data.changes", fmt.Sprintf("must contain at most %d entries", maxChanges))
		}
		if len([]rune(r.Data.OrganizerMessage)) > maxOrganizerMessageLen {
			errs.Add("data.organizer_message", fmt.Sprintf("must be at most %d characters", maxOrganizerMessageLen))
		}
		for i, change := range r.Data.Changes {
			if strings.TrimSpace(change.Field) == "" {
				errs.Add(fmt.Sprintf("data.changes[%d].field", i), "is required")
			}
		}
	}

	// event_updated without any description of the change is a message that
	// tells the user nothing actionable.
	if notificationType == domain.NotificationEventUpdated {
		hasChanges := r.Data != nil && (len(r.Data.Changes) > 0 || strings.TrimSpace(r.Data.OrganizerMessage) != "")
		if !hasChanges {
			errs.Add("data", "type \"event_updated\" requires data.changes or data.organizer_message")
		}
	}

	if errs.Empty() {
		return nil
	}
	return errs
}

// ToDomain converts a validated request into the internal model.
//
// Callers must call Validate first; ToDomain assumes well-formed input and
// silently tolerates what validation would already have rejected.
func (r NotificationRequestV1) ToDomain() domain.Notification {
	startsAt, _ := parseTimestamp(r.Event.StartsAt)

	notification := domain.Notification{
		RequestID: strings.TrimSpace(r.RequestID),
		Type:      domain.NotificationType(strings.TrimSpace(r.Type)),
		Recipient: domain.Recipient{MaxUserID: r.Recipient.MaxUserID},
		Event: domain.Event{
			ID:         strings.TrimSpace(r.Event.ID),
			Title:      strings.TrimSpace(r.Event.Title),
			StartsAt:   startsAt,
			Address:    strings.TrimSpace(r.Event.Address),
			MiniAppURL: strings.TrimSpace(r.Event.MiniAppURL),
		},
	}

	if r.Registration != nil {
		notification.Registration = domain.Registration{ID: strings.TrimSpace(r.Registration.ID)}
	}

	if r.Data != nil {
		if r.Data.OfferExpiresAt != "" {
			if expiresAt, err := parseTimestamp(r.Data.OfferExpiresAt); err == nil {
				notification.OfferExpiresAt = &expiresAt
			}
		}
		notification.OrganizerMessage = strings.TrimSpace(r.Data.OrganizerMessage)
		for _, change := range r.Data.Changes {
			notification.Changes = append(notification.Changes, domain.FieldChange{
				Field: strings.TrimSpace(change.Field),
				Old:   strings.TrimSpace(change.Old),
				New:   strings.TrimSpace(change.New),
				Note:  strings.TrimSpace(change.Note),
			})
		}
	}

	return notification
}

// parseTimestamp accepts RFC3339 with the event city's offset and reports a
// usable message on failure.
//
// A UTC timestamp is rejected rather than accepted and converted: the bot
// prints times in the offset they carry, and it has no way to know which city
// a bare UTC instant belongs to. See domain.HasCityOffset.
func parseTimestamp(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("is required and must be an RFC3339 timestamp, e.g. 2026-09-22T19:00:00+03:00")
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be an RFC3339 timestamp, e.g. 2026-09-22T19:00:00+03:00")
	}
	if !domain.HasCityOffset(parsed) {
		return time.Time{}, fmt.Errorf("must carry the event city's UTC offset, e.g. 2026-09-22T19:00:00+03:00; " +
			"UTC (\"Z\", \"+00:00\") is rejected because the bot prints times in the offset they arrive with")
	}
	return parsed, nil
}

func isHTTPURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

func joinTypes() string {
	all := domain.AllNotificationTypes()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
}
