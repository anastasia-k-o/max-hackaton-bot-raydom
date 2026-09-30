package httpgw

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/core"
)

// The Core Backend does not exist yet, so these tests are the specification of
// what the bot will send it and how the bot reacts to every answer. When the
// real backend appears, a failing test here means the two sides disagree —
// which is exactly what this suite is for.

func testRequest() core.ActionRequest {
	return core.ActionRequest{
		RegistrationID: "registration_15",
		EventID:        "event_42",
		MaxUserID:      123456789,
		RequestID:      "req_123",
		OccurredAt:     time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC),
	}
}

func newTestGateway(t *testing.T, handler http.Handler) (*Gateway, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	gateway, err := New(Config{
		BaseURL:    server.URL,
		APIKey:     "core-secret",
		Timeout:    2 * time.Second,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return gateway, server
}

// TestRequestShape pins the HTTP call each action makes: method, path,
// headers and body.
func TestRequestShape(t *testing.T) {
	tests := []struct {
		name     string
		call     func(*Gateway, context.Context) (*core.ActionResult, error)
		wantPath string
	}{
		{
			name: "confirm",
			call: func(g *Gateway, ctx context.Context) (*core.ActionResult, error) {
				return g.ConfirmRegistration(ctx, testRequest())
			},
			wantPath: "/registrations/registration_15/confirm",
		},
		{
			name: "cancel",
			call: func(g *Gateway, ctx context.Context) (*core.ActionResult, error) {
				return g.CancelRegistration(ctx, testRequest())
			},
			wantPath: "/registrations/registration_15/cancel",
		},
		{
			name: "accept waitlist",
			call: func(g *Gateway, ctx context.Context) (*core.ActionResult, error) {
				return g.AcceptWaitlistOffer(ctx, testRequest())
			},
			wantPath: "/waitlist-offers/registration_15/accept",
		},
		{
			name: "decline waitlist",
			call: func(g *Gateway, ctx context.Context) (*core.ActionResult, error) {
				return g.DeclineWaitlistOffer(ctx, testRequest())
			},
			wantPath: "/waitlist-offers/registration_15/decline",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				gotMethod string
				gotPath   string
				gotAPIKey string
				gotReqID  string
				gotBody   requestBody
			)

			gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotAPIKey = r.Header.Get(HeaderAPIKey)
				gotReqID = r.Header.Get(HeaderRequestID)
				_ = json.NewDecoder(r.Body).Decode(&gotBody)

				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"status":"accepted"}`)
			}))

			result, err := tc.call(gateway, context.Background())
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if result.Status != core.StatusAccepted {
				t.Errorf("status = %q", result.Status)
			}

			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotAPIKey != "core-secret" {
				t.Errorf("%s = %q", HeaderAPIKey, gotAPIKey)
			}
			if gotReqID != "req_123" {
				t.Errorf("%s = %q, want the correlation id to propagate", HeaderRequestID, gotReqID)
			}

			// Every id the backend needs must be in the body too.
			if gotBody.RegistrationID != "registration_15" {
				t.Errorf("body registration_id = %q", gotBody.RegistrationID)
			}
			if gotBody.EventID != "event_42" {
				t.Errorf("body event_id = %q", gotBody.EventID)
			}
			if gotBody.MaxUserID != 123456789 {
				t.Errorf("body max_user_id = %d", gotBody.MaxUserID)
			}
			if gotBody.RequestID != "req_123" {
				t.Errorf("body request_id = %q", gotBody.RequestID)
			}
			if gotBody.OccurredAt != "2026-09-22T16:00:00Z" {
				t.Errorf("body occurred_at = %q, want RFC3339 UTC", gotBody.OccurredAt)
			}
		})
	}
}

func TestSuccessWithEventSummary(t *testing.T) {
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"status": "accepted",
			"message": "ok",
			"event": {
				"id": "event_42",
				"title": "Йога в парке",
				"starts_at": "2026-09-22T19:00:00+03:00",
				"address": "Парк Горького"
			}
		}`)
	}))

	result, err := gateway.ConfirmRegistration(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("ConfirmRegistration(): %v", err)
	}
	if result.Event == nil {
		t.Fatal("expected the event summary to be decoded")
	}
	if result.Event.Title != "Йога в парке" {
		t.Errorf("title = %q", result.Event.Title)
	}
	if result.Event.StartsAt == nil {
		t.Fatal("expected starts_at to be parsed")
	}
	if _, offset := result.Event.StartsAt.Zone(); offset != 3*60*60 {
		t.Errorf("starts_at lost its offset: %v", result.Event.StartsAt)
	}
}

