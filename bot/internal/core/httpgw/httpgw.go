// Package httpgw implements core.Gateway over HTTP.
//
// The Core Backend does not exist yet, so this adapter is written against the
// contract documented in docs/INTEGRATION_MINIAPP.md and tested against
// httptest.Server. Writing it now is what keeps CORE_MODE=http an honest
// switch rather than a promise: when the backend appears, the work is
// confirming the paths match, not designing an integration from scratch.
//
// Contract implemented here:
//
//	POST {CORE_BASE_URL}/registrations/{registration_id}/confirm
//	POST {CORE_BASE_URL}/registrations/{registration_id}/cancel
//	POST {CORE_BASE_URL}/waitlist-offers/{registration_id}/accept
//	POST {CORE_BASE_URL}/waitlist-offers/{registration_id}/decline
//	GET  {CORE_BASE_URL}/health        (readiness only)
//
// Auth: X-Api-Key: {CORE_API_KEY}. Correlation: X-Request-Id.
package httpgw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/observability"
)

// Header names used when calling the Core Backend.
const (
	// HeaderAPIKey authenticates the bot to the Core Backend. It is a
	// different mechanism from the internal API key protecting the bot's own
	// endpoints, and from the MAX webhook secret.
	HeaderAPIKey = "X-Api-Key"
	// HeaderRequestID propagates the correlation id.
	HeaderRequestID = "X-Request-Id"
)

// maxErrorBody bounds how much of an error response is read, so a backend
// returning an HTML error page cannot blow up the bot's memory or logs.
const maxErrorBody = 8 << 10

// Config configures the HTTP gateway.
type Config struct {
	// BaseURL is the Core Backend root, for example https://api.example.ru/v1.
	BaseURL string
	// APIKey authenticates the bot. Empty means no auth header is sent.
	APIKey string
	// Timeout bounds each call.
	Timeout time.Duration
	// HTTPClient overrides the transport. Tests inject the one from
	// httptest.Server.
	HTTPClient *http.Client
}

// Gateway is the HTTP-backed core.Gateway.
type Gateway struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// requestBody is the JSON sent to the Core Backend.
//
// The ids also travel in the URL path, but repeating them in the body makes
// backend-side logging and replay straightforward, and makes the payload
// self-contained if the backend later moves to a single RPC endpoint.
type requestBody struct {
	RegistrationID string `json:"registration_id"`
	EventID        string `json:"event_id,omitempty"`
	MaxUserID      int64  `json:"max_user_id"`
	RequestID      string `json:"request_id,omitempty"`
	OccurredAt     string `json:"occurred_at,omitempty"`
}

// responseBody is the JSON expected back on success.
type responseBody struct {
	Status  string        `json:"status"`
	Message string        `json:"message,omitempty"`
	Event   *eventSummary `json:"event,omitempty"`
}

type eventSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	Address  string `json:"address"`
}

// errorBody is the JSON expected back on a 4xx.
//
// It mirrors the bot's own error envelope, so both sides of the integration
// speak the same shape.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// New builds an HTTP core gateway.
func New(cfg Config) (*Gateway, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("httpgw: base url is required")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("httpgw: invalid base url %q: %w", base, err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else if client.Timeout == 0 {
		// Respect an injected client but never leave it unbounded.
		cloned := *client
		cloned.Timeout = timeout
		client = &cloned
	}

	return &Gateway{baseURL: base, apiKey: strings.TrimSpace(cfg.APIKey), client: client}, nil
}

// Mode implements core.Gateway.
func (g *Gateway) Mode() string { return "http" }

// Ping performs a cheap readiness check against the backend.
func (g *Gateway) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+"/health", nil)
	if err != nil {
		return core.NewError(core.CodeUnavailable, "build health request", 0, err)
	}
	g.setHeaders(req, "")

	resp, err := g.client.Do(req)
	if err != nil {
		return core.NewError(core.CodeUnavailable, "core backend unreachable", 0, err)
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return core.NewError(core.CodeUnavailable, fmt.Sprintf("core health returned %d", resp.StatusCode), resp.StatusCode, nil)
	}
	return nil
}

// ConfirmRegistration confirms attendance.
func (g *Gateway) ConfirmRegistration(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.call(ctx, "registrations", req.RegistrationID, "confirm", req)
}

// CancelRegistration cancels a registration.
func (g *Gateway) CancelRegistration(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.call(ctx, "registrations", req.RegistrationID, "cancel", req)
}

// AcceptWaitlistOffer takes a freed seat.
func (g *Gateway) AcceptWaitlistOffer(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.call(ctx, "waitlist-offers", req.RegistrationID, "accept", req)
}

// DeclineWaitlistOffer declines a freed seat.
func (g *Gateway) DeclineWaitlistOffer(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.call(ctx, "waitlist-offers", req.RegistrationID, "decline", req)
}

