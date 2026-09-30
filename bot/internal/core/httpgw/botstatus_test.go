package httpgw

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"hackatonBotMAX/internal/core"
)

// TestReportBotStatusShape pins the call the Core Backend has to implement:
// method, path, service key and body.
func TestReportBotStatusShape(t *testing.T) {
	var (
		method, path, apiKey, requestID string
		body                            map[string]any
	)
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		apiKey, requestID = r.Header.Get(HeaderAPIKey), r.Header.Get(HeaderRequestID)
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusNoContent)
	}))

	err := gateway.ReportBotStatus(context.Background(), core.BotStatusRequest{
		MaxUserID:  123456789,
		Available:  false,
		Reason:     core.ReasonBotStopped,
		OccurredAt: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		RequestID:  "req-1",
	})
	if err != nil {
		t.Fatalf("ReportBotStatus(): %v", err)
	}

	if method != http.MethodPost || path != "/max-users/123456789/bot-status" {
		t.Errorf("%s %s, want POST /max-users/123456789/bot-status", method, path)
	}
	if apiKey != "core-secret" || requestID != "req-1" {
		t.Errorf("headers: api key %q, request id %q", apiKey, requestID)
	}
	want := map[string]any{
		"max_user_id": float64(123456789),
		"available":   false,
		"reason":      "bot_stopped",
		"occurred_at": "2026-09-28T10:00:00Z",
		"request_id":  "req-1",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("body[%s] = %v, want %v", key, body[key], value)
		}
	}
	// available must be sent even when false: omitting it would read as
	// "unknown" rather than "the bot cannot reach this user".
	if _, present := body["available"]; !present {
		t.Error("available omitted when false")
	}
}

func TestReportBotStatusServerErrorIsUnavailable(t *testing.T) {
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	err := gateway.ReportBotStatus(context.Background(), core.BotStatusRequest{
		MaxUserID: 1, Available: true, Reason: core.ReasonBotStarted,
	})
	if core.CodeOf(err) != core.CodeUnavailable {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestReportBotStatusValidatesBeforeTheNetwork(t *testing.T) {
	called := false
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	for _, req := range []core.BotStatusRequest{
		{MaxUserID: 0, Reason: core.ReasonBotStarted},
		{MaxUserID: 1, Reason: "bot_teleported"},
	} {
		if err := gateway.ReportBotStatus(context.Background(), req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
	if called {
		t.Error("an invalid request reached the network")
	}
}
