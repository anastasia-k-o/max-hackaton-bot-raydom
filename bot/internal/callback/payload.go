// Package callback encodes and decodes inline-button payloads.
//
// Wire format (version 1):
//
//	v1|<action>|<registration_id>[|<event_id>]
//
// Design notes:
//
//   - The payload is versioned so the format can change without breaking
//     buttons already sitting in users' chats. Decode dispatches on the
//     leading version token and returns ErrUnsupportedVersion for anything it
//     does not know.
//   - Button *text* is never a command. Texts are localised and will be
//     tweaked before a demo; payloads are stable machine identifiers.
//   - '|' is the separator because the MAX SDK's own CallbackPayload helper
//     splits on ':'; using '|' keeps our format independent of that helper.
//   - MAX limits a callback payload to 1024 bytes (schema.yaml,
//     CallbackButton.payload maxLength). Encode enforces that limit so an
//     oversized id fails fast at render time rather than producing a button
//     that MAX silently rejects.
package callback

import (
	"errors"
	"fmt"
	"strings"

	"hackatonBotMAX/internal/domain"
)

const (
	// Version1 is the current payload version token.
	Version1 = "v1"
	// separator splits payload fields.
	separator = "|"
	// MaxPayloadLen is the MAX API limit for a callback button payload.
	MaxPayloadLen = 1024
)

// Errors returned by Decode and Encode. Callers should match with errors.Is.
var (
	// ErrEmptyPayload is returned for an empty or whitespace-only payload.
	ErrEmptyPayload = errors.New("callback: empty payload")
	// ErrMalformed is returned when the payload has the wrong shape.
	ErrMalformed = errors.New("callback: malformed payload")
	// ErrUnsupportedVersion is returned for an unknown version token.
	ErrUnsupportedVersion = errors.New("callback: unsupported payload version")
	// ErrUnknownAction is returned for a well-formed payload naming an
	// action this build does not implement.
	ErrUnknownAction = errors.New("callback: unknown action")
	// ErrPayloadTooLong is returned when the encoded payload exceeds the
	// MAX API limit.
	ErrPayloadTooLong = errors.New("callback: payload exceeds MAX limit")
	// ErrInvalidField is returned when an id contains the separator or is
	// empty where it is required.
	ErrInvalidField = errors.New("callback: invalid field")
)

// actionCodes maps domain actions onto short, stable wire tokens.
//
// Short codes keep payloads well inside the 1024-byte budget even with long
// registration ids. They are part of the wire format: change a code only
// together with the version token.
var actionCodes = map[domain.ActionType]string{
	domain.ActionConfirmRegistration: "confirm",
	domain.ActionCancelRegistration:  "cancel",
	domain.ActionAcceptWaitlist:      "wl_accept",
	domain.ActionDeclineWaitlist:     "wl_decline",
}

// codeActions is the reverse of actionCodes, built once at init.
var codeActions = func() map[string]domain.ActionType {
	m := make(map[string]domain.ActionType, len(actionCodes))
	for action, code := range actionCodes {
		m[code] = action
	}
	return m
}()

// Payload is the decoded, still transport-level content of a callback button.
//
// It intentionally carries no user identity: the MAX user id comes from the
// webhook Update body, not from data that round-tripped through the client.
type Payload struct {
	Version        string
	Action         domain.ActionType
	RegistrationID string
	EventID        string
}

// Encode renders an action and its ids into a v1 payload string.
//
// registrationID is required and must not contain the separator; eventID is
// optional and is omitted when empty.
func Encode(action domain.ActionType, registrationID, eventID string) (string, error) {
	code, ok := actionCodes[action]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	if err := validateField("registration_id", registrationID, true); err != nil {
		return "", err
	}
	if err := validateField("event_id", eventID, false); err != nil {
		return "", err
	}

	parts := []string{Version1, code, registrationID}
	if eventID != "" {
		parts = append(parts, eventID)
	}
	encoded := strings.Join(parts, separator)

	if len(encoded) > MaxPayloadLen {
		return "", fmt.Errorf("%w: %d > %d bytes", ErrPayloadTooLong, len(encoded), MaxPayloadLen)
	}
	return encoded, nil
}

// MustEncode is Encode for cases where the inputs are known-good, such as
// tests and fixtures. It panics on error.
func MustEncode(action domain.ActionType, registrationID, eventID string) string {
	encoded, err := Encode(action, registrationID, eventID)
	if err != nil {
		panic(err)
	}
	return encoded
}

// Decode parses a payload produced by Encode.
//
// Every failure mode returns a typed error rather than panicking: a stale
// button from an older bot version, a truncated payload, or a hand-crafted
// value from a curious user must all end in a polite message, not a crash.
func Decode(raw string) (Payload, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Payload{}, ErrEmptyPayload
	}

	parts := strings.Split(trimmed, separator)
	version := parts[0]
	if version != Version1 {
		return Payload{}, fmt.Errorf("%w: %q", ErrUnsupportedVersion, version)
	}
	if len(parts) < 3 || len(parts) > 4 {
		return Payload{}, fmt.Errorf("%w: expected 3 or 4 fields, got %d", ErrMalformed, len(parts))
	}

	action, ok := codeActions[parts[1]]
	if !ok {
		return Payload{}, fmt.Errorf("%w: %q", ErrUnknownAction, parts[1])
	}

	registrationID := parts[2]
	if registrationID == "" {
		return Payload{}, fmt.Errorf("%w: registration_id is empty", ErrMalformed)
	}

	payload := Payload{
		Version:        version,
		Action:         action,
		RegistrationID: registrationID,
	}
	if len(parts) == 4 {
		payload.EventID = parts[3]
	}
	return payload, nil
}

// validateField rejects ids that would corrupt the encoding.
func validateField(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%w: %s is required", ErrInvalidField, name)
		}
		return nil
	}
	if strings.Contains(value, separator) {
		return fmt.Errorf("%w: %s must not contain %q", ErrInvalidField, name, separator)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s must not have surrounding whitespace", ErrInvalidField, name)
	}
	return nil
}
