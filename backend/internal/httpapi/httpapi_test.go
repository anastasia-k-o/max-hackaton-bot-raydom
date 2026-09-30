package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hackatonCore/internal/clock"
	"hackatonCore/internal/domain"
	"hackatonCore/internal/notify"
	"hackatonCore/internal/service"
	"hackatonCore/internal/store"
)

const botKey = "bot-key"

type harness struct {
	t   *testing.T
	srv *httptest.Server
	svc *service.Service
	clk *clock.Travel
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.DiscardHandler)
	clk := clock.NewTravel()
	d := notify.New(st, notify.Config{Mode: notify.ModeLog}, log)
	svc := service.New(st, clk, service.Config{Timing: domain.RealTiming(), MiniAppURL: "https://afisha.test/app"}, log, nil)
	srv := httptest.NewServer(NewHandler(Deps{Service: svc, Store: st, Dispatcher: d, Clock: clk, BotAPIKey: botKey, DevMode: true, Log: log,
		Auth: service.AuthConfig{DevLogin: true}}))
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, svc: svc, clk: clk}
}

type call struct {
	method, path string
	body         any
	headers      map[string]string
}

func (h *harness) do(c call) (int, map[string]any) {
	h.t.Helper()
	var body io.Reader
	if c.body != nil {
		b, _ := json.Marshal(c.body)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(c.method, h.srv.URL+c.path, body)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func (h *harness) login(maxID int64, name string) (string, string) {
	h.t.Helper()
	body := map[string]any{"first_name": name}
	if maxID != 0 {
		body["max_user_id"] = maxID
	}
	code, out := h.do(call{method: "POST", path: "/dev/login", body: body})
	if code != 200 {
		h.t.Fatalf("login: %d %v", code, out)
	}
	return out["token"].(string), out["user"].(map[string]any)["id"].(string)
}

func (h *harness) createEvent(token string, in time.Duration, capacity any) string {
	h.t.Helper()
	at := domain.FormatTime(time.Now().Add(in), domain.LocationOf("Europe/Moscow"))
	code, out := h.do(call{method: "POST", path: "/api/events", headers: bearer(token), body: map[string]any{
		"title": "Настолки", "description": "Играем", "category_id": "games", "tag_ids": []string{"t_boardgames"},
		"starts_at": at, "duration_min": 120, "city_id": "msk", "district": "Басманный", "address": "Покровка, 17",
		"capacity": capacity,
	}})
	if code != 201 {
		h.t.Fatalf("create event: %d %v", code, out)
	}
	return out["id"].(string)
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// TestBotAndMiniAppReachTheSameAction is the risk the integration doc
// warns about: the backend must accept the bot's key-based identity as well
// as the mini app's session, on the same endpoint.
func TestBotAndMiniAppReachTheSameAction(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	ev := h.createEvent(org, 48*time.Hour, nil)
	anna, _ := h.login(555, "Анна")

	code, reg := h.do(call{method: "POST", path: "/registrations", headers: bearer(anna), body: map[string]any{"event_id": ev}})
	if code != 201 || reg["status"] != "registered" {
		t.Fatalf("register: %d %v", code, reg)
	}
	regID := reg["id"].(string)

	// The bot: key + max_user_id from MAX.
	code, out := h.do(call{method: "POST", path: "/registrations/" + regID + "/confirm",
		headers: map[string]string{"X-Api-Key": botKey, "X-Request-Id": "req-1"},
		body: map[string]any{"registration_id": regID, "event_id": ev, "max_user_id": 555, "request_id": "req-1",
			"occurred_at": time.Now().UTC().Format(time.RFC3339)}})
	if code != 200 || out["status"] != "accepted" {
		t.Fatalf("bot confirm: %d %v", code, out)
	}
	event, _ := out["event"].(map[string]any)
	if !strings.HasSuffix(event["starts_at"].(string), "+03:00") {
		t.Errorf("event in the answer = %v", event)
	}

	// The mini app: session; the same action is now a no-op.
	code, out = h.do(call{method: "POST", path: "/api/registrations/" + regID + "/confirm", headers: bearer(anna)})
	if code != 200 || out["status"] != "noop" {
		t.Fatalf("mini app confirm: %d %v", code, out)
	}
}

func TestTrustRules(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	ev := h.createEvent(org, 48*time.Hour, nil)
	anna, _ := h.login(555, "Анна")
	h.login(666, "Борис")
	_, reg := h.do(call{method: "POST", path: "/registrations", headers: bearer(anna), body: map[string]any{"event_id": ev}})
	path := "/registrations/" + reg["id"].(string) + "/cancel"

	cases := []struct {
		name    string
		headers map[string]string
		body    map[string]any
		code    int
		errCode string
	}{
		{"no credentials", nil, nil, 401, "unauthorized"},
		{"wrong bot key", map[string]string{"X-Api-Key": "nope"}, map[string]any{"max_user_id": 555}, 401, "unauthorized"},
		{"bot without max_user_id", map[string]string{"X-Api-Key": botKey}, nil, 400, "invalid_request"},
		{"bot speaking for someone else", map[string]string{"X-Api-Key": botKey}, map[string]any{"max_user_id": 666}, 404, "not_found"},
		{"bot for an unknown MAX user", map[string]string{"X-Api-Key": botKey}, map[string]any{"max_user_id": 999}, 404, "not_found"},
		{"session with a forged max_user_id", bearer(anna), map[string]any{"max_user_id": 666}, 403, "forbidden"},
	}
	for _, tc := range cases {
		code, out := h.do(call{method: "POST", path: path, headers: tc.headers, body: tc.body})
		if code != tc.code || errCode(out) != tc.errCode {
			t.Errorf("%s: %d %v, want %d %s", tc.name, code, out, tc.code, tc.errCode)
		}
	}
}

func TestBotStatusEndpoint(t *testing.T) {
	h := newHarness(t)
	code, out := h.do(call{method: "POST", path: "/max-users/321/bot-status", headers: map[string]string{"X-Api-Key": botKey},
		body: map[string]any{"max_user_id": 321, "available": false, "reason": "bot_stopped", "occurred_at": "2026-09-28T10:00:00Z"}})
	if code != 200 || out["status"] != "accepted" {
		t.Fatalf("%d %v", code, out)
	}
	u, err := h.svc.UserByMaxID(context.Background(), 321)
	if err != nil || u.BotAvailable {
		t.Errorf("user = %+v, %v", u, err)
	}
	if code, _ := h.do(call{method: "POST", path: "/max-users/321/bot-status", body: map[string]any{"available": true, "reason": "bot_started"}}); code != 401 {
		t.Errorf("without the key: %d", code)
	}
}

func TestIdempotentReplay(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	ev := h.createEvent(org, 48*time.Hour, nil)
	anna, _ := h.login(555, "Анна")
	hdr := bearer(anna)
	hdr["X-Request-Id"] = "req-same"
	c1, r1 := h.do(call{method: "POST", path: "/registrations", headers: hdr, body: map[string]any{"event_id": ev}})
	c2, r2 := h.do(call{method: "POST", path: "/registrations", headers: hdr, body: map[string]any{"event_id": ev}})
	if c1 != 201 || c2 != 201 || r1["id"] != r2["id"] {
		t.Errorf("replay: %d %v / %d %v", c1, r1, c2, r2)
	}
	delete(hdr, "X-Request-Id")
	if c3, r3 := h.do(call{method: "POST", path: "/registrations", headers: hdr, body: map[string]any{"event_id": ev}}); c3 != 409 || errCode(r3) != "already_registered" {
		t.Errorf("new request: %d %v", c3, r3)
	}
}

func TestErrorEnvelope(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	code, out := h.do(call{method: "POST", path: "/events", headers: bearer(org), body: map[string]any{"title": ""}})
	if code != 400 || errCode(out) != "invalid_request" {
		t.Fatalf("%d %v", code, out)
	}
	e := out["error"].(map[string]any)
	if e["request_id"] == "" || len(e["details"].([]any)) < 5 {
		t.Errorf("envelope = %v", e)
	}
}

// TestClockTravelSendsReminders drives the dev API the way scripts/scenario.sh does.
func TestClockTravelSendsReminders(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	ev := h.createEvent(org, 26*time.Hour, nil)
	anna, _ := h.login(555, "Анна")
	h.do(call{method: "POST", path: "/registrations", headers: bearer(anna), body: map[string]any{"event_id": ev}})

	code, out := h.do(call{method: "POST", path: "/dev/clock", body: map[string]any{"advance": "2h1m"}})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	queued := out["scheduler"].(map[string]any)["queued"].([]any)
	if len(queued) != 1 || !strings.HasPrefix(queued[0].(string), "reminder_24h") {
		t.Errorf("queued = %v", queued)
	}
	if out["delivery"].(map[string]any)["logged"].(float64) < 1 {
		t.Errorf("delivery = %v", out["delivery"])
	}
	code, out = h.do(call{method: "POST", path: "/dev/clock", body: map[string]any{"reset": true}})
	if code != 200 || out["clock_offset"] != "0s" {
		t.Errorf("reset: %d %v", code, out)
	}
}

func TestFillAndCancelAsOwner(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	ev := h.createEvent(org, 48*time.Hour, 1)
	code, out := h.do(call{method: "POST", path: "/dev/events/" + ev + "/fill", body: map[string]any{"count": 1}})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	demoReg := out["registration_ids"].([]any)[0].(string)
	anna, _ := h.login(555, "Анна")
	_, reg := h.do(call{method: "POST", path: "/registrations", headers: bearer(anna), body: map[string]any{"event_id": ev}})
	if reg["status"] != "waitlist" {
		t.Fatalf("reg = %v", reg)
	}
	if code, out := h.do(call{method: "POST", path: "/dev/registrations/" + demoReg + "/cancel"}); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	_, card := h.do(call{method: "GET", path: "/events/" + ev, headers: bearer(anna)})
	if mine := card["my_registration"].(map[string]any); mine["status"] != "offered" || mine["offer_expires_at"] == nil {
		t.Errorf("my registration = %v", mine)
	}
}

func TestHealthAndPrefix(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/health", "/api/health"} {
		if code, out := h.do(call{method: "GET", path: p}); code != 200 || out["status"] != "ok" {
			t.Errorf("%s: %d %v", p, code, out)
		}
	}
}

// TestMiniAppSignInAndProfile walks the mini app's first screen: sign in,
// read the full profile, accept the consent, pick interests.
func TestMiniAppSignInAndProfile(t *testing.T) {
	h := newHarness(t)
	code, out := h.do(call{method: "POST", path: "/api/auth/max", body: map[string]any{"init_data": "dev"}})
	if code != 200 {
		t.Fatalf("auth: %d %v", code, out)
	}
	token := out["token"].(string)
	user := out["user"].(map[string]any)
	for _, k := range []string{"id", "max_user_id", "first_name", "last_name", "photo_url", "is_verified", "is_author",
		"city_id", "district", "bot_available", "onboarding_completed", "consent_accepted_at", "notification_settings"} {
		if _, ok := user[k]; !ok {
			t.Errorf("user has no %q: %v", k, user)
		}
	}
	if user["consent_accepted_at"] != nil {
		t.Errorf("consent before accepting: %v", user["consent_accepted_at"])
	}
	if code, out := h.do(call{method: "POST", path: "/api/me/consent", headers: bearer(token), body: map[string]any{"accepted": true}}); code != 200 || out["consent_accepted_at"] == nil {
		t.Errorf("consent: %d %v", code, out)
	}
	if code, out := h.do(call{method: "PUT", path: "/api/me/interests", headers: bearer(token), body: map[string]any{"tag_ids": []string{"t_yoga", "t_run", "t_chess"}}}); code != 200 || len(out["tag_ids"].([]any)) != 3 {
		t.Errorf("interests: %d %v", code, out)
	}
	if code, out := h.do(call{method: "PATCH", path: "/api/me", headers: bearer(token), body: map[string]any{"notification_settings": map[string]any{"reminders": false}}}); code != 200 ||
		out["notification_settings"].(map[string]any)["reminders"] != false {
		t.Errorf("patch me: %d %v", code, out)
	}
	if code, _ := h.do(call{method: "GET", path: "/api/me"}); code != 401 {
		t.Errorf("me without a session: %d", code)
	}
}

func TestCatalogIsPublicAndPersonalWithSession(t *testing.T) {
	h := newHarness(t)
	org, _ := h.login(0, "Орг")
	h.createEvent(org, 48*time.Hour, nil)
	code, out := h.do(call{method: "GET", path: "/api/events?city_id=msk&sort=starts_at&page=1&page_size=12"})
	if code != 200 || out["total"].(float64) != 1 {
		t.Fatalf("catalog: %d %v", code, out)
	}
	item := out["items"].([]any)[0].(map[string]any)
	if _, leaked := item["starts_at"]; leaked {
		t.Errorf("catalog card leaks the time to anonymous visitors: %v", item)
	}
	if code, out := h.do(call{method: "GET", path: "/api/recommendations?city_id=msk"}); code != 200 || out["mode"] != "popular" {
		t.Errorf("recommendations: %d %v", code, out)
	}
	if code, out := h.do(call{method: "POST", path: "/api/ml/suggest-tags", body: map[string]any{"title": "Шахматный турнир", "description": ""}}); code != 200 || out["category_id"] != "games" {
		t.Errorf("suggest: %d %v", code, out)
	}
}
