package service

import (
	"strings"
	"testing"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

func TestRegisterSendsRegistrationCreated(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(10))
	anna := e.person("Анна")

	r := e.register(anna, ev)
	if r.Status != domain.RegRegistered {
		t.Fatalf("status = %s, want registered (outside the 6 h window)", r.Status)
	}
	box := e.outbox()
	if len(box) != 1 || box[0].Type != TypeRegistrationCreated || box[0].Status != store.NotifPending {
		t.Fatalf("outbox = %+v", box)
	}
	env := envelopeOf(t, box[0])
	if env.Recipient.MaxUserID != *anna.MaxUserID || env.Registration == nil || env.Registration.ID != r.ID {
		t.Errorf("envelope = %+v", env)
	}
	if !strings.HasSuffix(env.Event.StartsAt, "+03:00") {
		t.Errorf("starts_at = %s: the bot rejects anything but the city's offset", env.Event.StartsAt)
	}
	if env.Event.MiniAppURL != "https://afisha.test/app/"+ev.ID {
		t.Errorf("mini_app_url = %s", env.Event.MiniAppURL)
	}
}

func TestRegisterInsideConfirmationWindowIsConfirmed(t *testing.T) {
	e := newEnv(t)
	ev := e.event(5*time.Hour, nil)
	r := e.register(e.person("Борис"), ev)
	if r.Status != domain.RegConfirmed || r.ConfirmedAt == nil {
		t.Fatalf("registration = %+v, want confirmed at once", r)
	}
}

func TestRegisterRefusals(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	anna := e.person("Анна")
	e.register(anna, ev)

	cases := []struct {
		name string
		do   func() error
		code string
	}{
		{"twice", func() error { _, err := e.svc.Register(e.ctx, anna, ev.ID); return err }, "already_registered"},
		{"author", func() error { _, err := e.svc.Register(e.ctx, e.org, ev.ID); return err }, "author_cannot_register"},
		{"unknown event", func() error { _, err := e.svc.Register(e.ctx, anna, "event_x"); return err }, "not_found"},
		{"unverified", func() error {
			u := e.person("Вера")
			u.IsVerified = false
			_, err := e.svc.Register(e.ctx, u, ev.ID)
			return err
		}, "verification_required"},
		{"closed", func() error {
			soon := e.event(50*time.Minute, nil)
			_, err := e.svc.Register(e.ctx, e.person("Глеб"), soon.ID)
			return err
		}, "registration_closed"},
	}
	for _, tc := range cases {
		if got := codeOf(tc.do()); got != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, got, tc.code)
		}
	}
}

// TestWaitlistChain walks the queue: full event, a seat frees up, the first
// in line gets an offer with a deadline, accepts in time, and the queue
// behind them moves up.
func TestWaitlistChain(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	holder := e.person("Держатель")
	first, second := e.person("Первый"), e.person("Второй")

	seat := e.register(holder, ev)
	q1, q2 := e.register(first, ev), e.register(second, ev)
	if q1.Status != domain.RegWaitlist || *q1.QueuePosition != 1 || *q2.QueuePosition != 2 {
		t.Fatalf("queue = %+v / %+v", q1, q2)
	}
	if got := e.queuedTypes(first.ID); len(got) != 0 {
		t.Fatalf("queued for a waitlisted user: %v (the bot has no such message)", got)
	}

	if _, err := e.svc.Cancel(e.ctx, holder, seat.ID, "ill"); err != nil {
		t.Fatal(err)
	}
	q1, q2 = e.reg(q1.ID), e.reg(q2.ID)
	if q1.Status != domain.RegOffered || q1.OfferExpiresAt == nil {
		t.Fatalf("first in line = %+v, want offered", q1)
	}
	if want := start.Add(30 * time.Minute); !q1.OfferExpiresAt.Equal(want) {
		t.Errorf("offer expires %v, want %v (30 min outside the 6 h window)", q1.OfferExpiresAt, want)
	}
	if *q2.QueuePosition != 1 {
		t.Errorf("second moved to %d, want 1", *q2.QueuePosition)
	}
	offers := e.queuedTypes(first.ID)
	if !equal(offers, []string{TypeWaitlistOffer}) {
		t.Fatalf("first got %v, want a waitlist_offer", offers)
	}
	for _, n := range e.outbox() {
		if n.Type == TypeWaitlistOffer {
			if exp := envelopeOf(t, n).Data.OfferExpiresAt; !strings.HasSuffix(exp, "+03:00") {
				t.Errorf("offer_expires_at = %q", exp)
			}
		}
	}

	res, err := e.svc.Accept(e.ctx, first, q1.ID, time.Time{})
	if err != nil || res.Status != "accepted" {
		t.Fatalf("accept = %+v, %v", res, err)
	}
	if got := e.reg(q1.ID); got.Status != domain.RegRegistered || !got.FromWaitlist {
		t.Errorf("after accept = %+v", got)
	}
	// A second press is a no-op, not an error.
	if res, err := e.svc.Accept(e.ctx, first, q1.ID, time.Time{}); err != nil || res.Status != "noop" {
		t.Errorf("second accept = %+v, %v", res, err)
	}
}

