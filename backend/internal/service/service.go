// Package service holds the business rules: seats, the waitlist, the
// confirmation window, reminders. The rules are ported from the mini app's
// mocks (frontend/src/api/mocks/handlers.js), which the frontend team wrote
// as the de facto specification; where the mocks were silent — what the bot
// is told, and when — the rules follow bot/docs/INTEGRATION_MINIAPP.md.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hackatonCore/internal/clock"
	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// Config is what the rules need from the outside.
type Config struct {
	Timing domain.Timing
	// MiniAppURL is the mini app's /app root, e.g. https://host/app. The
	// card of an event is MiniAppURL + "/" + event id. Empty: no link.
	MiniAppURL string
}

// Service applies the rules. It is safe for concurrent use: every command
// runs in one database transaction, and the store serialises them.
type Service struct {
	store  *store.Store
	clock  clock.Clock
	cfg    Config
	log    *slog.Logger
	notify func() // wakes the dispatcher after something was queued
}

// New builds the service. notify may be nil.
func New(st *store.Store, clk clock.Clock, cfg Config, log *slog.Logger, notify func()) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if notify == nil {
		notify = func() {}
	}
	cfg.MiniAppURL = strings.TrimRight(strings.TrimSpace(cfg.MiniAppURL), "/")
	return &Service{store: st, clock: clk, cfg: cfg, log: log, notify: notify}
}

// Timing exposes the schedule, for the dev dashboard.
func (s *Service) Timing() domain.Timing { return s.cfg.Timing }

// Now is the service's current time.
func (s *Service) Now() time.Time { return s.clock.Now() }

// run executes fn in a transaction and wakes the dispatcher afterwards.
func (s *Service) run(ctx context.Context, fn func(*store.Tx) error) error {
	err := s.store.InTx(ctx, fn)
	if err == nil {
		s.notify()
	}
	return err
}

// newID returns prefix_ + 12 hex chars. Only [a-z0-9_] so the id is also a
// valid MAX start parameter (^[\w-]{0,512}$) for open_app buttons.
func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// NewToken returns an unguessable session token.
func NewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// notFound turns store.ErrNotFound into the API's 404.
func notFound(err error, what, id string) error {
	if errors.Is(err, store.ErrNotFound) {
		return domain.NotFound(what, id)
	}
	return err
}

func requireVerified(u domain.User) error {
	if !u.IsVerified {
		return domain.Errorf(http.StatusForbidden, "verification_required", "user %s is not verified", u.ID)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
