package http

import (
	"encoding/json"
	"net/http"

	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/observability"
)

// writeJSON encodes a value as a JSON response.
//
// The body is marshalled before the status is written so an encoding failure
// cannot produce a half-written 200.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		observability.LoggerFrom(r.Context()).Error("response encoding failed", "error", err.Error())
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"response encoding failed"}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// writeError emits the standard error envelope.
//
// message is developer-facing and must never carry a raw Go error string: the
// cause goes to the logs, the caller gets something intelligible.
func writeError(w http.ResponseWriter, r *http.Request, code contracts.ErrorCode, message string, details ...contracts.FieldError) {
	requestID := observability.RequestIDFrom(r.Context())
	response := contracts.NewErrorResponse(code, contracts.SanitizeMessage(message, 500), requestID, details...)
	writeJSON(w, r, code.HTTPStatus(), response)
}

// writeValidationError emits a 400 with per-field details.
func writeValidationError(w http.ResponseWriter, r *http.Request, errs contracts.FieldErrors) {
	writeError(w, r, contracts.CodeInvalidRequest, errs.Error(), errs.Details()...)
}
