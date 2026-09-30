// Package stub implements core.Gateway in memory.
//
// It accepts every action, records it, and returns a predictable success. That
// is deliberate: the bot must be demonstrable end to end before a Core Backend
// exists, and a stub that invents business rules would teach the team the
// wrong contract.
//
// The recorded actions are exposed through GET /dev/core/actions, which is how
// Scenario B ("Bot recognised the callback, StubCore received the action") is
// verified from Postman.
package stub

import (
	"context"
	"sync"
	"time"

	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/observability"
)

// ActionKind names the gateway method that was called.
type ActionKind string

const (
	// KindConfirmRegistration records ConfirmRegistration.
	KindConfirmRegistration ActionKind = "confirm_registration"
	// KindCancelRegistration records CancelRegistration.
	KindCancelRegistration ActionKind = "cancel_registration"
	// KindAcceptWaitlist records AcceptWaitlistOffer.
	KindAcceptWaitlist ActionKind = "accept_waitlist"
	// KindDeclineWaitlist records DeclineWaitlistOffer.
	KindDeclineWaitlist ActionKind = "decline_waitlist"
	// KindBotStatus records ReportBotStatus.
	KindBotStatus ActionKind = "bot_status"
)

// Action is one recorded call, shaped for JSON inspection in Postman.
type Action struct {
	Seq            int64      `json:"seq"`
	Kind           ActionKind `json:"kind"`
	At             time.Time  `json:"at"`
	RegistrationID string     `json:"registration_id"`
	EventID        string     `json:"event_id,omitempty"`
	MaxUserID      int64      `json:"max_user_id"`
	RequestID      string     `json:"request_id,omitempty"`
	OccurredAt     *time.Time `json:"occurred_at,omitempty"`

	// Available and Reason are set on KindBotStatus only.
	Available *bool  `json:"available,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Gateway is the in-memory core.Gateway. Safe for concurrent use.
type Gateway struct {
	mu      sync.RWMutex
	actions []Action
	seq     int64
	// maxActions caps memory use; this is an inspection buffer, not storage.
	maxActions int

	now func() time.Time

	// failNext forces the next call to fail, once. Tests use it to drive the
	// business-conflict and backend-unavailable branches.
	failNext error
}

// Option customises the stub.
type Option func(*Gateway)

// WithClock overrides the time source.
func WithClock(now func() time.Time) Option {
	return func(g *Gateway) { g.now = now }
}

// WithMaxActions overrides the retention cap.
func WithMaxActions(n int) Option {
	return func(g *Gateway) {
		if n > 0 {
			g.maxActions = n
		}
	}
}

// New creates a stub gateway.
func New(opts ...Option) *Gateway {
	gateway := &Gateway{
		actions:    make([]Action, 0, 32),
		maxActions: 500,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(gateway)
	}
	return gateway
}

// Mode implements core.Gateway.
func (g *Gateway) Mode() string { return "stub" }

// Ping always succeeds: the stub has no dependency to be unavailable.
func (g *Gateway) Ping(context.Context) error { return nil }

// ReportBotStatus records what the bot would tell the backend about its
// dialog with a user.
func (g *Gateway) ReportBotStatus(ctx context.Context, req core.BotStatusRequest) error {
	if err := ctx.Err(); err != nil {
		return core.NewError(core.CodeUnavailable, "context cancelled", 0, err)
	}
	if err := req.Validate(); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.failNext != nil {
		err := g.failNext
		g.failNext = nil
		return err
	}

	available := req.Available
	action := Action{
		Kind:      KindBotStatus,
		MaxUserID: req.MaxUserID,
		RequestID: firstNonEmpty(req.RequestID, observability.RequestIDFrom(ctx)),
		Available: &available,
		Reason:    string(req.Reason),
	}
	if !req.OccurredAt.IsZero() {
		occurredAt := req.OccurredAt.UTC()
		action.OccurredAt = &occurredAt
	}
	g.appendLocked(action)
	return nil
}

// ConfirmRegistration records a confirmation.
func (g *Gateway) ConfirmRegistration(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.record(ctx, KindConfirmRegistration, req)
}

// CancelRegistration records a cancellation.
func (g *Gateway) CancelRegistration(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.record(ctx, KindCancelRegistration, req)
}

// AcceptWaitlistOffer records acceptance of a freed seat.
func (g *Gateway) AcceptWaitlistOffer(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.record(ctx, KindAcceptWaitlist, req)
}

// DeclineWaitlistOffer records a declined seat.
func (g *Gateway) DeclineWaitlistOffer(ctx context.Context, req core.ActionRequest) (*core.ActionResult, error) {
	return g.record(ctx, KindDeclineWaitlist, req)
}

// Actions returns a copy of everything recorded, oldest first.
func (g *Gateway) Actions() []Action {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]Action, len(g.actions))
	copy(out, g.actions)
	return out
}

// Reset clears the recorded history and returns how many entries were dropped.
func (g *Gateway) Reset() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	n := len(g.actions)
	g.actions = g.actions[:0]
	return n
}

// LastAction returns the most recently recorded action.
func (g *Gateway) LastAction() (Action, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.actions) == 0 {
		return Action{}, false
	}
	return g.actions[len(g.actions)-1], true
}

// FailNext makes the next gateway call return err, once.
func (g *Gateway) FailNext(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failNext = err
}

// record validates, stores and acknowledges one action.
func (g *Gateway) record(ctx context.Context, kind ActionKind, req core.ActionRequest) (*core.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, core.NewError(core.CodeUnavailable, "context cancelled", 0, err)
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.failNext != nil {
		err := g.failNext
		g.failNext = nil
		return nil, err
	}

	action := Action{
		Kind:           kind,
		RegistrationID: req.RegistrationID,
		EventID:        req.EventID,
		MaxUserID:      req.MaxUserID,
		RequestID:      firstNonEmpty(req.RequestID, observability.RequestIDFrom(ctx)),
	}
	if !req.OccurredAt.IsZero() {
		occurredAt := req.OccurredAt.UTC()
		action.OccurredAt = &occurredAt
	}
	g.appendLocked(action)

	// The stub returns no EventSummary on purpose: the bot must render a
	// correct message from what it already knows, so that swapping in a real
	// backend that does return one is an enhancement, not a bug fix.
	return &core.ActionResult{Status: core.StatusAccepted}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// appendLocked numbers an action and stores it, trimming the oldest entries
// past the cap. The caller must hold g.mu.
func (g *Gateway) appendLocked(action Action) {
	g.seq++
	action.Seq = g.seq
	action.At = g.now().UTC()
	g.actions = append(g.actions, action)
	if len(g.actions) > g.maxActions {
		overflow := len(g.actions) - g.maxActions
		g.actions = append(g.actions[:0], g.actions[overflow:]...)
	}
}
