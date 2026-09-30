package contracts

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// One error shape for the whole HTTP API.
//
//	{"error": {"code": "...", "message": "...", "request_id": "...", "details": [...]}}
//
// `code` is machine-readable and stable; `message` is for the developer
// reading the response in Postman. Internal Go error strings never appear
// here: they go to the logs, where they belong.

// ErrorCode is the stable, machine-readable error classification.
type ErrorCode string

const (
	// CodeInvalidRequest covers malformed JSON and failed field validation.
	CodeInvalidRequest ErrorCode = "invalid_request"
	// CodeUnauthorized means a missing or wrong API key / webhook secret.
	CodeUnauthorized ErrorCode = "unauthorized"
	// CodeNotFound is an unknown route.
	CodeNotFound ErrorCode = "not_found"
	// CodeMethodNotAllowed is the wrong HTTP verb.
	CodeMethodNotAllowed ErrorCode = "method_not_allowed"
	// CodeConflict is a business rule refusal from upstream.
	CodeConflict ErrorCode = "conflict"
	// CodeUpstreamUnavailable means MAX or the Core Backend could not be
	// reached. It is deliberately distinct from internal_error: the caller
	// may retry it.
	CodeUpstreamUnavailable ErrorCode = "upstream_unavailable"
	// CodeRecipientUnavailable means MAX refused to deliver to this user: they
	// stopped or blocked the bot, or never opened a dialog with it. Unlike
	// upstream_unavailable, retrying will not help until the user opens the
	// dialog again, so the Core Backend should stop retrying and mark the
	// user as unreachable.
	CodeRecipientUnavailable ErrorCode = "recipient_unavailable"
	// CodePayloadTooLarge is a body over the size limit.
	CodePayloadTooLarge ErrorCode = "payload_too_large"
	// CodeInternalError is an unexpected failure inside the bot.
	CodeInternalError ErrorCode = "internal_error"
)

// HTTPStatus maps an error code onto its HTTP status.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case CodeInvalidRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case CodeConflict:
		return http.StatusConflict
	case CodeUpstreamUnavailable:
		return http.StatusBadGateway
	case CodeRecipientUnavailable:
		return http.StatusUnprocessableEntity
	case CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

// ErrorResponse is the top-level error envelope.
type ErrorResponse struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload is the error itself.
type ErrorPayload struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id,omitempty"`
	// Details lists per-field problems for validation failures.
	Details []FieldError `json:"details,omitempty"`
}

// FieldError is one field-level validation problem.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// NewErrorResponse builds an envelope.
func NewErrorResponse(code ErrorCode, message, requestID string, details ...FieldError) ErrorResponse {
	return ErrorResponse{Error: ErrorPayload{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		Details:   details,
	}}
}

// FieldErrors accumulates validation problems keyed by field path.
type FieldErrors map[string]string

// Add records a problem for a field. The first problem for a field wins, so
// the most specific check should run first.
func (e FieldErrors) Add(field, message string) {
	if _, exists := e[field]; !exists {
		e[field] = message
	}
}

// Empty reports whether anything was recorded.
func (e FieldErrors) Empty() bool { return len(e) == 0 }

// Error implements error. The summary names the first field in sorted order so
// the message is deterministic across runs.
func (e FieldErrors) Error() string {
	if len(e) == 0 {
		return "validation failed"
	}
	details := e.Details()
	first := details[0]
	if len(details) == 1 {
		return fmt.Sprintf("%s %s", first.Field, first.Message)
	}
	return fmt.Sprintf("%s %s (and %d more problem(s))", first.Field, first.Message, len(details)-1)
}

// Details returns the problems as a stable, sorted slice.
func (e FieldErrors) Details() []FieldError {
	fields := make([]string, 0, len(e))
	for field := range e {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	details := make([]FieldError, 0, len(fields))
	for _, field := range fields {
		details = append(details, FieldError{Field: field, Message: e[field]})
	}
	return details
}

// AsFieldErrors extracts FieldErrors from err, if that is what it is.
func AsFieldErrors(err error) (FieldErrors, bool) {
	fieldErrs, ok := err.(FieldErrors)
	return fieldErrs, ok
}

// SanitizeMessage strips newlines and truncates, so a message derived from an
// upstream response cannot inject log lines or return a wall of text.
func SanitizeMessage(message string, limit int) string {
	cleaned := strings.Join(strings.Fields(message), " ")
	if limit > 0 && len([]rune(cleaned)) > limit {
		return string([]rune(cleaned)[:limit]) + "…"
	}
	return cleaned
}
