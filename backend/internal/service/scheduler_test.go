package service

import (
	"testing"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// TestReminderChain is the test the whole scheduler exists for: sign up two
// days ahead and walk the clock to the start, checking what is sent when.
func TestReminderChain(t *testing.T) {
	e := newEnv(t)
	ev := e.event(26*time.Hour, nil)
	anna := e.person("Анна")
	e.register(anna, ev)

	steps := []struct {
		at   time.Duration // before the start
		want []string      // newly queued for Анна at this step
	}{
		{25 * time.Hour, nil},
		{24 * time.Hour, []string{TypeReminder24h}},
		{12 * time.Hour, nil},
		{6 * time.Hour, []string{TypeConfirmationRequired}},
		{3 * time.Hour, []string{TypeConfirmationRetry}},
		{90 * time.Minute, nil},
		{time.Hour, []string{TypeReminder1h}},
		{10 * time.Minute, nil},
	}
	seen := len(e.queuedTypes(anna.ID)) // registration_created
	for _, s := range steps {
		e.clk.Set(ev.StartsAt.Add(-s.at))
		e.tick()
		e.tick() // twice: every message must still be queued once
		all := e.queuedTypes(anna.ID)
		got := all[seen:]
		seen = len(all)
		if !equal(got, s.want) {
			t.Errorf("%s before: queued %v, want %v", s.at, got, s.want)
		}
	}
}

func TestConfirmedUserGetsNoConfirmationRequests(t *testing.T) {
	e := newEnv(t)
	ev := e.event(26*time.Hour, nil)
	anna := e.person("Анна")
	r := e.register(anna, ev)
	e.svc.Confirm(e.ctx, anna, r.ID)

	for _, at := range []time.Duration{24 * time.Hour, 6 * time.Hour, 3 * time.Hour, time.Hour} {
		e.clk.Set(ev.StartsAt.Add(-at))
		e.tick()
	}
	want := []string{TypeRegistrationCreated, TypeReminder24h, TypeReminder1h}
	if got := e.queuedTypes(anna.ID); !equal(got, want) {
		t.Errorf("queued %v, want %v", got, want)
	}
}

// TestLateSignupSkipsTheDayBeforeReminder: signing up 20 h ahead gets «вы
// записаны», not also «завтра!».
func TestLateSignupSkipsTheDayBeforeReminder(t *testing.T) {
	e := newEnv(t)
	ev := e.event(20*time.Hour, nil)
	anna := e.person("Анна")
	e.register(anna, ev)
	e.tick()
	if got := e.queuedTypes(anna.ID); !equal(got, []string{TypeRegistrationCreated}) {
		t.Errorf("queued %v", got)
	}
}

// TestDowntimeDoesNotSendStaleReminders: a server that was down through the
// 24 h mark does not send "tomorrow!" three hours before the start.
func TestDowntimeDoesNotSendStaleReminders(t *testing.T) {
	e := newEnv(t)
	ev := e.event(30*time.Hour, nil)
	anna := e.person("Анна")
	e.register(anna, ev)

	e.clk.Set(ev.StartsAt.Add(-5 * time.Hour))
	e.tick()
	if got := e.queuedTypes(anna.ID); !equal(got, []string{TypeRegistrationCreated, TypeConfirmationRequired}) {
		t.Errorf("queued %v", got)
	}
}

func TestRemindersOffKeepsConfirmations(t *testing.T) {
	e := newEnv(t)
	ev := e.event(26*time.Hour, nil)
	anna := e.person("Анна")
	anna.NotifyReminders = false
	e.st.InTx(e.ctx, func(tx *store.Tx) error { return tx.SaveUser(e.ctx, anna) })
	e.register(anna, ev)

	e.clk.Set(ev.StartsAt.Add(-24 * time.Hour))
	e.tick()
	e.clk.Set(ev.StartsAt.Add(-6 * time.Hour))
	e.tick()
	if got := e.queuedTypes(anna.ID); !equal(got, []string{TypeRegistrationCreated, TypeConfirmationRequired}) {
		t.Errorf("queued %v: reminders off, confirmation still due", got)
	}
}

func TestTickExpiresOffers(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	holder, first, second := e.person("Д"), e.person("П"), e.person("В")
	seat := e.register(holder, ev)
	q1, q2 := e.register(first, ev), e.register(second, ev)
	e.svc.Cancel(e.ctx, holder, seat.ID, "")

	e.clk.Advance(29 * time.Minute)
	if rep := e.tick(); len(rep.ExpiredOffers) != 0 {
		t.Fatalf("expired too early: %+v", rep)
	}
	e.clk.Advance(2 * time.Minute)
	rep := e.tick()
	if len(rep.ExpiredOffers) != 1 || rep.ExpiredOffers[0] != q1.ID {
		t.Fatalf("report = %+v", rep)
	}
	if got := e.reg(q2.ID); got.Status != domain.RegOffered {
		t.Errorf("next = %+v, want offered", got)
	}
}

// TestMovingTheEventReArmsReminders: reminders are keyed by the start time,
// so a new time gets its own reminders.
func TestMovingTheEventReArmsReminders(t *testing.T) {
	e := newEnv(t)
	ev := e.event(26*time.Hour, nil)
	anna := e.person("Анна")
	e.register(anna, ev)
	e.clk.Set(ev.StartsAt.Add(-24 * time.Hour))
	e.tick()

	newStart := domain.FormatTime(ev.StartsAt.Add(24*time.Hour), ev.Location())
	res, err := e.svc.UpdateEvent(e.ctx, e.org, ev.ID, EventInput{StartsAt: &newStart})
	if err != nil {
		t.Fatal(err)
	}
	if res.NotifiedCount != 1 {
		t.Errorf("notified %d, want 1", res.NotifiedCount)
	}
	e.clk.Set(res.Event.StartsAt.Add(-24 * time.Hour))
	e.tick()
	want := []string{TypeRegistrationCreated, TypeReminder24h, TypeEventUpdated, TypeReminder24h}
	if got := e.queuedTypes(anna.ID); !equal(got, want) {
		t.Errorf("queued %v, want %v", got, want)
	}
}
