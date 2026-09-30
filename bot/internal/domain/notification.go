package domain

import "time"

// NotificationType identifies a kind of outbound notification.
//
// These values are part of the public contract with the Core Backend: they are
// the `type` field of POST /api/v1/notifications. Changing a value is a
// breaking API change and requires a new API version.
type NotificationType string

const (
	// NotificationRegistrationCreated confirms that the user is signed up.
	NotificationRegistrationCreated NotificationType = "registration_created"
	// NotificationReminder24h is the day-before reminder.
	NotificationReminder24h NotificationType = "reminder_24h"
	// NotificationConfirmationRequired asks the user to confirm attendance.
	NotificationConfirmationRequired NotificationType = "confirmation_required"
	// NotificationConfirmationRetry repeats the confirmation request.
	NotificationConfirmationRetry NotificationType = "confirmation_retry"
	// NotificationReminder1h is the "starting soon" reminder.
	NotificationReminder1h NotificationType = "reminder_1h"
	// NotificationWaitlistOffer offers a freed seat to a waitlisted user.
	NotificationWaitlistOffer NotificationType = "waitlist_offer"
	// NotificationEventUpdated reports changes made by the organiser.
	NotificationEventUpdated NotificationType = "event_updated"
	// NotificationEventCancelled reports that the event will not happen.
	NotificationEventCancelled NotificationType = "event_cancelled"
)

// AllNotificationTypes lists every supported type, in contract order.
//
// Used by validation and by the OpenAPI/doc generators so that adding a type
// in one place cannot silently diverge from the other.
func AllNotificationTypes() []NotificationType {
	return []NotificationType{
		NotificationRegistrationCreated,
		NotificationReminder24h,
		NotificationConfirmationRequired,
		NotificationConfirmationRetry,
		NotificationReminder1h,
		NotificationWaitlistOffer,
		NotificationEventUpdated,
		NotificationEventCancelled,
	}
}

// IsValid reports whether t is a supported notification type.
func (t NotificationType) IsValid() bool {
	for _, known := range AllNotificationTypes() {
		if t == known {
			return true
		}
	}
	return false
}

// Recipient identifies the MAX user a notification is addressed to.
type Recipient struct {
	// MaxUserID is the user's numeric MAX identifier. The Core Backend is
	// responsible for knowing the mapping app user -> MAX user.
	MaxUserID int64
}

// Event is the subset of event data the bot needs to render a message.
//
// The bot is not the source of truth for events; it only formats what it is
// given. StartsAt is an absolute instant with an offset (RFC3339); the bot
// localises it only at render time.
type Event struct {
	ID         string
	Title      string
	StartsAt   time.Time
	Address    string
	MiniAppURL string
}

// Registration identifies the user's registration on an event.
type Registration struct {
	ID string
}

// FieldChange describes one organiser-made change for event_updated.
//
// Field is a machine-readable key ("time", "address", ...), Old and New are
// already human-readable; Note carries free-form organiser text.
type FieldChange struct {
	Field string
	Old   string
	New   string
	Note  string
}

// Change field keys recognised by the renderer. Unknown keys are still
// rendered, using the raw key as the label, so the Core Backend can add new
// change kinds without a bot release.
const (
	ChangeFieldTime    = "time"
	ChangeFieldAddress = "address"
	ChangeFieldTitle   = "title"
	ChangeFieldOther   = "other"
)

// Notification is the internal, validated command to notify a user.
//
// The HTTP layer maps its versioned wire DTO onto this struct. Application
// services never see the wire format, so a v2 API can be added by writing a new
// mapper and nothing else.
type Notification struct {
	// RequestID is the caller-supplied correlation id. It flows into logs,
	// idempotency checks and (later) into CoreGateway calls.
	RequestID string

	Type         NotificationType
	Recipient    Recipient
	Event        Event
	Registration Registration

	// OfferExpiresAt is required for NotificationWaitlistOffer.
	OfferExpiresAt *time.Time
	// Changes carries the organiser's edits for NotificationEventUpdated.
	Changes []FieldChange
	// OrganizerMessage is free-form text from the organiser, shown verbatim.
	OrganizerMessage string
}

// NeedsRegistration reports whether this notification type requires a
// registration id, i.e. whether its message can carry action buttons.
func (t NotificationType) NeedsRegistration() bool {
	switch t {
	case NotificationEventUpdated, NotificationEventCancelled:
		return false
	default:
		return true
	}
}

// HasCityOffset reports whether t carries a non-zero UTC offset.
//
// The bot prints dates and times in exactly the offset they arrive with: that
// is how "19:00" in the message matches "19:00" on the poster, in whatever
// city the event is. A timestamp in UTC ("Z", "+00:00", "-00:00") carries no
// city at all, and printing it would show a Moscow user a time three hours
// early. Such a timestamp is therefore a contract error, not something to
// guess about.
//
// No Russian city sits at UTC+0, so the rule rejects nothing legitimate for
// this product.
func HasCityOffset(t time.Time) bool {
	_, offset := t.Zone()
	return offset != 0
}