// TestUTCStartsAtIsDropped: a UTC time from the backend would be printed hours
// off for the user, so the bot drops it and the reply goes out without a time.
// The action itself still succeeds.
func TestUTCStartsAtIsDropped(t *testing.T) {
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"status": "accepted",
			"event": {"id": "event_42", "title": "Йога в парке", "starts_at": "2026-09-22T16:00:00Z"}
		}`)
	}))

	result, err := gateway.ConfirmRegistration(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("ConfirmRegistration(): %v", err)
	}
	if result.Status != core.StatusAccepted {
		t.Errorf("status = %q, want accepted: a UTC time must not fail the action", result.Status)
	}
	if result.Event == nil || result.Event.Title != "Йога в парке" {
		t.Fatalf("the rest of the summary must survive: %+v", result.Event)
	}
	if result.Event.StartsAt != nil {
		t.Errorf("UTC starts_at kept (%v); it would be shown three hours off", result.Event.StartsAt)
	}
}

func TestNoopStatusIsPreserved(t *testing.T) {
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"noop"}`)
	}))

	result, err := gateway.ConfirmRegistration(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("ConfirmRegistration(): %v", err)
	}
	if result.Status != core.StatusNoop {
		t.Errorf("status = %q, want noop", result.Status)
	}
}

// TestEmptySuccessBodyIsStillSuccess: the action happened; failing the user
// over a missing body would be the wrong trade.
func TestEmptySuccessBodyIsStillSuccess(t *testing.T) {
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	result, err := gateway.CancelRegistration(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("CancelRegistration(): %v", err)
	}
	if result.Status != core.StatusAccepted {
		t.Errorf("status = %q", result.Status)
	}
}

// TestBusinessErrorsFrom4xx: each backend error code must reach the bot as the
// matching core.ErrorCode, because that is what selects the user's message.
func TestBusinessErrorsFrom4xx(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		bodyCode       string
		wantCode       core.ErrorCode
		wantIsBusiness bool
	}{
		{"event cancelled", 409, "event_cancelled", core.CodeEventCancelled, true},
		{"american spelling", 409, "event_canceled", core.CodeEventCancelled, true},
		{"registration cancelled", 409, "registration_cancelled", core.CodeRegistrationCancelled, true},
		{"offer expired", 410, "offer_expired", core.CodeOfferExpired, true},
		{"waitlist offer expired alias", 410, "waitlist_offer_expired", core.CodeOfferExpired, true},
		{"not found by code", 404, "registration_not_found", core.CodeNotFound, true},
		{"forbidden", 403, "forbidden", core.CodeForbidden, true},
		{"plain conflict", 409, "conflict", core.CodeConflict, true},
		{"unknown code falls back to the status", 404, "something_new", core.CodeNotFound, true},
		{"unknown code and status", 400, "", core.CodeConflict, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"code":"`+tc.bodyCode+`","message":"backend says no"}}`)
			}))

			_, err := gateway.ConfirmRegistration(context.Background(), testRequest())
			if err == nil {
				t.Fatal("expected an error")
			}

			var coreErr *core.Error
			if !errors.As(err, &coreErr) {
				t.Fatalf("error is %T, want *core.Error", err)
			}
			if coreErr.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", coreErr.Code, tc.wantCode)
			}
			if coreErr.IsBusiness() != tc.wantIsBusiness {
				t.Errorf("IsBusiness() = %v, want %v", coreErr.IsBusiness(), tc.wantIsBusiness)
			}
			if coreErr.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", coreErr.StatusCode, tc.status)
			}
		})
	}
}

// TestServerErrorsAreUnavailable: a 5xx means the bot cannot know the outcome,
// so it must not claim the action succeeded — and the user gets "try later",
// not "you cannot do that".
func TestServerErrorsAreUnavailable(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"internal","message":"boom"}}`)
			}))

			_, err := gateway.AcceptWaitlistOffer(context.Background(), testRequest())
			if core.CodeOf(err) != core.CodeUnavailable {
				t.Fatalf("code = %q, want unavailable (err: %v)", core.CodeOf(err), err)
			}

			var coreErr *core.Error
			if errors.As(err, &coreErr) && coreErr.IsBusiness() {
				t.Error("a 5xx must not be treated as a business refusal")
			}
		})
	}
}

