package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/app"
	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/config"
	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/maxapi/mock"
	"hackatonBotMAX/internal/observability"
	"hackatonBotMAX/internal/render"
)

const (
	testInternalKey   = "test-internal-key"
	testWebhookSecret = "test-webhook-secret"
)

// harness is a fully wired bot running entirely in memory.
//
// It is the automated equivalent of the Postman flow from the specification:
// notification in, message recorded by MockMAX, simulated callback, action
// recorded by StubCore, message updated.
type harness struct {
	server   *httptest.Server
	mockMax  *mock.Client
	stubCore *stub.Gateway
	client   *http.Client
}

func newHarness(t *testing.T, appEnv config.AppEnv) *harness {
	t.Helper()

	cfg := config.Config{
		AppEnv:           appEnv,
		HTTPPort:         8080,
		LogLevel:         "error",
		MaxMode:          config.MaxModeMock,
		MaxUpdatesMode:   config.UpdatesModeWebhook,
		MaxWebhookSecret: testWebhookSecret,
		CoreMode:         config.CoreModeStub,
		InternalAPIKey:   testInternalKey,
		IdempotencyTTL:   time.Minute,
	}

	maxClient := mock.New()
	gateway := stub.New()
	renderer := render.New(render.WithDefaultMiniAppURL("https://example.ru/app"))
	store := idempotency.NewMemoryStore(time.Minute)

	deps := Deps{
		Config:              cfg,
		Logger:              observability.NewLogger("error"),
		Version:             "test",
		NotificationService: app.NewNotificationService(maxClient, renderer, store),
		BotService: app.NewBotService(maxClient, gateway, renderer, store,
			app.WithMiniAppURL("https://example.ru/app")),
		MaxClient:   maxClient,
		CoreGateway: gateway,
		MockMax:     maxClient,
		StubCore:    gateway,
	}

	server := httptest.NewServer(NewRouter(deps))
	t.Cleanup(server.Close)

	return &harness{server: server, mockMax: maxClient, stubCore: gateway, client: server.Client()}
}

func (h *harness) do(t *testing.T, method, path string, body any, headers map[string]string) *http.Response {
	t.Helper()

	var reader io.Reader
	switch typed := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (h *harness) postNotification(t *testing.T, body any) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost, PathNotifications, body, map[string]string{
		HeaderInternalAPIKey: testInternalKey,
	})
}

func (h *harness) postWebhook(t *testing.T, body any) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost, PathWebhook, body, map[string]string{
		HeaderMaxSecret: testWebhookSecret,
	})
}

