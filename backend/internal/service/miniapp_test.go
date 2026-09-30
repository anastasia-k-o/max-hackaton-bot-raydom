package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"hackatonCore/internal/seed"
	"hackatonCore/internal/store"
)

const testToken = "test-bot-token"

// signInitData builds initData the way MAX signs it, following the SDK's
// own test helper (bot_init_data_test.go in max-bot-api-client-go v2.4.0).
func signInitData(params map[string]string) string {
	var pairs []string
	for k, v := range params {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(testToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	values.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return values.Encode()
}

func maxUserJSON(id int64) string {
	return fmt.Sprintf(`{"id":%d,"first_name":"Анна","last_name":"Кузнецова","photo_url":"https://example.test/a.png"}`, id)
}

func authCfg() AuthConfig { return AuthConfig{BotToken: testToken, MaxAge: time.Hour} }

func TestLoginWithInitData(t *testing.T) {
	e := newEnv(t)
	initData := signInitData(map[string]string{
		"user": maxUserJSON(555), "auth_date": fmt.Sprint(time.Now().Unix()), "query_id": "q1",
	})
	u, token, err := e.svc.LoginWithInitData(e.ctx, authCfg(), initData)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || u.MaxUserID == nil || *u.MaxUserID != 555 || u.FirstName != "Анна" || u.IsVerified {
		t.Fatalf("user = %+v, token %q", u, token)
	}
	// A second sign-in finds the same user.
	again, _, err := e.svc.LoginWithInitData(e.ctx, authCfg(), initData)
	if err != nil || again.ID != u.ID {
		t.Fatalf("second login = %+v, %v", again, err)
	}
	if byToken, err := e.svc.UserBySession(e.ctx, token); err != nil || byToken.ID != u.ID {
		t.Errorf("session = %+v, %v", byToken, err)
	}
}

// TestBotStartedUserKeepsTheirStatus: the bot may create the user first
// (bot_started); signing in later must reuse that user.
func TestBotStartedUserKeepsTheirStatus(t *testing.T) {
	e := newEnv(t)
	e.svc.ReportBotStatus(e.ctx, 777, false, "bot_stopped", time.Now())
	u, _, err := e.svc.LoginWithInitData(e.ctx, authCfg(), signInitData(map[string]string{
		"user": maxUserJSON(777), "auth_date": fmt.Sprint(time.Now().Unix()),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if u.BotAvailable || u.FirstName != "Анна" {
		t.Errorf("user = %+v", u)
	}
}

func TestLoginRefusals(t *testing.T) {
	e := newEnv(t)
	fresh := fmt.Sprint(time.Now().Unix())
	good := signInitData(map[string]string{"user": maxUserJSON(1), "auth_date": fresh})
	cases := []struct {
		name     string
		cfg      AuthConfig
		initData string
		code     string
	}{
		{"empty", authCfg(), "", "invalid_request"},
		{"tampered", authCfg(), strings.Replace(good, "%22id%22%3A1", "%22id%22%3A2", 1), "unauthorized"},
		{"stale", authCfg(), signInitData(map[string]string{"user": maxUserJSON(1), "auth_date": fmt.Sprint(time.Now().Add(-2 * time.Hour).Unix())}), "unauthorized"},
		{"no token configured", AuthConfig{}, good, "upstream_unavailable"},
		{"dev outside dev mode", authCfg(), "dev", "unauthorized"},
	}
	for _, tc := range cases {
		_, _, err := e.svc.LoginWithInitData(e.ctx, tc.cfg, tc.initData)
		if got := codeOf(err); got != tc.code {
			t.Errorf("%s: code %q (%v), want %q", tc.name, got, err, tc.code)
		}
	}
}

func TestDevLoginIsTheDemoUser(t *testing.T) {
	e := newEnv(t)
	u, token, err := e.svc.LoginWithInitData(e.ctx, AuthConfig{DevLogin: true}, "dev")
	if err != nil || token == "" || u.ID != DemoUserID || !u.IsVerified || !u.Demo {
		t.Fatalf("user = %+v, err = %v", u, err)
	}
}

func TestProfileUpdates(t *testing.T) {
	e := newEnv(t)
	u := e.person("Анна")
	u.IsVerified = false
	e.st.InTx(e.ctx, func(tx *store.Tx) error { return tx.SaveUser(e.ctx, u) })

	if _, err := e.svc.UpdateMe(e.ctx, u.ID, ProfilePatch{IsAuthor: ptr(true)}); codeOf(err) != "verification_required" {
		t.Errorf("author without verification: %v", err)
	}
	if _, err := e.svc.Verify(e.ctx, u.ID, "demo", false); codeOf(err) != "invalid_request" {
		t.Errorf("demo phone outside dev mode: %v", err)
	}
	if _, err := e.svc.Verify(e.ctx, u.ID, "+79990000000", false); err != nil {
		t.Fatal(err)
	}
	p := ProfilePatch{IsAuthor: ptr(true), District: ptr("Тверской")}
	p.NotificationSettings = &struct {
		Reminders       *bool `json:"reminders"`
		Recommendations *bool `json:"recommendations"`
	}{Reminders: ptr(false)}
	got, err := e.svc.UpdateMe(e.ctx, u.ID, p)
	if err != nil || !got.IsAuthor || *got.District != "Тверской" || got.NotifyReminders || !got.NotifyRecommendation {
		t.Fatalf("updated = %+v, %v", got, err)
	}
	if _, err := e.svc.UpdateMe(e.ctx, u.ID, ProfilePatch{District: ptr("Бруклин")}); codeOf(err) != "invalid_request" {
		t.Errorf("unknown district: %v", err)
	}
	c, _ := e.svc.AcceptConsent(e.ctx, u.ID)
	if c.ConsentAcceptedAt == nil {
		t.Error("consent not recorded")
	}
}

func TestInterests(t *testing.T) {
	e := newEnv(t)
	u := e.person("Анна")
	if _, err := e.svc.SetInterests(e.ctx, u.ID, []string{"t_yoga", "t_run"}); codeOf(err) != "invalid_request" {
		t.Errorf("two tags: %v", err)
	}
	ids, err := e.svc.SetInterests(e.ctx, u.ID, []string{"t_yoga", "t_run", "t_nope", "t_chess", "t_yoga"})
	if err != nil || !equal(ids, []string{"t_yoga", "t_run", "t_chess"}) {
		t.Fatalf("ids = %v, %v", ids, err)
	}
	if me, _ := e.svc.Me(e.ctx, u.ID); !me.OnboardingCompleted {
		t.Error("onboarding not completed")
	}
}

// seeded builds an environment with the mocks' demo data.
func seeded(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	if ok, err := seed.IfEmpty(e.ctx, e.st, start); err != nil || ok {
		// newEnv already created an event-less DB; IfEmpty must seed it.
		if err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("seed did not run on an empty database")
	}
	return e
}

func TestSeedMatchesTheMocks(t *testing.T) {
	e := seeded(t)
	page, err := e.svc.Catalog(e.ctx, CatalogQuery{CityID: "msk", PageSize: 50}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 14 {
		t.Errorf("catalog shows %d events, want the mocks' 14 upcoming", page.Total)
	}
	// Seeding twice does nothing.
	if ok, _ := seed.IfEmpty(e.ctx, e.st, start); ok {
		t.Error("seeded a non-empty database")
	}
	me, _ := e.svc.Me(e.ctx, DemoUserID)
	regs, err := e.svc.MyRegistrations(e.ctx, me)
	if err != nil || len(regs) != 6 {
		t.Fatalf("demo user's registrations = %d, %v", len(regs), err)
	}
	// Reminders for demo people are never sent.
	e.clk.Advance(20 * time.Hour)
	e.tick()
	for _, n := range e.outbox() {
		if n.Status == "pending" {
			t.Errorf("message to a demo user queued: %+v", n)
		}
	}
}

func TestCatalogFilters(t *testing.T) {
	e := seeded(t)
	me, _ := e.svc.Me(e.ctx, DemoUserID)
	count := func(q CatalogQuery) int {
		q.PageSize = 50
		p, err := e.svc.Catalog(e.ctx, q, &me)
		if err != nil {
			t.Fatal(err)
		}
		return p.Total
	}
	if n := count(CatalogQuery{CategoryID: "sport"}); n != 4 {
		t.Errorf("sport = %d, want 4", n)
	}
	if n := count(CatalogQuery{Q: "ЙОГ"}); n != 1 {
		t.Errorf("search = %d, want 1", n)
	}
	if n := count(CatalogQuery{TagIDs: []string{"t_chess", "t_quiz"}}); n != 2 {
		t.Errorf("tags = %d, want 2", n)
	}
	if n := count(CatalogQuery{Date: "today"}); n != 1 {
		t.Errorf("today = %d, want 1 (the lecture in 40 minutes)", n)
	}
	if n := count(CatalogQuery{HasSeats: true}); n >= 14 {
		t.Errorf("has_seats hides nothing: %d", n)
	}
	p, _ := e.svc.Catalog(e.ctx, CatalogQuery{Sort: "popularity", PageSize: 2, Page: 1}, &me)
	if len(p.Items) != 2 || p.Items[0].ID != "event_13" {
		t.Errorf("popular first = %+v", p.Items)
	}
	p, _ = e.svc.Catalog(e.ctx, CatalogQuery{PageSize: 50}, &me)
	for _, it := range p.Items {
		if it.ID == "event_3" && (it.MyRegistrationStatus == nil || *it.MyRegistrationStatus != "registered") {
			t.Errorf("my status on event_3 = %v", it.MyRegistrationStatus)
		}
	}
}

func TestReportMatchesTheMocks(t *testing.T) {
	e := seeded(t)
	me, _ := e.svc.Me(e.ctx, DemoUserID)
	rep, err := e.svc.EventReport(e.ctx, me, "event_7")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Confirmed != 7 || rep.NoAnswer != 4 || rep.CancelledTotal != 2 || rep.CancelReasons["ill"] != 1 || len(rep.Participants) != 13 {
		t.Errorf("report = %+v", rep)
	}
	if _, err := e.svc.EventReport(e.ctx, me, "event_2"); codeOf(err) != "forbidden" {
		t.Errorf("someone else's report: %v", err)
	}
}

func TestRecommendations(t *testing.T) {
	e := seeded(t)
	me, _ := e.svc.Me(e.ctx, DemoUserID)
	items, mode, _ := e.svc.Recommendations(e.ctx, &me, "msk")
	if mode != "popular" || len(items) == 0 {
		t.Fatalf("mode %s, %d items", mode, len(items))
	}
	for _, it := range items {
		if it.Event.AuthorID == me.ID || it.Event.MyRegistrationStatus != nil {
			t.Errorf("recommended own or joined event %s", it.Event.ID)
		}
	}
	e.svc.SetInterests(e.ctx, me.ID, []string{"t_dance", "t_meet", "t_lectures"})
	me, _ = e.svc.Me(e.ctx, DemoUserID)
	items, mode, _ = e.svc.Recommendations(e.ctx, &me, "msk")
	if mode != "personal" || !strings.HasPrefix(items[0].Reason, "Вам интересно") {
		t.Errorf("personal = %s %+v", mode, items[0])
	}
}

func TestSuggestTags(t *testing.T) {
	s := SuggestTags("Йога для новичков", "Медитация и асаны в парке")
	if s.CategoryID == nil || *s.CategoryID != "sport" || !equal(s.TagIDs, []string{"t_yoga", "t_beginners"}) || s.Confidence <= 0.5 {
		t.Errorf("suggestion = %+v", s)
	}
	if s := SuggestTags("ab", ""); s.CategoryID != nil || len(s.TagIDs) != 0 {
		t.Errorf("short text = %+v", s)
	}
}

// TestDevLoginAsRealAccount: with DEMO_MAX_USER_ID, "dev" signs in as that
// MAX account — the one the bot writes to — verified and an author.
func TestDevLoginAsRealAccount(t *testing.T) {
	e := newEnv(t)
	e.svc.ReportBotStatus(e.ctx, 4242, true, "bot_started", time.Now()) // the bot met them first
	cfg := AuthConfig{DevLogin: true, DevMaxUserID: 4242}
	u, token, err := e.svc.LoginWithInitData(e.ctx, cfg, "dev")
	if err != nil || token == "" || u.MaxUserID == nil || *u.MaxUserID != 4242 || !u.IsVerified || !u.IsAuthor || u.Demo {
		t.Fatalf("user = %+v, err = %v", u, err)
	}
	again, _, _ := e.svc.LoginWithInitData(e.ctx, cfg, "dev")
	if again.ID != u.ID {
		t.Errorf("second login made another user: %s vs %s", again.ID, u.ID)
	}
	// Messages to this account are really sent, unlike to demo people.
	ev := e.event(48*time.Hour, nil)
	e.register(again, ev)
	if got := e.queuedTypes(u.ID); !equal(got, []string{TypeRegistrationCreated}) {
		t.Errorf("queued %v", got)
	}
}