// botStatusBody is the JSON body of POST /max-users/{max_user_id}/bot-status.
type botStatusBody struct {
	MaxUserID  int64  `json:"max_user_id"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason"`
	OccurredAt string `json:"occurred_at,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

// ReportBotStatus implements core.Gateway.
//
//	POST {CORE_BASE_URL}/max-users/{max_user_id}/bot-status
//	{"max_user_id": 123, "available": false, "reason": "bot_stopped", ...}
//
// Any 2xx is success and the body is ignored. The status is idempotent by
// nature (the latest report wins), so the backend needs no deduplication.
func (g *Gateway) ReportBotStatus(ctx context.Context, req core.BotStatusRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/max-users/%d/bot-status", g.baseURL, req.MaxUserID)
	payload := botStatusBody{
		MaxUserID: req.MaxUserID,
		Available: req.Available,
		Reason:    string(req.Reason),
		RequestID: req.RequestID,
	}
	if !req.OccurredAt.IsZero() {
		payload.OccurredAt = req.OccurredAt.UTC().Format(time.RFC3339)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return core.NewError(core.CodeInternal, "encode bot status", 0, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return core.NewError(core.CodeInternal, "build bot status request", 0, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	g.setHeaders(httpReq, firstNonEmpty(req.RequestID, observability.RequestIDFrom(ctx)))

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return core.NewError(core.CodeUnavailable, "core backend unreachable", 0, err)
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return core.NewError(core.CodeUnavailable,
			fmt.Sprintf("core backend returned %d for bot status", resp.StatusCode), resp.StatusCode, nil)
	}
	return nil
}

// call performs one action request and translates the outcome.
func (g *Gateway) call(ctx context.Context, collection, id, action string, actionReq core.ActionRequest) (*core.ActionResult, error) {
	if err := actionReq.Validate(); err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/%s/%s/%s", g.baseURL, collection, url.PathEscape(id), action)

	payload := requestBody{
		RegistrationID: actionReq.RegistrationID,
		EventID:        actionReq.EventID,
		MaxUserID:      actionReq.MaxUserID,
		RequestID:      actionReq.RequestID,
	}
	if !actionReq.OccurredAt.IsZero() {
		payload.OccurredAt = actionReq.OccurredAt.UTC().Format(time.RFC3339)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, core.NewError(core.CodeInternal, "encode core request", 0, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, core.NewError(core.CodeInternal, "build core request", 0, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	g.setHeaders(httpReq, firstNonEmpty(actionReq.RequestID, observability.RequestIDFrom(ctx)))

	resp, err := g.client.Do(httpReq)
	if err != nil {
		// A timeout and a refused connection are both "unavailable" as far
		// as the user is concerned: the bot cannot know the outcome, so it
		// must not claim the action succeeded.
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, core.NewError(core.CodeUnavailable, "core backend timed out", 0, err)
		}
		return nil, core.NewError(core.CodeUnavailable, "core backend unreachable", 0, err)
	}
	defer drainAndClose(resp.Body)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return decodeSuccess(resp.Body)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return nil, decodeBusinessError(resp)
	default:
		return nil, core.NewError(
			core.CodeUnavailable,
			fmt.Sprintf("core backend returned %d", resp.StatusCode),
			resp.StatusCode,
			nil,
		)
	}
}

// decodeSuccess parses a 2xx body. An empty or unparsable body is treated as a
// plain acceptance rather than a failure: the action did happen, and failing
// the user over a serialisation detail would be the wrong trade.
func decodeSuccess(body io.Reader) (*core.ActionResult, error) {
	var parsed responseBody
	if err := json.NewDecoder(body).Decode(&parsed); err != nil {
		return &core.ActionResult{Status: core.StatusAccepted}, nil
	}

	result := &core.ActionResult{Status: core.StatusAccepted, Message: parsed.Message}
	if parsed.Status == string(core.StatusNoop) {
		result.Status = core.StatusNoop
	}
	if parsed.Event != nil {
		summary := &core.EventSummary{
			ID:      parsed.Event.ID,
			Title:   parsed.Event.Title,
			Address: parsed.Event.Address,
		}
		if parsed.Event.StartsAt != "" {
			// A UTC time is dropped rather than shown: the bot prints times in
			// the offset they carry, and a UTC one would be hours off for the
			// user. Without it the reply simply omits the time.
			if startsAt, err := time.Parse(time.RFC3339, parsed.Event.StartsAt); err == nil && domain.HasCityOffset(startsAt) {
				summary.StartsAt = &startsAt
			}
		}
		result.Event = summary
	}
	return result, nil
}

// decodeBusinessError maps a 4xx onto a typed core.Error.
func decodeBusinessError(resp *http.Response) error {
	limited := io.LimitReader(resp.Body, maxErrorBody)
	raw, _ := io.ReadAll(limited)

	var parsed errorBody
	_ = json.Unmarshal(raw, &parsed)

	code := mapErrorCode(parsed.Error.Code, resp.StatusCode)
	message := parsed.Error.Message
	if message == "" {
		message = fmt.Sprintf("core backend returned %d", resp.StatusCode)
	}
	return core.NewError(code, message, resp.StatusCode, nil)
}

// mapErrorCode translates the backend's code vocabulary into the bot's.
//
// An unrecognised code falls back to the HTTP status, so a backend that
// invents a new code still produces a sensible user-facing message.
func mapErrorCode(raw string, status int) core.ErrorCode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "not_found", "registration_not_found", "event_not_found":
		return core.CodeNotFound
	case "event_cancelled", "event_canceled":
		return core.CodeEventCancelled
	case "registration_cancelled", "registration_canceled":
		return core.CodeRegistrationCancelled
	case "offer_expired", "waitlist_offer_expired":
		return core.CodeOfferExpired
	case "forbidden", "unauthorized":
		return core.CodeForbidden
	case "conflict":
		return core.CodeConflict
	}

	switch status {
	case http.StatusNotFound:
		return core.CodeNotFound
	case http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity:
		return core.CodeConflict
	case http.StatusUnauthorized, http.StatusForbidden:
		return core.CodeForbidden
	default:
		return core.CodeConflict
	}
}

// setHeaders applies auth and correlation headers.
func (g *Gateway) setHeaders(req *http.Request, requestID string) {
	if g.apiKey != "" {
		req.Header.Set(HeaderAPIKey, g.apiKey)
	}
	if requestID != "" {
		req.Header.Set(HeaderRequestID, requestID)
	}
	req.Header.Set("Accept", "application/json")
}

// drainAndClose closes a response body, draining a bounded amount first so the
// connection can be reused by keep-alive.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxErrorBody))
	_ = body.Close()
}

func isTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
