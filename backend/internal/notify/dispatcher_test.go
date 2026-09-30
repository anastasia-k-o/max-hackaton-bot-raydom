package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// fakeBot answers POST /api/v1/notifications with a scripted status.
type fakeBot struct {
	mu       sync.Mutex
	statuses []int
	calls    []*http.Request
	bodies   [][]byte
}

func (f *fakeBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, r)
	f.bodies = append(f.bodies, body)
	status := http.StatusAccepted
	if len(f.statuses) > 0 {
		status, f.statuses = f.statuses[0], f.statuses[1:]
	}
	w.WriteHeader(status)
	switch status {
	case http.StatusAccepted:
		_, _ = io.WriteString(w, `{"request_id":"x","status":"sent","message_id":"mid-1"}`)
	case http.StatusUnprocessableEntity:
		_, _ = io.WriteString(w, `{"error":{"code":"recipient_unavailable","message":"user blocked the bot"}}`)
	default:
		_, _ = io.WriteString(w, `{"error":{"code":"boom","message":"no"}}`)
	}
}

func setup(t *testing.T, statuses ...int) (*Dispatcher, *store.Store, *fakeBot) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot := &fakeBot{statuses: statuses}
	srv := httptest.NewServer(bot)
	t.Cleanup(srv.Close)
	d := New(st, Config{Mode: ModeHTTP, BotBaseURL: srv.URL + "/", InternalAPIKey: "secret", MaxAttempts: 3},
		slog.New(slog.DiscardHandler))

	ctx := context.Background()
	maxID := int64(42)
	err = st.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.SaveUser(ctx, domain.User{ID: "u1", MaxUserID: &maxID, FirstName: "А", CityID: "msk", BotAvailable: true, CreatedAt: time.Now()}); err != nil {
			return err
		}
		_, err := tx.EnqueueNotification(ctx, store.Notification{
			RequestID: "ntf_1", DedupKey: "k1", Type: "registration_created", UserID: "u1", EventID: "e1",
			Payload: []byte(`{"request_id":"ntf_1","type":"registration_created"}`), Status: store.NotifPending,
			NextAttemptAt: time.Now().Add(-time.Second), CreatedAt: time.Now(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return d, st, bot
}

func row(t *testing.T, st *store.Store) store.Notification {
	t.Helper()
	var rows []store.Notification
	_ = st.InTx(context.Background(), func(tx *store.Tx) error {
		var err error
		rows, err = tx.NotificationsSince(context.Background(), 0)
		return err
	})
	return rows[0]
}

func TestSentWithHeadersAndStableRequestID(t *testing.T) {
	d, st, bot := setup(t)
	rep, err := d.Flush(context.Background())
	if err != nil || rep.Sent != 1 {
		t.Fatalf("rep = %+v, err = %v", rep, err)
	}
	req := bot.calls[0]
	if req.URL.Path != "/api/v1/notifications" || req.Header.Get("X-Internal-Api-Key") != "secret" || req.Header.Get("X-Request-Id") != "ntf_1" {
		t.Errorf("request = %s %v", req.URL.Path, req.Header)
	}
	var body map[string]any
	_ = json.Unmarshal(bot.bodies[0], &body)
	if body["request_id"] != "ntf_1" {
		t.Errorf("body = %s", bot.bodies[0])
	}
	if n := row(t, st); n.Status != store.NotifSent || n.MessageID != "mid-1" || n.SentAt == nil {
		t.Errorf("row = %+v", n)
	}
}

// TestRecipientUnavailableMarksTheUser: a 422 means the user stopped the
// bot. No retry, and later messages to them are skipped at the source.
func TestRecipientUnavailableMarksTheUser(t *testing.T) {
	d, st, _ := setup(t, http.StatusUnprocessableEntity)
	rep, _ := d.Flush(context.Background())
	if rep.Failed != 1 {
		t.Fatalf("rep = %+v", rep)
	}
	var u domain.User
	_ = st.InTx(context.Background(), func(tx *store.Tx) error {
		var err error
		u, err = tx.User(context.Background(), "u1")
		return err
	})
	if u.BotAvailable {
		t.Error("user still marked as reachable after 422")
	}
}

func TestServerErrorsAreRetriedThenGivenUp(t *testing.T) {
	d, st, bot := setup(t, 502, 502, 502)
	d.Flush(context.Background())
	n := row(t, st)
	if n.Status != store.NotifPending || n.Attempts != 1 || !n.NextAttemptAt.After(time.Now()) {
		t.Fatalf("after one 502: %+v", n)
	}
	// Make it due again twice.
	for i := 0; i < 2; i++ {
		_ = st.InTx(context.Background(), func(tx *store.Tx) error {
			return tx.MarkNotification(context.Background(), n.ID, store.NotifPending, n.Attempts+i, time.Now().Add(-time.Second), "", "", nil)
		})
		d.Flush(context.Background())
	}
	if n := row(t, st); n.Status != store.NotifFailed || len(bot.calls) != 3 {
		t.Errorf("after 3 attempts: %+v, calls %d", n, len(bot.calls))
	}
}

func TestBadRequestIsNotRetried(t *testing.T) {
	d, st, bot := setup(t, http.StatusBadRequest)
	d.Flush(context.Background())
	d.Flush(context.Background())
	if n := row(t, st); n.Status != store.NotifFailed || len(bot.calls) != 1 {
		t.Errorf("row = %+v, calls = %d", n, len(bot.calls))
	}
}

func TestLogModeSendsNothing(t *testing.T) {
	d, st, bot := setup(t)
	d.cfg.Mode = ModeLog
	rep, _ := d.Flush(context.Background())
	if rep.Logged != 1 || len(bot.calls) != 0 || row(t, st).Status != store.NotifLogged {
		t.Errorf("rep = %+v, calls = %d", rep, len(bot.calls))
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second, 270 * time.Second, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := backoff(i); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i, got, w)
		}
	}
}