// TestTimeoutIsUnavailable covers the case where the backend simply never
// answers.
func TestTimeoutIsUnavailable(t *testing.T) {
	// The handler blocks until the test releases it. `defer close(release)`
	// runs before t.Cleanup(server.Close), so the server always drains.
	release := make(chan struct{})
	defer close(release)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(server.Close)

	gateway, err := New(Config{
		BaseURL:    server.URL,
		APIKey:     "core-secret",
		Timeout:    100 * time.Millisecond,
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	start := time.Now()
	_, err = gateway.ConfirmRegistration(context.Background(), testRequest())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout")
	}
	if core.CodeOf(err) != core.CodeUnavailable {
		t.Errorf("code = %q, want unavailable", core.CodeOf(err))
	}
	if elapsed > 2*time.Second {
		t.Errorf("the call took %v; the timeout is not being enforced", elapsed)
	}
}

// TestContextCancellationIsHonoured proves the gateway respects a deadline
// tighter than its own.
func TestContextCancellationIsHonoured(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(server.Close)

	gateway, err := New(Config{BaseURL: server.URL, Timeout: 10 * time.Second, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := gateway.ConfirmRegistration(ctx, testRequest()); core.CodeOf(err) != core.CodeUnavailable {
		t.Fatalf("code = %q, want unavailable (err: %v)", core.CodeOf(err), err)
	}
}

func TestUnreachableBackendIsUnavailable(t *testing.T) {
	// Port 0 on the loopback interface never accepts a connection.
	gateway, err := New(Config{BaseURL: "http://127.0.0.1:1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if _, err := gateway.DeclineWaitlistOffer(context.Background(), testRequest()); core.CodeOf(err) != core.CodeUnavailable {
		t.Fatalf("code = %q, want unavailable (err: %v)", core.CodeOf(err), err)
	}
}

func TestPing(t *testing.T) {
	t.Run("healthy backend", func(t *testing.T) {
		gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				t.Errorf("ping path = %q, want /health", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		if err := gateway.Ping(context.Background()); err != nil {
			t.Errorf("Ping(): %v", err)
		}
	})

	t.Run("unhealthy backend", func(t *testing.T) {
		gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		if err := gateway.Ping(context.Background()); core.CodeOf(err) != core.CodeUnavailable {
			t.Errorf("code = %q, want unavailable", core.CodeOf(err))
		}
	})
}

// TestValidationHappensBeforeTheNetwork: a missing id should not reach the
// backend as an empty path segment.
func TestValidationHappensBeforeTheNetwork(t *testing.T) {
	called := false
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := testRequest()
	req.RegistrationID = ""

	if _, err := gateway.ConfirmRegistration(context.Background(), req); err == nil {
		t.Fatal("expected a validation error")
	}
	if called {
		t.Error("the backend should not have been called")
	}
}

func TestRegistrationIDIsPathEscaped(t *testing.T) {
	var gotPath string
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))

	req := testRequest()
	req.RegistrationID = "reg/15 with spaces"

	if _, err := gateway.ConfirmRegistration(context.Background(), req); err != nil {
		t.Fatalf("ConfirmRegistration(): %v", err)
	}
	if strings.Contains(gotPath, "reg/15") {
		t.Errorf("the id was not escaped into the path: %q", gotPath)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("an empty base url should be rejected")
	}
}

// TestInjectedClientGetsATimeout guards against an unbounded call sneaking in
// through a caller-supplied http.Client.
func TestInjectedClientGetsATimeout(t *testing.T) {
	gateway, err := New(Config{
		BaseURL:    "https://example.ru",
		Timeout:    3 * time.Second,
		HTTPClient: &http.Client{}, // no timeout set
	})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if gateway.client.Timeout != 3*time.Second {
		t.Errorf("client timeout = %v, want 3s", gateway.client.Timeout)
	}
}

func TestModeIsHTTP(t *testing.T) {
	gateway, _ := newTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	if gateway.Mode() != "http" {
		t.Errorf("Mode() = %q", gateway.Mode())
	}
}
