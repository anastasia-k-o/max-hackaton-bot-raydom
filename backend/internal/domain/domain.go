// Package domain holds the backend's data model and its error vocabulary.
//
// Status names, error codes and field names follow the mini app's mocks
// (frontend/src/api/mocks/handlers.js) and the bot's contract
// (bot/docs/INTEGRATION_MINIAPP.md). Both clients already speak them, so the
// backend adopts them as they are rather than inventing a third dialect.
package domain

import (
	"fmt"
	"net/http"
	"slices"
	"time"
)

// Event statuses.
const (
	EventModeration = "moderation"
	EventPublished  = "published"
	EventCancelled  = "cancelled"
	EventPast       = "past" // demo data only: the mocks' finished events
)

// Registration statuses. A place in the waitlist is a registration too: one
// registration id follows the user from the queue to a seat.
const (
	RegRegistered = "registered" // has a seat, has not confirmed yet
	RegConfirmed  = "confirmed"  // pressed «Приду», or signed up inside the confirmation window
	RegWaitlist   = "waitlist"
	RegOffered    = "offered" // a seat is held for them until offer_expires_at
	RegCancelled  = "cancelled"
)

// Cancel reasons set by the system rather than the user.
const (
	ReasonOfferExpired  = "offer_expired"
	ReasonOfferDeclined = "offer_declined"
)

// ActiveStatuses are registrations that still count for the user.
var ActiveStatuses = []string{RegRegistered, RegConfirmed, RegWaitlist, RegOffered}

// SeatStatuses hold a seat against the event's capacity.
var SeatStatuses = []string{RegRegistered, RegConfirmed, RegOffered}

// IsActive reports whether a registration status still counts.
func IsActive(status string) bool { return slices.Contains(ActiveStatuses, status) }

// HoldsSeat reports whether a registration status occupies a seat.
func HoldsSeat(status string) bool { return slices.Contains(SeatStatuses, status) }

// User is a person known to the backend. MaxUserID is nil for demo
// participants that exist only to fill seats: nothing can be sent to them.
type User struct {
	ID                   string
	MaxUserID            *int64
	FirstName            string
	LastName             string
	PhotoURL             *string
	IsVerified           bool
	IsAuthor             bool
	CityID               string
	District             *string
	BotAvailable         bool
	BotStatusAt          *time.Time
	OnboardingCompleted  bool
	ConsentAcceptedAt    *time.Time
	NotifyReminders      bool
	NotifyRecommendation bool
	// Interests are tag ids for «Для вас».
	Interests []string
	// Demo marks seeded people: the bot is never asked to write to them,
	// even when they carry a made-up MAX id.
	Demo      bool
	CreatedAt time.Time
}

// DisplayName is how the user appears to organisers: «Мария Л.».
func (u User) DisplayName() string {
	if u.LastName == "" {
		return u.FirstName
	}
	return fmt.Sprintf("%s %s.", u.FirstName, string([]rune(u.LastName)[:1]))
}

// Event is an announcement in the catalog.
type Event struct {
	ID               string
	Title            string
	ShortDescription string
	Description      string
	CategoryID       string
	TagIDs           []string
	StartsAt         time.Time
	DurationMin      int
	Timezone         string
	CityID           string
	District         string
	Address          string
	HowToFind        string
	Lat, Lon         *float64
	Capacity         *int // nil = unlimited
	// ExtraRegistered and ExtraWaitlist are demo participants without rows,
	// carried over from the mocks so seeded events look lived-in.
	ExtraRegistered int
	ExtraWaitlist   int
	Level           *string
	AgeLimit        *string
	Bring           *string
	AuthorID        string
	AuthorName      string
	Contact         string
	CoverURL        *string
	Status          string
	ModerationFlags []string
	PublishedAt     time.Time
	Views           int
	Popularity      int
	// Version grows on every change participants are told about, so the
	// event_updated notifications of two edits do not collapse into one.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Location is the event's city time zone. Times are always shown and sent
// in it: the bot prints a time in exactly the offset it arrives with.
func (e Event) Location() *time.Location { return LocationOf(e.Timezone) }

// Registration is a seat or a place in the queue.
type Registration struct {
	ID             string
	EventID        string
	UserID         string
	Status         string
	QueuePosition  *int
	OfferExpiresAt *time.Time
	CancelReason   *string
	CancelledLate  bool
	FromWaitlist   bool
	CreatedAt      time.Time
	ConfirmedAt    *time.Time
	CancelledAt    *time.Time
}

// Error is a business error with the HTTP status and code both clients
// understand. Message goes to logs, never to the user: the clients map the
// code to their own text.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []FieldError
}

// FieldError points at one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds a business error.
func Errorf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Common errors.
func NotFound(what, id string) *Error {
	return Errorf(http.StatusNotFound, "not_found", "%s %s not found", what, id)
}

// Conflict is a generic "not now".
func Conflict(format string, args ...any) *Error {
	return Errorf(http.StatusConflict, "conflict", format, args...)
}

// Invalid collects field errors into one 400.
func Invalid(details []FieldError) *Error {
	msg := details[0].Field + ": " + details[0].Message
	if len(details) > 1 {
		msg += fmt.Sprintf(" (and %d more problem(s))", len(details)-1)
	}
	return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: msg, Details: details}
}
