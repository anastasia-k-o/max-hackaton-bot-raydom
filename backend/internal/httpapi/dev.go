package httpapi

import (
	"net/http"
	"strings"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/service"
)

// Dev tools, mounted only with DEV_MODE=true. They let one person test the
// whole chain: log in as the organiser and as the participant, fill an
// event with demo people, move the clock to "tomorrow at 13:00" and have
// the reminders go out right now.
func (a *api) devRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /dev/login", a.devLogin)
	mux.HandleFunc("GET /dev/state", a.devState)
	mux.HandleFunc("POST /dev/clock", a.devClock)
	mux.HandleFunc("POST /dev/scheduler/run", a.devRunScheduler)
	mux.HandleFunc("POST /dev/events/{id}/fill", a.devFill)
	mux.HandleFunc("POST /dev/registrations/{id}/cancel", a.devCancelAsOwner)
	mux.HandleFunc("POST /dev/reset", a.devReset)
}

func (a *api) devLogin(w http.ResponseWriter, r *http.Request) {
	var in service.DevUser
	if err := decode(r, &in); err != nil {
		a.writeError(w, r, err)
		return
	}
	if in.IsAuthor == nil {
		yes := true
		in.IsAuthor = &yes
	}
	u, token, err := a.Service.DevLogin(r.Context(), in)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": map[string]any{
		"id": u.ID, "max_user_id": u.MaxUserID, "first_name": u.FirstName, "last_name": u.LastName,
		"is_verified": u.IsVerified, "is_author": u.IsAuthor, "bot_available": u.BotAvailable,
	}})
}

func (a *api) devState(w http.ResponseWriter, r *http.Request) {
	st, err := a.Service.State(r.Context(), r.URL.Query().Get("payload") != "")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// devClock moves the server's clock. Body: {"advance": "23h30m"},
// {"set": "2026-09-29T13:00:00+03:00"} or {"reset": true}. The scheduler
// then runs at once, so the answer already lists what became due.
func (a *api) devClock(w http.ResponseWriter, r *http.Request) {
	if a.Clock == nil {
		a.writeError(w, r, domain.Conflict("the clock cannot be moved in this build"))
		return
	}
	var body struct {
		Advance string `json:"advance"`
		Set     string `json:"set"`
		Reset   bool   `json:"reset"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	switch {
	case body.Reset:
		a.Clock.Reset()
	case body.Advance != "":
		d, err := time.ParseDuration(strings.TrimSpace(body.Advance))
		if err != nil {
			a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "advance", Message: "длительность вида 90m, 23h30m"}}))
			return
		}
		a.Clock.Advance(d)
	case body.Set != "":
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(body.Set))
		if err != nil {
			a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "set", Message: "RFC 3339, например 2026-09-29T13:00:00+03:00"}}))
			return
		}
		a.Clock.Set(t)
	default:
		a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "advance", Message: "нужно advance, set или reset"}}))
		return
	}
	a.Log.Warn("dev clock moved", "now", a.Clock.Now().Format(time.RFC3339), "offset", a.Clock.Offset().String())
	a.runScheduler(w, r)
}

func (a *api) devRunScheduler(w http.ResponseWriter, r *http.Request) { a.runScheduler(w, r) }

// runScheduler runs one tick and delivers the result before answering.
func (a *api) runScheduler(w http.ResponseWriter, r *http.Request) {
	tick, err := a.Service.Tick(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	flush, err := a.Dispatcher.Flush(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	offset := "0s"
	if a.Clock != nil {
		offset = a.Clock.Offset().String()
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": tick.Now, "clock_offset": offset, "scheduler": tick, "delivery": flush})
}

func (a *api) devFill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count int `json:"count"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	if body.Count == 0 {
		body.Count = 1
	}
	ids, err := a.Service.FillEvent(r.Context(), r.PathValue("id"), body.Count)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registration_ids": ids})
}

func (a *api) devCancelAsOwner(w http.ResponseWriter, r *http.Request) {
	res, err := a.Service.CancelAsOwner(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	flush, _ := a.Dispatcher.Flush(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "delivery": flush})
}

func (a *api) devReset(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Reset(r.Context()); err != nil {
		a.writeError(w, r, err)
		return
	}
	if a.Clock != nil {
		a.Clock.Reset()
	}
	if a.Reseed != nil {
		if err := a.Reseed(r.Context()); err != nil {
			a.writeError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}
