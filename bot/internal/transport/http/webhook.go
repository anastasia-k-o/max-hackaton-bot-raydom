package http

import (
	"bytes"
	"io"
	"net/http"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/observability"
)

// maxWebhookBody caps the webhook body.
const maxWebhookBody = 256 << 10

// webhookHandler serves POST /webhooks/max.
//
// Status codes here are a protocol decision, not a cosmetic one. MAX retries
// on a non-2xx, so the handler returns 200 for anything it has finished with —
// including events it does not handle and payloads it could not parse — and
// reserves non-2xx for "this delivery was not legitimate" (401) or "something
// on our side genuinely failed and a retry might work" (502).
//
// A webhook that a retry can never fix must not be retried forever.
type webhookHandler struct {
	service *app.BotService
}

func (h *webhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, contracts.CodeMethodNotAllowed, "only POST is allowed on this endpoint")
		return
	}

	logger := observability.LoggerFrom(r.Context())

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		logger.Warn("webhook body could not be read", observability.KeyUpstreamError, err.Error())
		writeError(w, r, contracts.CodeInvalidRequest, "request body could not be read")
		return
	}
	if len(body) > maxWebhookBody {
		writeError(w, r, contracts.CodePayloadTooLarge, "request body exceeds 256 KiB")
		return
	}

	update, err := maxapi.DecodeUpdate(body)
	if err != nil {
		// Acknowledge and drop: MAX cannot fix a malformed body by sending
		// it again, and 4xx here would produce an endless retry loop.
		logger.Warn("webhook update could not be decoded", observability.KeyUpstreamError, err.Error())
		writeJSON(w, r, http.StatusOK, map[string]any{"status": "ignored", "reason": "undecodable_update"})
		return
	}

	outcome, err := h.service.HandleUpdate(r.Context(), update)
	if err != nil {
		// The bot understood the update but could not complete it, usually
		// because MAX itself was unreachable. A retry may legitimately help.
		logger.Error("webhook processing failed",
			observability.KeyUpdateType, string(update.Type),
			observability.KeyUpstreamError, err.Error(),
		)
		writeError(w, r, contracts.CodeUpstreamUnavailable, "update could not be processed, retry later")
		return
	}

	status := "handled"
	switch {
	case outcome.Duplicate:
		status = "duplicate"
	case !outcome.Handled:
		status = "ignored"
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"status":      status,
		"update_type": string(update.Type),
		"result":      outcome.Result,
	})
}

// newBytesReader is a tiny helper so notifications.go can decode from a byte
// slice it already read for the size check.
func newBytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