func decodeBody(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func validNotification(notificationType domain.NotificationType, requestID string) map[string]any {
	body := map[string]any{
		"request_id": requestID,
		"type":       string(notificationType),
		"recipient":  map[string]any{"max_user_id": 123456789},
		"event": map[string]any{
			"id":           "event_42",
			"title":        "Йога в парке",
			"starts_at":    "2026-09-22T19:00:00+03:00",
			"address":      "Парк Горького",
			"mini_app_url": "https://example.ru/app/event_42",
		},
		"registration": map[string]any{"id": "registration_15"},
	}

	switch notificationType {
	case domain.NotificationWaitlistOffer:
		body["data"] = map[string]any{"offer_expires_at": "2026-09-22T16:00:00+03:00"}
	case domain.NotificationEventUpdated:
		delete(body, "registration")
		body["data"] = map[string]any{
			"changes": []map[string]any{{"field": "time", "old": "19:00", "new": "20:00"}},
		}
	case domain.NotificationEventCancelled:
		delete(body, "registration")
	}
	return body
}

// --- Health ---------------------------------------------------------------

func TestHealthIsAlwaysOK(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodGet, PathHealth, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	decodeBody(t, resp, &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
}

func TestReadyReportsBothDependencies(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodGet, PathReady, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 in mock mode", resp.StatusCode)
	}

	var body struct {
		Status       string             `json:"status"`
		Dependencies []dependencyStatus `json:"dependencies"`
	}
	decodeBody(t, resp, &body)

	if body.Status != "ready" {
		t.Errorf("status = %q", body.Status)
	}
	if len(body.Dependencies) != 2 {
		t.Fatalf("got %d dependencies, want 2", len(body.Dependencies))
	}

	modes := map[string]string{}
	for _, dep := range body.Dependencies {
		modes[dep.Name] = dep.Mode
		if dep.Status != "ok" {
			t.Errorf("dependency %q status = %q", dep.Name, dep.Status)
		}
	}
	if modes["max"] != "mock" || modes["core"] != "stub" {
		t.Errorf("modes = %v, want max=mock core=stub", modes)
	}
}

// TestReadyFailsWhenMaxIsUnhealthy proves the probe is real: a broken MAX
// client makes the bot report unready rather than quietly carrying on.
func TestReadyFailsWhenMaxIsUnhealthy(t *testing.T) {
	h := newHarness(t, config.EnvDev)
	h.mockMax.FailNext(&maxapi.Error{Op: "get_me", Err: maxapi.ErrUnauthorized, Message: "unauthorized"})

	resp := h.do(t, http.MethodGet, PathReady, nil, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}

	var body map[string]any
	decodeBody(t, resp, &body)
	if body["status"] != "not_ready" {
		t.Errorf("status = %v", body["status"])
	}
}

// --- Notifications API ----------------------------------------------------

// TestScenarioA is the specification's Scenario A: MAX_MODE=mock,
// CORE_MODE=stub, POST a notification, MockMAX receives a correct message.
func TestScenarioA(t *testing.T) {
	for _, notificationType := range domain.AllNotificationTypes() {
		t.Run(string(notificationType), func(t *testing.T) {
			h := newHarness(t, config.EnvDev)

			resp := h.postNotification(t, validNotification(notificationType, "req_"+string(notificationType)))
			if resp.StatusCode != http.StatusAccepted {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 202. body: %s", resp.StatusCode, body)
			}

			var accepted contracts.NotificationResponseV1
			decodeBody(t, resp, &accepted)
			if accepted.Status != contracts.StatusSent {
				t.Errorf("status = %q", accepted.Status)
			}
			if accepted.MessageID == "" {
				t.Error("expected a message id")
			}
			if accepted.RequestID != "req_"+string(notificationType) {
				t.Errorf("request id not echoed: %q", accepted.RequestID)
			}

			sent, ok := h.mockMax.LastSent()
			if !ok {
				t.Fatal("MockMAX recorded no message")
			}
			if sent.UserID != 123456789 {
				t.Errorf("sent to %d", sent.UserID)
			}
			if sent.Text == "" {
				t.Error("message has no text")
			}
		})
	}
}

func TestNotificationRejectsUnknownType(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	body := validNotification(domain.NotificationReminder24h, "req_1")
	body["type"] = "send_fireworks"

	resp := h.postNotification(t, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var errResp contracts.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != contracts.CodeInvalidRequest {
		t.Errorf("code = %q", errResp.Error.Code)
	}
	if errResp.Error.RequestID == "" {
		t.Error("the error should carry a request id")
	}

	found := false
	for _, detail := range errResp.Error.Details {
		if detail.Field == "type" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a detail on the type field, got %+v", errResp.Error.Details)
	}
	if len(h.mockMax.Records()) != 0 {
		t.Error("an invalid request must not reach MAX")
	}
}

func TestNotificationValidationDetails(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.postNotification(t, map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var errResp contracts.ErrorResponse
	decodeBody(t, resp, &errResp)
	if len(errResp.Error.Details) < 4 {
		t.Errorf("expected several field problems, got %+v", errResp.Error.Details)
	}
}

func TestNotificationRejectsMalformedJSON(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodPost, PathNotifications, "{not json", map[string]string{
		HeaderInternalAPIKey: testInternalKey,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestNotificationRejectsUnknownFields: a typo in the Core Backend should be a
// loud 400, not a silently dropped field.
func TestNotificationRejectsUnknownFields(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	body := validNotification(domain.NotificationReminder24h, "req_1")
	body["reciepient"] = map[string]any{"max_user_id": 1}

	resp := h.postNotification(t, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNotificationIsIdempotent(t *testing.T) {
	h := newHarness(t, config.EnvDev)
	body := validNotification(domain.NotificationReminder24h, "req_same")

	first := h.postNotification(t, body)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d", first.StatusCode)
	}

	second := h.postNotification(t, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200", second.StatusCode)
	}

	var duplicate contracts.NotificationResponseV1
	decodeBody(t, second, &duplicate)
	if !duplicate.Duplicate || duplicate.Status != contracts.StatusDuplicate {
		t.Errorf("response = %+v, want a duplicate", duplicate)
	}
	if len(h.mockMax.Records()) != 1 {
		t.Errorf("MockMAX got %d messages, want 1", len(h.mockMax.Records()))
	}
}

func TestNotificationReportsMaxOutageAsUpstreamUnavailable(t *testing.T) {
	h := newHarness(t, config.EnvDev)
	h.mockMax.FailNext(&maxapi.Error{Op: "send_message", Temporary: true, Message: "network failure"})

	resp := h.postNotification(t, validNotification(domain.NotificationReminder24h, "req_1"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}

	var errResp contracts.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != contracts.CodeUpstreamUnavailable {
		t.Errorf("code = %q", errResp.Error.Code)
	}
	// No Go error strings leak out.
	if strings.Contains(errResp.Error.Message, "maxapi.Error") {
		t.Errorf("internal detail leaked: %q", errResp.Error.Message)
	}
}

// TestNotificationReportsBlockedUserAsRecipientUnavailable: a user who stopped
// the bot is not a MAX outage. The backend gets 422 recipient_unavailable,
// which says "stop retrying and mark the user unreachable", instead of a 502
// that says the opposite. The request_id is released, so the same
// notification can go through once the user opens the dialog again.
func TestNotificationReportsBlockedUserAsRecipientUnavailable(t *testing.T) {
	h := newHarness(t, config.EnvDev)
	h.mockMax.FailNext(&maxapi.Error{
		Op:         "send_message",
		StatusCode: http.StatusForbidden,
		Err:        fmt.Errorf("%w: refused", maxapi.ErrRecipientUnavailable),
	})

	resp := h.postNotification(t, validNotification(domain.NotificationReminder24h, "req_blocked"))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	var errResp contracts.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != contracts.CodeRecipientUnavailable {
		t.Errorf("code = %q, want recipient_unavailable", errResp.Error.Code)
	}

	retry := h.postNotification(t, validNotification(domain.NotificationReminder24h, "req_blocked"))
	if retry.StatusCode != http.StatusAccepted {
		t.Errorf("retry after the user came back: status %d, want 202 (request_id must not stay burned)", retry.StatusCode)
	}
}

func TestNotificationRejectsWrongMethod(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodGet, PathNotifications, nil, map[string]string{
		HeaderInternalAPIKey: testInternalKey,
	})
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// --- Internal API key -----------------------------------------------------

func TestInternalAPIKeyIsEnforced(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{"missing key", nil},
		{"wrong key", map[string]string{HeaderInternalAPIKey: "nope"}},
		{"empty key", map[string]string{HeaderInternalAPIKey: ""}},
		{"webhook secret is not an api key", map[string]string{HeaderInternalAPIKey: testWebhookSecret}},
		{"right value on the wrong header", map[string]string{HeaderMaxSecret: testInternalKey}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, config.EnvDev)

			resp := h.do(t, http.MethodPost, PathNotifications,
				validNotification(domain.NotificationReminder24h, "req_1"), tc.headers)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}

			var errResp contracts.ErrorResponse
			decodeBody(t, resp, &errResp)
			if errResp.Error.Code != contracts.CodeUnauthorized {
				t.Errorf("code = %q", errResp.Error.Code)
			}
			if len(h.mockMax.Records()) != 0 {
				t.Error("an unauthorised request must not reach MAX")
			}
		})
	}
}

// --- Webhook --------------------------------------------------------------

func TestWebhookSecretIsEnforced(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{"missing secret", nil},
		{"wrong secret", map[string]string{HeaderMaxSecret: "nope"}},
		{"internal key is not a webhook secret", map[string]string{HeaderMaxSecret: testInternalKey}},
		{"right value on the wrong header", map[string]string{HeaderInternalAPIKey: testWebhookSecret}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, config.EnvDev)

			resp := h.do(t, http.MethodPost, PathWebhook, botStartedUpdate(42), tc.headers)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if len(h.mockMax.Records()) != 0 {
				t.Error("a rejected webhook must have no side effects")
			}
		})
	}
}

