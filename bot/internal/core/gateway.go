// Package core is the port through which the bot reaches the Core Backend.
//
// This is the most important boundary in the project. The bot is not the
// source of truth about registrations, seats, waitlists or events: it turns a
// button press into a typed request, hands it over, and renders whatever comes
// back. Today the request goes into an in-memory stub; tomorrow it goes over
// HTTP; if the bot ends up inside the same Go binary as the backend it becomes
// a direct method call. None of those changes touch BotService.
package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ActionRequest is a user action forwarded to the Core Backend.
//
// MaxUserID is always taken from the MAX webhook body, never from the callback
// payload: the payload round-trips through the client and cannot be trusted to
// state who is acting.
type ActionRequest struct {
	// RegistrationID identifies the registration the action applies to.
	RegistrationID string
	// EventID is optional context; the backend may already know it from the
	// registration.
	EventID string
	// MaxUserID is the acting MAX user, from the webhook Update.
	MaxUserID int64
	// RequestID correlates this call with the webhook that caused it and
	// with the bot's own logs.
	RequestID string
	// OccurredAt is when the user pressed the button, per MAX.
	OccurredAt time.Time
}

// BotStatusReason says what MAX told the bot about its dialog with a user.
type BotStatusReason string

const (
	// ReasonBotStarted: the user opened the dialog or pressed "Start".
	ReasonBotStarted BotStatusReason = "bot_started"
	// ReasonBotStopped: the user stopped (blocked) the bot.
	ReasonBotStopped BotStatusReason = "bot_stopped"
	// ReasonDialogRemoved: the user deleted the dialog with the bot.
	ReasonDialogRemoved BotStatusReason = "dialog_removed"
)

// BotStatusRequest tells the Core Backend whether the bot can reach a user.
//
// Only the bot can know this: MAX reports it to the bot and to nobody else.
// The backend needs it to stop promising reminders the bot cannot deliver;
// the mini app shows it as "бот не может написать вам" (bot_available).
type BotStatusRequest struct {
	// MaxUserID is the user, from the MAX update.
	MaxUserID int64
	// Available is true when the bot can write to the user.
	Available bool
	// Reason is the MAX event that changed the status.
	Reason BotStatusReason
	// OccurredAt is when it happened, per MAX.
	OccurredAt time.Time
	// RequestID correlates the call with the bot's logs.
	RequestID string
}

// Validate checks the invariants every gateway implementation relies on.
func (r BotStatusRequest) Validate() error {
	if r.MaxUserID == 0 {
		return NewError(CodeForbidden, "max_user_id is required", 0, nil)
	}
	switch r.Reason {
	case ReasonBotStarted, ReasonBotStopped, ReasonDialogRemoved:
	default:
		return NewError(CodeConflict, fmt.Sprintf("unknown bot status reason %q", r.Reason), 0, nil)
	}
	return nil
}

// ResultStatus is the outcome of an action as far as the user is concerned.
type ResultStatus string

const (
	// StatusAccepted means the backend applied the action.
	StatusAccepted ResultStatus = "accepted"
	// StatusNoop means the action was already in effect: pressing
	// "Приду" twice is not an error.
	StatusNoop ResultStatus = "noop"
)

// EventSummary lets the backend return fresh event details so the bot can
// rewrite the message with accurate data rather than echoing what it sent.
type EventSummary struct {
	ID       string
	Title    string
	StartsAt *time.Time
	Address  string
}

// ActionResult is a successful outcome.
type ActionResult struct {
	Status ResultStatus
	// Event, when non-nil, carries refreshed event details.
	Event *EventSummary
	// Message is optional backend-supplied text. The bot ignores it for
	// user-facing copy today (all copy lives in internal/render) but logs it.
	Message string
}

