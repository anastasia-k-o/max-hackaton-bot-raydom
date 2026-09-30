package domain

import "time"

// ActionType is a business action a user triggered by pressing a button.
//
// It is decoded from the callback payload (internal/callback) and is the only
// thing the application layer switches on. Button *text* is never used as a
// command: texts are localised and may change before the demo.
type ActionType string

const (
	// ActionConfirmRegistration means "I will attend".
	ActionConfirmRegistration ActionType = "confirm_registration"
	// ActionCancelRegistration means "I cannot make it".
	ActionCancelRegistration ActionType = "cancel_registration"
	// ActionAcceptWaitlist means "take the freed seat".
	ActionAcceptWaitlist ActionType = "accept_waitlist"
	// ActionDeclineWaitlist means "decline the freed seat".
	ActionDeclineWaitlist ActionType = "decline_waitlist"
)

// AllActionTypes lists every action the bot understands.
func AllActionTypes() []ActionType {
	return []ActionType{
		ActionConfirmRegistration,
		ActionCancelRegistration,
		ActionAcceptWaitlist,
		ActionDeclineWaitlist,
	}
}

// IsValid reports whether a is a supported action.
func (a ActionType) IsValid() bool {
	for _, known := range AllActionTypes() {
		if a == known {
			return true
		}
	}
	return false
}

// CallbackAction is a decoded user action, ready for the application layer.
//
// MaxUserID is taken from the webhook Update body (callback.user.user_id) and
// never from the payload: payloads travel through the client and must be
// treated as untrusted with respect to identity.
type CallbackAction struct {
	Action         ActionType
	RegistrationID string
	EventID        string

	MaxUserID  int64
	CallbackID string
	// MessageID is the mid of the message the button was attached to.
	MessageID string
	// RequestID correlates this action across logs and the CoreGateway call.
	RequestID string
	// OccurredAt is when MAX says the button was pressed. The backend needs
	// it to reason about an offer that may have expired between the press
	// and the delivery of the webhook.
	OccurredAt time.Time
}
