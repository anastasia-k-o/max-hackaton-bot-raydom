package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/service"
	"hackatonCore/internal/store"
)

// actionBody is what both clients send to an action endpoint. The bot fills
// every field; the mini app may send only cancel_reason.
type actionBody struct {
	RegistrationID string `json:"registration_id"`
	EventID        string `json:"event_id"`
	MaxUserID      int64  `json:"max_user_id"`
	RequestID      string `json:"request_id"`
	OccurredAt     string `json:"occurred_at"`
	CancelReason   string `json:"cancel_reason"`
}

type actionKind int

const (
	actConfirm actionKind = iota
	actCancel
	actAccept
	actDecline
)

func (a *api) action(kind actionKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body actionBody
		if err := decode(r, &body); err != nil {
			a.writeError(w, r, err)
			return
		}
		regID := r.PathValue("id")
		if body.RegistrationID != "" && body.RegistrationID != regID {
			a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "registration_id", Message: "does not match the path"}}))
			return
		}
		actor, err := a.actor(r, body.MaxUserID)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		a.idempotent(w, r, actor.ID, body.RequestID, func() (int, any, error) {
			var (
				res *service.ActionResult
				err error
			)
			ctx := r.Context()
			switch kind {
			case actConfirm:
				res, err = a.Service.Confirm(ctx, actor, regID)
			case actCancel:
				res, err = a.Service.Cancel(ctx, actor, regID, body.CancelReason)
			case actAccept:
				var at time.Time
				if body.OccurredAt != "" {
					at, _ = time.Parse(time.RFC3339, body.OccurredAt)
				}
				res, err = a.Service.Accept(ctx, actor, regID, at)
			case actDecline:
				res, err = a.Service.Decline(ctx, actor, regID)
			}
			return http.StatusOK, res, err
		})
	}
}

// idempotent replays the stored answer when the same request id comes back
// for the same actor and path. Only successes are stored: a refusal is
// recomputed from the current state, which gives the same answer anyway.
func (a *api) idempotent(w http.ResponseWriter, r *http.Request, actorID, bodyRequestID string, fn func() (int, any, error)) {
	rid := strings.TrimSpace(bodyRequestID)
	if rid == "" {
		rid = strings.TrimSpace(r.Header.Get("X-Request-Id"))
	}
	key := ""
	if rid != "" {
		key = actorID + " " + r.Method + " " + r.URL.Path + " " + rid
		var stored *store.StoredResponse
		_ = a.Store.InTx(r.Context(), func(tx *store.Tx) error {
			var err error
			stored, err = tx.Idempotent(r.Context(), key)
			return err
		})
		if stored != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(stored.StatusCode)
			_, _ = w.Write(stored.Body)
			return
		}
	}
	status, out, err := fn()
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	body, _ := json.Marshal(out)
	if key != "" {
		ctx := context.WithoutCancel(r.Context())
		_ = a.Store.InTx(ctx, func(tx *store.Tx) error {
			return tx.RememberResponse(ctx, key, status, body, time.Now())
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

type botStatusBody struct {
	MaxUserID  int64  `json:"max_user_id"`
	Available  *bool  `json:"available"`
	Reason     string `json:"reason"`
	OccurredAt string `json:"occurred_at"`
	RequestID  string `json:"request_id"`
}

// botStatus records whether the bot can write to a user. Only the bot knows.
func (a *api) botStatus(w http.ResponseWriter, r *http.Request) {
	bot, err := a.fromBot(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if !bot {
		a.writeError(w, r, errUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("max_user_id"), 10, 64)
	if err != nil {
		a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "max_user_id", Message: "число"}}))
		return
	}
	var body botStatusBody
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	if body.Available == nil {
		a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "available", Message: "обязательно"}}))
		return
	}
	if body.MaxUserID != 0 && body.MaxUserID != id {
		a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "max_user_id", Message: "does not match the path"}}))
		return
	}
	var at time.Time
	if body.OccurredAt != "" {
		at, _ = time.Parse(time.RFC3339, body.OccurredAt)
	}
	applied, err := a.Service.ReportBotStatus(r.Context(), id, *body.Available, body.Reason, at)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "accepted", "applied": applied})
}

func (a *api) createRegistration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EventID   string `json:"event_id"`
		RequestID string `json:"request_id"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	actor, err := a.sessionUser(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if body.EventID == "" {
		a.writeError(w, r, domain.Invalid([]domain.FieldError{{Field: "event_id", Message: "обязательно"}}))
		return
	}
	a.idempotent(w, r, actor.ID, body.RequestID, func() (int, any, error) {
		reg, err := a.Service.Register(r.Context(), actor, body.EventID)
		if err != nil {
			return 0, nil, err
		}
		v, err := a.Service.Event(r.Context(), actor, reg.EventID)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, toRegistrationDTO(reg, v.Event.Location()), nil
	})
}

func (a *api) createEvent(w http.ResponseWriter, r *http.Request) {
	actor, err := a.sessionUser(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var in service.EventInput
	if err := decode(r, &in); err != nil {
		a.writeError(w, r, err)
		return
	}
	a.idempotent(w, r, actor.ID, "", func() (int, any, error) {
		e, err := a.Service.CreateEvent(r.Context(), actor, in)
		if err != nil {
			return 0, nil, err
		}
		v, err := a.Service.Event(r.Context(), actor, e.ID)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, a.toEventDetails(v), nil
	})
}

func (a *api) getEvent(w http.ResponseWriter, r *http.Request) {
	actor, err := a.sessionUser(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	v, err := a.Service.Event(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toEventDetails(v))
}

func (a *api) updateEvent(w http.ResponseWriter, r *http.Request) {
	actor, err := a.sessionUser(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var in service.EventInput
	if err := decode(r, &in); err != nil {
		a.writeError(w, r, err)
		return
	}
	res, err := a.Service.UpdateEvent(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	v, err := a.Service.Event(r.Context(), actor, res.Event.ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	dto := a.toEventDetails(v)
	dto.NotifiedCount = &res.NotifiedCount
	writeJSON(w, http.StatusOK, dto)
}

func (a *api) cancelEvent(w http.ResponseWriter, r *http.Request) {
	actor, err := a.sessionUser(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var body struct {
		OrganizerMessage string `json:"organizer_message"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	status, n, err := a.Service.CancelEvent(r.Context(), actor, r.PathValue("id"), body.OrganizerMessage)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "notified_count": n})
}

func (a *api) messageParticipants(w http.ResponseWriter, r *http.Request) {
	actor, err := a.sessionUser(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var body struct {
		OrganizerMessage string `json:"organizer_message"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	n, err := a.Service.MessageParticipants(r.Context(), actor, r.PathValue("id"), body.OrganizerMessage)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "accepted", "notified_count": n})
}
