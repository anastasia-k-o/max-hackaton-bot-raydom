// Package httpapi is the backend's HTTP surface: the endpoints the bot calls
// when a button is pressed, the ones the mini app calls, and in dev mode the
// tools for testing it all alone.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hackatonCore/internal/clock"
	"hackatonCore/internal/domain"
	"hackatonCore/internal/notify"
	"hackatonCore/internal/service"
	"hackatonCore/internal/store"
)

// Deps is what the handlers need.
type Deps struct {
	Service    *service.Service
	Store      *store.Store
	Dispatcher *notify.Dispatcher
	Clock      *clock.Travel // nil outside dev mode
	BotAPIKey  string
	DevMode    bool
	Log        *slog.Logger
	// Reseed refills demo data after /dev/reset.
	Reseed func(context.Context) error
	// Auth configures POST /auth/max.
	Auth service.AuthConfig
}

type api struct{ Deps }

const maxBody = 64 << 10

// NewHandler builds the router. Every path is served both at the root (what
// the bot's CORE_BASE_URL points at) and under /api (the mini app's default
// VITE_API_URL), so one deployment serves both clients.
func NewHandler(d Deps) http.Handler {
	a := &api{d}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /categories", a.categories)

	// Button actions: from the bot (X-Api-Key) or the mini app (session).
	mux.HandleFunc("POST /registrations/{id}/confirm", a.action(actConfirm))
	mux.HandleFunc("POST /registrations/{id}/cancel", a.action(actCancel))
	mux.HandleFunc("POST /waitlist-offers/{id}/accept", a.action(actAccept))
	mux.HandleFunc("POST /waitlist-offers/{id}/decline", a.action(actDecline))
	mux.HandleFunc("POST /max-users/{max_user_id}/bot-status", a.botStatus)

	// Mini app.
	mux.HandleFunc("POST /registrations", a.createRegistration)
	mux.HandleFunc("POST /events", a.createEvent)
	mux.HandleFunc("GET /events/{id}", a.getEvent)
	mux.HandleFunc("PATCH /events/{id}", a.updateEvent)
	mux.HandleFunc("POST /events/{id}/cancel", a.cancelEvent)
	mux.HandleFunc("POST /events/{id}/messages", a.messageParticipants)
	mux.HandleFunc("GET /events", a.catalog)
	mux.HandleFunc("GET /events/{id}/report", a.report)
	mux.HandleFunc("POST /events/{id}/views", a.addView)
	mux.HandleFunc("POST /events/{id}/complaints", a.complain)
	mux.HandleFunc("GET /recommendations", a.recommendations)
	mux.HandleFunc("POST /ml/suggest-tags", a.suggestTags)

	// Session and profile.
	mux.HandleFunc("POST /auth/max", a.authMax)
	mux.HandleFunc("GET /me", a.me)
	mux.HandleFunc("PATCH /me", a.updateMe)
	mux.HandleFunc("POST /me/verification", a.verify)
	mux.HandleFunc("POST /me/consent", a.consent)
	mux.HandleFunc("GET /me/interests", a.getInterests)
	mux.HandleFunc("PUT /me/interests", a.setInterests)
	mux.HandleFunc("GET /me/registrations", a.myRegistrations)
	mux.HandleFunc("GET /me/events", a.myEvents)

	if d.DevMode {
		a.devRoutes(mux)
	}

	root := http.NewServeMux()
	root.Handle("/api/", http.StripPrefix("/api", mux))
	root.Handle("/", mux)
	return a.middleware(root)
}

type ctxKey int

const requestIDKey ctxKey = 1

// middleware assigns a request id, limits the body and logs the request.
func (a *api) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if rid == "" || len(rid) > 128 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			rid = "req-" + hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", rid)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, rid))
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path != "/health" {
			a.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", rid)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string              `json:"code"`
	Message   string              `json:"message"`
	RequestID string              `json:"request_id,omitempty"`
	Details   []domain.FieldError `json:"details,omitempty"`
}

// writeError renders the error envelope both clients already parse. A
// business refusal is always 4xx: the bot keeps buttons in place on 5xx,
// and a refusal that never changes would have the user press forever.
func (a *api) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var de *domain.Error
	if errors.As(err, &de) && de.Status >= 400 {
		writeJSON(w, de.Status, errorBody{Error: errorDetail{Code: de.Code, Message: de.Message, RequestID: requestID(r), Details: de.Details}})
		return
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: errorDetail{Code: "payload_too_large", Message: "body exceeds 64 KiB", RequestID: requestID(r)}})
		return
	}
	a.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err, "request_id", requestID(r))
	writeJSON(w, http.StatusInternalServerError, errorBody{Error: errorDetail{Code: "internal_error", Message: "internal error", RequestID: requestID(r)}})
}

// decode reads a JSON body. An empty body decodes to the zero value: the
// bot's action body is optional for the rules, and a mini app PATCH may
// legitimately send {}.
func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return domain.Errorf(http.StatusBadRequest, "invalid_request", "body is not valid JSON for this endpoint: %v", err)
	}
	return nil
}

func (a *api) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *api) categories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": domain.Categories})
}