// Gateway is everything the bot needs from the Core Backend.
//
// Implementations: internal/core/stub (in-memory, always succeeds) and
// internal/core/httpgw (real HTTP calls). A third, "direct", implementation
// wrapping an application service is the intended path if bot and backend
// merge into one binary.
type Gateway interface {
	ConfirmRegistration(ctx context.Context, req ActionRequest) (*ActionResult, error)
	CancelRegistration(ctx context.Context, req ActionRequest) (*ActionResult, error)
	AcceptWaitlistOffer(ctx context.Context, req ActionRequest) (*ActionResult, error)
	DeclineWaitlistOffer(ctx context.Context, req ActionRequest) (*ActionResult, error)
	// ReportBotStatus tells the backend whether the bot can reach a user.
	// Best effort: the caller logs a failure and moves on, because nothing
	// the user did depends on it.
	ReportBotStatus(ctx context.Context, req BotStatusRequest) error
	// Mode names the implementation ("stub" or "http") for logs and /ready.
	Mode() string
	// Ping reports whether the backend is reachable. The stub always
	// succeeds; the HTTP adapter performs a cheap health call.
	Ping(ctx context.Context) error
}

// ErrorCode classifies a Core Backend failure.
//
// These codes are the bot's vocabulary for deciding what to tell the user.
// They map onto the business outcomes the spec calls out: a cancelled event, a
// registration that is already gone, an expired waitlist offer, and the
// backend simply being down.
type ErrorCode string

const (
	// CodeNotFound means the registration or event does not exist.
	CodeNotFound ErrorCode = "not_found"
	// CodeEventCancelled means the event was cancelled by the organiser.
	CodeEventCancelled ErrorCode = "event_cancelled"
	// CodeRegistrationCancelled means the registration is already cancelled.
	CodeRegistrationCancelled ErrorCode = "registration_cancelled"
	// CodeOfferExpired means the waitlist offer window has closed.
	CodeOfferExpired ErrorCode = "offer_expired"
	// CodeConflict is any other business rule refusal.
	CodeConflict ErrorCode = "conflict"
	// CodeForbidden means the bot is not allowed to act for this user.
	CodeForbidden ErrorCode = "forbidden"
	// CodeUnavailable means the backend could not be reached or failed.
	CodeUnavailable ErrorCode = "unavailable"
	// CodeInternal is an unexpected backend failure.
	CodeInternal ErrorCode = "internal"
)

// Error is a typed Core Backend failure.
type Error struct {
	Code ErrorCode
	// Message is for logs and developers, never shown to the user verbatim.
	Message    string
	StatusCode int
	Err        error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("core %s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("core %s: %s", e.Code, e.Message)
}

// Unwrap exposes the cause to errors.Is/As.
func (e *Error) Unwrap() error { return e.Err }

// IsBusiness reports whether the failure is a business rule refusal rather
// than an infrastructure problem. Business refusals deserve a specific,
// friendly message; infrastructure problems deserve "try again later".
func (e *Error) IsBusiness() bool {
	switch e.Code {
	case CodeNotFound, CodeEventCancelled, CodeRegistrationCancelled, CodeOfferExpired, CodeConflict, CodeForbidden:
		return true
	default:
		return false
	}
}

// CodeOf extracts the ErrorCode from err, defaulting to CodeInternal.
func CodeOf(err error) ErrorCode {
	var coreErr *Error
	if errors.As(err, &coreErr) {
		return coreErr.Code
	}
	return CodeInternal
}

// NewError builds a *Error.
func NewError(code ErrorCode, message string, status int, cause error) *Error {
	return &Error{Code: code, Message: message, StatusCode: status, Err: cause}
}

// Validate checks the invariants every gateway implementation relies on.
//
// Catching a missing registration id here means the stub and the HTTP adapter
// do not each need their own guard, and a bug shows up as a clear error rather
// than an empty path segment in a URL.
func (r ActionRequest) Validate() error {
	if r.RegistrationID == "" {
		return NewError(CodeConflict, "registration_id is required", 0, nil)
	}
	if r.MaxUserID == 0 {
		return NewError(CodeForbidden, "max_user_id is required", 0, nil)
	}
	return nil
}
