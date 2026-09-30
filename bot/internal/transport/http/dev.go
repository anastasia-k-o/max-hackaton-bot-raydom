package http

import (
	"net/http"

	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/maxapi/mock"
)

// Development-only inspection endpoints.
//
// These exist so the full loop — notification in, message recorded, simulated
// callback, action recorded, message updated — can be verified from Postman
// with no MAX token and no backend. They are mounted only when APP_ENV=dev.
// In prod the routes are never registered, so they return the router's normal
// 404 rather than merely refusing: an endpoint that does not exist cannot be
// probed for whether it exists.
//
// Nothing in production code paths reads these handlers or the buffers behind
// them; the mock and the stub are the only writers.

// Either backing buffer may be absent: with MAX_MODE=real there is no MockMAX
// to inspect, and with CORE_MODE=http there is no StubCore. The router
// registers only the routes whose buffer exists (see NewRouter).

// mockMessagesHandler serves GET/DELETE /dev/max/messages.
type mockMessagesHandler struct {
	client *mock.Client
}

func (h *mockMessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		records := h.client.Records()
		writeJSON(w, r, http.StatusOK, map[string]any{
			"count":    len(records),
			"messages": records,
		})
	case http.MethodDelete:
		removed := h.client.Reset()
		writeJSON(w, r, http.StatusOK, map[string]any{
			"status":  "cleared",
			"removed": removed,
		})
	default:
		writeError(w, r, contracts.CodeMethodNotAllowed, "only GET and DELETE are allowed on this endpoint")
	}
}

// stubActionsHandler serves GET/DELETE /dev/core/actions.
type stubActionsHandler struct {
	gateway *stub.Gateway
}

func (h *stubActionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		actions := h.gateway.Actions()
		writeJSON(w, r, http.StatusOK, map[string]any{
			"count":   len(actions),
			"actions": actions,
		})
	case http.MethodDelete:
		removed := h.gateway.Reset()
		writeJSON(w, r, http.StatusOK, map[string]any{
			"status":  "cleared",
			"removed": removed,
		})
	default:
		writeError(w, r, contracts.CodeMethodNotAllowed, "only GET and DELETE are allowed on this endpoint")
	}
}