// TestScenarioB is the specification's Scenario B: a simulated
// message_callback makes the bot decode the action, call StubCore, and tell
// MockMAX to update the message — all without MAX or a backend.
func TestScenarioB(t *testing.T) {
	tests := []struct {
		name     string
		action   domain.ActionType
		wantKind stub.ActionKind
	}{
		{"confirm_registration", domain.ActionConfirmRegistration, stub.KindConfirmRegistration},
		{"cancel_registration", domain.ActionCancelRegistration, stub.KindCancelRegistration},
		{"accept_waitlist", domain.ActionAcceptWaitlist, stub.KindAcceptWaitlist},
		{"decline_waitlist", domain.ActionDeclineWaitlist, stub.KindDeclineWaitlist},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, config.EnvDev)

			// 1. The notification produces a message with buttons.
			notificationType := domain.NotificationConfirmationRequired
			if strings.Contains(tc.name, "waitlist") {
				notificationType = domain.NotificationWaitlistOffer
			}
			if resp := h.postNotification(t, validNotification(notificationType, "req_1")); resp.StatusCode != http.StatusAccepted {
				t.Fatalf("notification status = %d", resp.StatusCode)
			}

			sent, _ := h.mockMax.LastSent()
			payload := findPayload(t, sent, tc.action)

			// 2. MAX delivers the button press.
			resp := h.postWebhook(t, callbackUpdateBody(payload, 123456789))
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("webhook status = %d, body: %s", resp.StatusCode, body)
			}

			// 3. StubCore recorded the action.
			actions := h.stubCore.Actions()
			if len(actions) != 1 {
				t.Fatalf("StubCore recorded %d actions, want 1", len(actions))
			}
			if actions[0].Kind != tc.wantKind {
				t.Errorf("action kind = %q, want %q", actions[0].Kind, tc.wantKind)
			}
			if actions[0].RegistrationID != "registration_15" {
				t.Errorf("registration = %q", actions[0].RegistrationID)
			}
			if actions[0].MaxUserID != 123456789 {
				t.Errorf("max user = %d", actions[0].MaxUserID)
			}

			// 4. MockMAX recorded the message update.
			answer, _ := h.mockMax.LastRecord()
			if answer.Kind != mock.KindCallbackAnswer {
				t.Fatalf("last record kind = %q, want a callback answer", answer.Kind)
			}
			if answer.Text == "" {
				t.Error("the answer should replace the message")
			}
			for _, row := range answer.Keyboard {
				for _, button := range row {
					if button.Kind == string(domain.ButtonCallback) {
						t.Error("the updated message must not keep the action buttons")
					}
				}
			}
		})
	}
}

