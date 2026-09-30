package service

import (
	"testing"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

func TestEventUpdateListsChanges(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(5))
	anna, boris := e.person("Анна"), e.person("Борис")
	e.register(anna, ev)
	r := e.register(boris, ev)
	e.svc.Cancel(e.ctx, boris, r.ID, "")

	newStart := domain.FormatTime(ev.StartsAt.Add(time.Hour), ev.Location())
	res, err := e.svc.UpdateEvent(e.ctx, e.org, ev.ID, EventInput{
		StartsAt: &newStart, Address: ptr("Сокольники"), OrganizerMessage: ptr("Вход с главной аллеи"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.NotifiedCount != 1 {
		t.Errorf("notified %d, want 1 (the cancelled one is not told)", res.NotifiedCount)
	}
	var env Envelope
	for _, n := range e.outbox() {
		if n.Type == TypeEventUpdated {
			env = envelopeOf(t, n)
		}
	}
	if env.Data == nil || len(env.Data.Changes) != 2 {
		t.Fatalf("data = %+v", env.Data)
	}
	if c := env.Data.Changes[0]; c.Field != "time" || c.Old != "10:00" || c.New != "11:00" {
		t.Errorf("time change = %+v", c)
	}
	if c := env.Data.Changes[1]; c.Field != "address" || c.New != "Сокольники" {
		t.Errorf("address change = %+v", c)
	}
	if env.Data.OrganizerMessage != "Вход с главной аллеи" || env.Event.StartsAt != newStart {
		t.Errorf("envelope = %+v", env)
	}
}

func TestEditWithoutVisibleChangesTellsNobody(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	e.register(e.person("Анна"), ev)
	res, err := e.svc.UpdateEvent(e.ctx, e.org, ev.ID, EventInput{Description: ptr("Новое описание")})
	if err != nil || res.NotifiedCount != 0 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestCapacityRules(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	e.register(e.person("А"), ev)
	queued := e.register(e.person("Б"), ev)

	_, err := e.svc.UpdateEvent(e.ctx, e.org, ev.ID, EventInput{Capacity: Nullable[int]{Set: true, Value: ptr(0)}})
	if codeOf(err) != "invalid_request" {
		t.Errorf("capacity 0: %v", err)
	}
	// Raising the capacity offers the new seat to the queue.
	if _, err := e.svc.UpdateEvent(e.ctx, e.org, ev.ID, EventInput{Capacity: Nullable[int]{Set: true, Value: ptr(2)}}); err != nil {
		t.Fatal(err)
	}
	if got := e.reg(queued.ID); got.Status != domain.RegOffered {
		t.Errorf("queued = %+v, want offered", got)
	}
	if _, err := e.svc.UpdateEvent(e.ctx, e.org, ev.ID, EventInput{Capacity: Nullable[int]{Set: true, Value: ptr(1)}}); codeOf(err) != "capacity_below_registered" {
		t.Errorf("shrink below taken: %v", err)
	}
}

func TestCancelEventTellsEveryoneActive(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, ptr(1))
	anna, boris := e.person("Анна"), e.person("Борис")
	r := e.register(anna, ev)
	e.register(boris, ev) // waitlist: also told

	status, n, err := e.svc.CancelEvent(e.ctx, e.org, ev.ID, "Штормовое предупреждение")
	if err != nil || status != "accepted" || n != 2 {
		t.Fatalf("cancel = %s %d %v", status, n, err)
	}
	if status, _, _ := e.svc.CancelEvent(e.ctx, e.org, ev.ID, ""); status != "noop" {
		t.Errorf("second cancel = %s", status)
	}
	if _, err := e.svc.Confirm(e.ctx, anna, r.ID); codeOf(err) != "event_cancelled" {
		t.Errorf("confirm on cancelled event = %v", err)
	}
	// No reminders for a cancelled event.
	e.clk.Set(ev.StartsAt.Add(-time.Hour))
	e.tick()
	for _, n := range e.outbox() {
		if n.Type == TypeReminder1h {
			t.Errorf("reminder for a cancelled event: %+v", n)
		}
	}
}

func TestOnlyTheAuthorEdits(t *testing.T) {
	e := newEnv(t)
	ev := e.event(48*time.Hour, nil)
	if _, err := e.svc.UpdateEvent(e.ctx, e.person("Чужой"), ev.ID, EventInput{Title: ptr("x")}); codeOf(err) != "forbidden" {
		t.Errorf("err = %v", err)
	}
}

func TestEventValidation(t *testing.T) {
	e := newEnv(t)
	utc := "2026-10-01T16:00:00Z"
	_, err := e.svc.CreateEvent(e.ctx, e.org, EventInput{StartsAt: &utc})
	de, ok := err.(*domain.Error)
	if !ok || de.Code != "invalid_request" {
		t.Fatalf("err = %v", err)
	}
	fields := map[string]bool{}
	for _, d := range de.Details {
		fields[d.Field] = true
	}
	for _, f := range []string{"title", "description", "category_id", "tag_ids", "starts_at", "duration_min", "address"} {
		if !fields[f] {
			t.Errorf("no error for %s: %+v", f, de.Details)
		}
	}
}

func TestStopWordsGoToModeration(t *testing.T) {
	e := newEnv(t)
	at := domain.FormatTime(start.Add(48*time.Hour), domain.LocationOf("Europe/Moscow"))
	ev, err := e.svc.CreateEvent(e.ctx, e.org, EventInput{
		Title: ptr("Йога"), Description: ptr("Стоимость 500 руб"), CategoryID: ptr("sport"),
		TagIDs: &[]string{"t_yoga"}, StartsAt: &at, DurationMin: ptr(60), CityID: ptr("msk"),
		District: ptr("Хамовники"), Address: ptr("Парк"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != domain.EventModeration || len(ev.ModerationFlags) == 0 {
		t.Errorf("event = %s %v", ev.Status, ev.ModerationFlags)
	}
	if _, err := e.svc.Register(e.ctx, e.person("А"), ev.ID); codeOf(err) != "conflict" {
		t.Errorf("register for an event in moderation: %v", err)
	}
}

func TestBotStatusCreatesUnknownUsersAndIgnoresOldNews(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.ReportBotStatus(e.ctx, 777, true, "bot_started", start); err != nil {
		t.Fatal(err)
	}
	u, err := e.svc.UserByMaxID(e.ctx, 777)
	if err != nil || !u.BotAvailable {
		t.Fatalf("user = %+v, %v", u, err)
	}
	e.svc.ReportBotStatus(e.ctx, 777, false, "bot_stopped", start.Add(time.Minute))
	applied, _ := e.svc.ReportBotStatus(e.ctx, 777, true, "bot_started", start) // older, arrived late
	u, _ = e.svc.UserByMaxID(e.ctx, 777)
	if applied || u.BotAvailable {
		t.Errorf("an older status overwrote a newer one: applied=%v user=%+v", applied, u)
	}
	if _, err := e.svc.ReportBotStatus(e.ctx, 777, true, "hello", start); codeOf(err) != "invalid_request" {
		t.Errorf("unknown reason: %v", err)
	}
	_ = store.NotifSent
}