// TestLateAcceptIsRefusedAndSeatMovesOn: pressing «Занять место» after the
// deadline gets offer_expired, and the seat goes to the next person at once.
func TestLateAcceptIsRefusedAndSeatMovesOn(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	holder, first, second := e.person("Д"), e.person("П"), e.person("В")
	seat := e.register(holder, ev)
	q1, q2 := e.register(first, ev), e.register(second, ev)
	e.svc.Cancel(e.ctx, holder, seat.ID, "")

	e.clk.Advance(31 * time.Minute)
	_, err := e.svc.Accept(e.ctx, first, q1.ID, time.Time{})
	if codeOf(err) != "offer_expired" {
		t.Fatalf("late accept err = %v, want offer_expired", err)
	}
	if got := e.reg(q1.ID); got.Status != domain.RegCancelled || *got.CancelReason != domain.ReasonOfferExpired {
		t.Errorf("expired offer = %+v", got)
	}
	if got := e.reg(q2.ID); got.Status != domain.RegOffered {
		t.Errorf("next in line = %+v, want offered", got)
	}
	// Pressing again still says expired, not "conflict".
	if _, err := e.svc.Accept(e.ctx, first, q1.ID, time.Time{}); codeOf(err) != "offer_expired" {
		t.Errorf("second late accept = %v", err)
	}
}

// TestClickInTimeCountsEvenIfDeliveredLate: occurred_at from MAX decides.
func TestClickInTimeCountsEvenIfDeliveredLate(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	holder, first := e.person("Д"), e.person("П")
	seat := e.register(holder, ev)
	q1 := e.register(first, ev)
	e.svc.Cancel(e.ctx, holder, seat.ID, "")

	pressed := e.clk.Now().Add(29 * time.Minute)
	e.clk.Advance(32 * time.Minute)
	if res, err := e.svc.Accept(e.ctx, first, q1.ID, pressed); err != nil || res.Status != "accepted" {
		t.Fatalf("accept of a timely click = %+v, %v", res, err)
	}
}

func TestDeclinePassesTheSeatOn(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	holder, first, second := e.person("Д"), e.person("П"), e.person("В")
	seat := e.register(holder, ev)
	q1, q2 := e.register(first, ev), e.register(second, ev)
	e.svc.Cancel(e.ctx, holder, seat.ID, "")

	if res, err := e.svc.Decline(e.ctx, first, q1.ID); err != nil || res.Status != "accepted" {
		t.Fatalf("decline = %+v, %v", res, err)
	}
	if got := e.reg(q2.ID); got.Status != domain.RegOffered {
		t.Errorf("next = %+v, want offered", got)
	}
}

func TestSomeoneElsesRegistrationIsNotFound(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	r := e.register(e.person("Анна"), ev)
	if _, err := e.svc.Confirm(e.ctx, e.person("Чужой"), r.ID); codeOf(err) != "not_found" {
		t.Errorf("err = %v, want not_found", err)
	}
}

func TestConfirmAndCancelStates(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	anna := e.person("Анна")
	r := e.register(anna, ev)

	if res, _ := e.svc.Confirm(e.ctx, anna, r.ID); res.Status != "accepted" {
		t.Errorf("confirm = %+v", res)
	}
	if res, _ := e.svc.Confirm(e.ctx, anna, r.ID); res.Status != "noop" {
		t.Errorf("confirm again = %+v", res)
	}
	if res, _ := e.svc.Cancel(e.ctx, anna, r.ID, "plans_changed"); res.Status != "accepted" {
		t.Errorf("cancel = %+v", res)
	}
	if res, _ := e.svc.Cancel(e.ctx, anna, r.ID, ""); res.Status != "noop" {
		t.Errorf("cancel again = %+v", res)
	}
	if _, err := e.svc.Confirm(e.ctx, anna, r.ID); codeOf(err) != "registration_cancelled" {
		t.Errorf("confirm after cancel = %v", err)
	}
	if _, err := e.svc.Cancel(e.ctx, anna, r.ID, "because"); codeOf(err) != "invalid_request" {
		t.Errorf("unknown reason = %v", err)
	}
}

func TestDemoParticipantsAreNeverMessaged(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	demo := e.user(nil, "Демо")
	e.register(demo, ev)
	box := e.outbox()
	if len(box) != 1 || box[0].Status != store.NotifSkipped {
		t.Fatalf("outbox = %+v, want one skipped row", box)
	}
}

func TestBlockedBotSkipsMessages(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	anna := e.person("Анна")
	if _, err := e.svc.ReportBotStatus(e.ctx, *anna.MaxUserID, false, "bot_stopped", start); err != nil {
		t.Fatal(err)
	}
	e.register(e.userByID(anna.ID), ev)
	if got := e.outbox(); got[0].Status != store.NotifSkipped {
		t.Fatalf("row = %+v, want skipped: the bot would answer 422", got[0])
	}
}