func TestWebhookBotStartedSendsGreeting(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.postWebhook(t, botStartedUpdate(123456789))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	sent, ok := h.mockMax.LastSent()
	if !ok {
		t.Fatal("no greeting sent")
	}
	if !strings.HasPrefix(sent.Text, "Привет!") {
		t.Errorf("unexpected greeting:\n%s", sent.Text)
	}
}

func TestWebhookMessageCreatedFallback(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.postWebhook(t, map[string]any{
		"update_type": "message_created",
		"timestamp":   time.Now().UnixMilli(),
		"message": map[string]any{
			"sender":    map[string]any{"user_id": 42, "name": "Тест"},
			"recipient": map[string]any{"chat_id": 555, "user_id": 42, "chat_type": "dialog"},
			"body":      map[string]any{"mid": "mid-1", "seq": 1, "text": "привет"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	sent, ok := h.mockMax.LastSent()
	if !ok {
		t.Fatal("no fallback reply sent")
	}
	if !strings.Contains(sent.Text, "мини-приложение") {
		t.Errorf("fallback should point at the mini app:\n%s", sent.Text)
	}
}

// TestWebhookUnknownUpdateTypesReturn200: a non-2xx would make MAX retry an
// event the bot will never understand.
func TestWebhookUnknownUpdateTypesReturn200(t *testing.T) {
	unknown := []string{"dialog_muted", "user_added", "chat_title_changed", "invented_in_2027"}

	for _, updateType := range unknown {
		t.Run(updateType, func(t *testing.T) {
			h := newHarness(t, config.EnvDev)

			resp := h.postWebhook(t, map[string]any{
				"update_type": updateType,
				"timestamp":   time.Now().UnixMilli(),
				"user":        map[string]any{"user_id": 42},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			var body map[string]any
			decodeBody(t, resp, &body)
			if body["status"] != "ignored" {
				t.Errorf("status = %v, want ignored", body["status"])
			}
			if len(h.mockMax.Records()) != 0 {
				t.Error("an unhandled update must have no side effects")
			}
		})
	}
}

// TestWebhookSurvivesHostilePayloads is the "no panic" acceptance criterion,
// exercised through the real HTTP stack.
func TestWebhookSurvivesHostilePayloads(t *testing.T) {
	bodies := []string{
		`{"update_type":"message_callback"}`,
		`{"update_type":"message_callback","callback":{}}`,
		`{"update_type":"message_callback","callback":{"callback_id":"cb1","payload":"Приду"}}`,
		`{"update_type":"message_callback","callback":{"callback_id":"cb2","payload":"v9|confirm|x"}}`,
		`{"update_type":"message_created"}`,
		`{"update_type":"bot_started"}`,
		`{"update_type":""}`,
		`{}`,
		`{"update_type":"message_callback","message":null,"callback":null}`,
		`null`,
		`[]`,
		`{"update_type":"message_callback","callback":{"callback_id":"cb3","payload":"` + strings.Repeat("x", 5000) + `"}}`,
	}

	for i, body := range bodies {
		t.Run(fmt.Sprintf("payload_%d", i), func(t *testing.T) {
			h := newHarness(t, config.EnvDev)

			resp := h.do(t, http.MethodPost, PathWebhook, body, map[string]string{
				HeaderMaxSecret: testWebhookSecret,
			})
			if resp.StatusCode >= 500 {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d (a hostile payload must not 5xx). body: %s", resp.StatusCode, raw)
			}
			if len(h.stubCore.Actions()) != 0 {
				t.Error("a malformed payload must not reach the Core Gateway")
			}
		})
	}
}

func TestWebhookDeduplicatesRedelivery(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
	body := callbackUpdateBody(payload, 123456789)

	if resp := h.postWebhook(t, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("first delivery: %d", resp.StatusCode)
	}

	resp := h.postWebhook(t, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("redelivery: %d", resp.StatusCode)
	}

	var decoded map[string]any
	decodeBody(t, resp, &decoded)
	if decoded["status"] != "duplicate" {
		t.Errorf("status = %v, want duplicate", decoded["status"])
	}
	if len(h.stubCore.Actions()) != 1 {
		t.Errorf("StubCore saw %d actions, want 1", len(h.stubCore.Actions()))
	}
}

func TestWebhookRejectsWrongMethod(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodGet, PathWebhook, nil, map[string]string{
		HeaderMaxSecret: testWebhookSecret,
	})
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// --- Dev endpoints --------------------------------------------------------

func TestDevEndpointsInDev(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	if resp := h.postNotification(t, validNotification(domain.NotificationReminder24h, "req_1")); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status = %d", resp.StatusCode)
	}

	t.Run("mock max messages", func(t *testing.T) {
		resp := h.do(t, http.MethodGet, PathDevMaxMessages, nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}

		var body struct {
			Count    int           `json:"count"`
			Messages []mock.Record `json:"messages"`
		}
		decodeBody(t, resp, &body)
		if body.Count != 1 || len(body.Messages) != 1 {
			t.Fatalf("count = %d, messages = %d", body.Count, len(body.Messages))
		}
		if body.Messages[0].Text == "" {
			t.Error("the recorded message should carry its text")
		}
		if len(body.Messages[0].Keyboard) == 0 {
			t.Error("the recorded message should carry its keyboard, so buttons are inspectable")
		}
	})

	t.Run("clear mock max messages", func(t *testing.T) {
		resp := h.do(t, http.MethodDelete, PathDevMaxMessages, nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if len(h.mockMax.Records()) != 0 {
			t.Error("the buffer should be empty")
		}
	})

	t.Run("stub core actions", func(t *testing.T) {
		payload := callback.MustEncode(domain.ActionConfirmRegistration, "registration_15", "event_42")
		if resp := h.postWebhook(t, callbackUpdateBody(payload, 123456789)); resp.StatusCode != http.StatusOK {
			t.Fatalf("webhook status = %d", resp.StatusCode)
		}

		resp := h.do(t, http.MethodGet, PathDevCoreActions, nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}

		var body struct {
			Count   int           `json:"count"`
			Actions []stub.Action `json:"actions"`
		}
		decodeBody(t, resp, &body)
		if body.Count != 1 {
			t.Fatalf("count = %d, want 1", body.Count)
		}
		if body.Actions[0].Kind != stub.KindConfirmRegistration {
			t.Errorf("kind = %q", body.Actions[0].Kind)
		}
	})

	t.Run("clear stub core actions", func(t *testing.T) {
		resp := h.do(t, http.MethodDelete, PathDevCoreActions, nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if len(h.stubCore.Actions()) != 0 {
			t.Error("the buffer should be empty")
		}
	})
}

// TestDevEndpointsAreAbsentInProduction is the acceptance criterion
// "dev endpoints выключены в production mode".
func TestDevEndpointsAreAbsentInProduction(t *testing.T) {
	h := newHarness(t, config.EnvProd)

	for _, path := range []string{PathDevMaxMessages, PathDevCoreActions} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			resp := h.do(t, method, path, nil, nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s status = %d, want 404 in production", method, path, resp.StatusCode)
			}
		}
	}
}

// --- Cross-cutting --------------------------------------------------------

func TestUnknownRouteUsesTheErrorEnvelope(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodGet, "/nope", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	var errResp contracts.ErrorResponse
	decodeBody(t, resp, &errResp)
	if errResp.Error.Code != contracts.CodeNotFound {
		t.Errorf("code = %q", errResp.Error.Code)
	}
}

// TestRequestIDPropagates: a caller-supplied id is honoured so a trace can
// span the Core Backend and the bot.
func TestRequestIDPropagates(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodPost, PathNotifications,
		validNotification(domain.NotificationReminder24h, "req_1"),
		map[string]string{
			HeaderInternalAPIKey: testInternalKey,
			HeaderRequestID:      "trace-abc-123",
		})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderRequestID); got != "trace-abc-123" {
		t.Errorf("%s = %q, want the supplied id", HeaderRequestID, got)
	}

	sent, _ := h.mockMax.LastSent()
	if sent.RequestID != "trace-abc-123" {
		t.Errorf("the correlation id did not reach the MAX adapter: %q", sent.RequestID)
	}
}

func TestRequestIDIsGeneratedWhenAbsent(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	resp := h.do(t, http.MethodGet, PathHealth, nil, nil)
	if got := resp.Header.Get(HeaderRequestID); got == "" {
		t.Error("a request id should always be assigned")
	}
}

// TestOversizedBodiesAreRejected guards both entry points against a body that
// would otherwise be read into memory in full.
func TestOversizedBodiesAreRejected(t *testing.T) {
	h := newHarness(t, config.EnvDev)

	huge := `{"request_id":"` + strings.Repeat("x", 70<<10) + `"}`
	resp := h.do(t, http.MethodPost, PathNotifications, huge, map[string]string{
		HeaderInternalAPIKey: testInternalKey,
	})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

// --- fixtures -------------------------------------------------------------

func botStartedUpdate(userID int64) map[string]any {
	return map[string]any{
		"update_type": "bot_started",
		"timestamp":   time.Now().UnixMilli(),
		"chat_id":     555,
		"user": map[string]any{
			"user_id":    userID,
			"first_name": "Тест",
			"name":       "Тест",
			"is_bot":     false,
		},
	}
}

// callbackUpdateBody mirrors the shape MAX actually posts for a button press.
func callbackUpdateBody(payload string, userID int64) map[string]any {
	return map[string]any{
		"update_type": "message_callback",
		"timestamp":   time.Now().UnixMilli(),
		"chat_id":     555,
		"message": map[string]any{
			"sender":    map[string]any{"user_id": 1, "is_bot": true},
			"recipient": map[string]any{"chat_id": 555, "user_id": userID, "chat_type": "dialog"},
			"body":      map[string]any{"mid": "mid-1", "seq": 1, "text": "Подтвердите участие"},
		},
		"callback": map[string]any{
			"timestamp":   time.Now().UnixMilli(),
			"callback_id": fmt.Sprintf("cb-%d-%s", time.Now().UnixNano(), payload),
			"payload":     payload,
			"user": map[string]any{
				"user_id":    userID,
				"first_name": "Тест",
				"name":       "Тест",
			},
		},
	}
}

// findPayload picks the payload for an action out of a recorded message. It
// deliberately matches on the decoded action, never on the button label.
func findPayload(t *testing.T, record mock.Record, action domain.ActionType) string {
	t.Helper()

	for _, row := range record.Keyboard {
		for _, button := range row {
			if button.Payload == "" {
				continue
			}
			decoded, err := callback.Decode(button.Payload)
			if err != nil {
				continue
			}
			if decoded.Action == action {
				return button.Payload
			}
		}
	}
	t.Fatalf("no button for action %q in %+v", action, record.Keyboard)
	return ""
}

// compile-time assurance that the stub satisfies the port the router needs.
var _ core.Gateway = (*stub.Gateway)(nil)
