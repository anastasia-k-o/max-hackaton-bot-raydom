package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"hackatonCore/internal/clock"
	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// start is a fixed "now" for every test: Monday 28 September 2026, 10:00 MSK.
var start = time.Date(2026, 9, 28, 10, 0, 0, 0, domain.LocationOf("Europe/Moscow"))

type env struct {
	t     *testing.T
	ctx   context.Context
	st    *store.Store
	clk   *clock.Fixed
	svc   *Service
	org   domain.User
	maxID int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewFixed(start)
	e := &env{t: t, ctx: context.Background(), st: st, clk: clk,
		svc: New(st, clk, Config{Timing: domain.RealTiming(), MiniAppURL: "https://afisha.test/app/"}, nil, nil)}
	e.org = e.user(nil, "Орг")
	return e
}

// user creates a verified user; maxID nil makes a demo participant.
func (e *env) user(maxID *int64, name string) domain.User {
	e.t.Helper()
	u, _, err := e.svc.DevLogin(e.ctx, DevUser{MaxUserID: maxID, FirstName: name, IsAuthor: ptr(true)})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// person is a user with a MAX id, who can receive messages.
func (e *env) person(name string) domain.User {
	e.maxID++
	id := 1000 + e.maxID
	return e.user(&id, name)
}

func (e *env) event(in time.Duration, capacity *int) domain.Event {
	e.t.Helper()
	at := domain.FormatTime(e.clk.Now().Add(in), domain.LocationOf("Europe/Moscow"))
	ev, err := e.svc.CreateEvent(e.ctx, e.org, EventInput{
		Title: ptr("Йога в парке"), Description: ptr("Практика на траве"), CategoryID: ptr("sport"),
		TagIDs: &[]string{"t_yoga"}, StartsAt: &at, DurationMin: ptr(60), CityID: ptr("msk"),
		District: ptr("Хамовники"), Address: ptr("Парк Горького"),
		Capacity: Nullable[int]{Set: true, Value: capacity},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return ev
}

func (e *env) register(u domain.User, ev domain.Event) domain.Registration {
	e.t.Helper()
	r, err := e.svc.Register(e.ctx, u, ev.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) reg(id string) domain.Registration {
	e.t.Helper()
	var r domain.Registration
	if err := e.st.InTx(e.ctx, func(tx *store.Tx) error {
		var err error
		r, err = tx.Registration(e.ctx, id)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) userByID(id string) domain.User {
	e.t.Helper()
	var u domain.User
	_ = e.st.InTx(e.ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.User(e.ctx, id)
		return err
	})
	return u
}

// outbox returns everything queued so far, oldest first.
func (e *env) outbox() []store.Notification {
	e.t.Helper()
	var out []store.Notification
	if err := e.st.InTx(e.ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.NotificationsSince(e.ctx, 0)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// sent lists "type→user" of pending rows, i.e. what would reach MAX.
func (e *env) queuedTypes(userID string) []string {
	var out []string
	for _, n := range e.outbox() {
		if n.UserID == userID && n.Status == store.NotifPending {
			out = append(out, n.Type)
		}
	}
	return out
}

func (e *env) tick() TickReport {
	e.t.Helper()
	rep, err := e.svc.Tick(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return rep
}

func envelopeOf(t *testing.T, n store.Notification) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal(n.Payload, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func codeOf(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
