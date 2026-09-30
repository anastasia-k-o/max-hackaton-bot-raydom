package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/observability"
)

// maxNotificationBody caps the request body. A notification is a few hundred
// bytes; anything near this limit is a mistake or an attack.
const maxNotificationBody = 64 << 10

// notificationsHandler serves POST /api/v1/notifications.
//
// This endpoint is the contract between the Core Backend and the bot. Today
// Postman calls it; later the Core Backend calls it with exactly the same
// body. That substitution is the whole point, so the handler stays a thin
// decode-validate-delegate shell.
type notificationsHandler struct {
	service *app.NotificationService
}

func (h *notificationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, contracts.CodeMethodNotAllowed, "only POST is allowed on this endpoint")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxNotificationBody+1))
	if err != nil {
		writeError(w, r, contracts.CodeInvalidRequest, "request body could not be read")
		return
	}
	if len(body) > maxNotificationBody {
		writeError(w, r, contracts.CodePayloadTooLarge, "request body exceeds 64 KiB")
		return
	}

	var request contracts.NotificationRequestV1
	decoder := json.NewDecoder(newBytesReader(body))
	// Unknown fields are rejected so a typo in the Core Backend surfaces as a
	// clear 400 rather than a notification that silently loses a field.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, r, contracts.CodeInvalidRequest, "request body is not valid JSON for this contract: "+decodeHint(err))
		return
	}

	if err := request.Validate(); err != nil {
		if fieldErrs, ok := contracts.AsFieldErrors(err); ok {
			writeValidationError(w, r, fieldErrs)
			return
		}
		writeError(w, r, contracts.CodeInvalidRequest, err.Error())
		return
	}

	notification := request.ToDomain()
	result, err := h.service.Send(r.Context(), notification)
	if err != nil {
		h.writeSendError(w, r, err)
		return
	}

	response := contracts.NotificationResponseV1{
		RequestID: notification.RequestID,
		Status:    contracts.StatusSent,
		MessageID: result.MessageID,
	}
	if result.Duplicate {
		response.Status = contracts.StatusDuplicate
		response.Duplicate = true
		// 200 rather than 202: nothing new was accepted for processing.
		writeJSON(w, r, http.StatusOK, response)
		return
	}

	writeJSON(w, r, http.StatusAccepted, response)
}

// writeSendError maps a send failure onto the API error vocabulary.
//
// The caller needs to know whether retrying is worthwhile, so a MAX outage is
// a 502 upstream_unavailable while a rendering bug is a 500.
func (h *notificationsHandler) writeSendError(w http.ResponseWriter, r *http.Request, err error) {
	observability.LoggerFrom(r.Context()).Error("notification send failed",
		observability.KeyUpstreamError, err.Error(),
	)

	if errors.Is(err, app.ErrSendFailed) {
		// Checked first: a user who blocked the bot is not "MAX is down".
		// The backend must stop retrying and mark the user unreachable,
		// which a 502 would tell it to do the opposite of.
		if errors.Is(err, maxapi.ErrRecipientUnavailable) {
			writeError(w, r, contracts.CodeRecipientUnavailable,
				"MAX refused delivery: the user stopped the bot or has no dialog with it; retrying will not help")
			return
		}
		var maxErr *maxapi.Error
		if errors.As(err, &maxErr) && errors.Is(maxErr.Err, maxapi.ErrUnauthorized) {
			writeError(w, r, contracts.CodeUpstreamUnavailable, "MAX rejected the bot token; check MAX_BOT_TOKEN")
			return
		}
		writeError(w, r, contracts.CodeUpstreamUnavailable, "MAX API is unavailable, retry later")
		return
	}

	writeError(w, r, contracts.CodeInternalError, "notification could not be processed")
}

// decodeHint turns a json error into a short, safe explanation.
//
// A type error names the offending field, which is genuinely useful; anything
// else is reduced to a generic sentence rather than leaking internals.
func decodeHint(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field != "" {
			return "field " + typeErr.Field + " has the wrong type"
		}
		return "a field has the wrong type"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "malformed JSON"
	}
	// DisallowUnknownFields produces a plain error whose message names the
	// field; it is safe and helpful to pass through.
	return contracts.SanitizeMessage(err.Error(), 200)
}
