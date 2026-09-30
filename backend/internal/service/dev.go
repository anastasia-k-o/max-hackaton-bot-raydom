package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// Dev-mode helpers. They exist so that one person can test the whole chain:
// play the organiser and the participant, fill an event with demo people,
// and make one of them leave to free a seat.

// FillEvent signs up count demo participants (users without a MAX id, so
// nothing is ever sent to them).
func (s *Service) FillEvent(ctx context.Context, eventID string, count int) ([]string, error) {
	if count <= 0 || count > 200 {
		return nil, domain.Invalid([]domain.FieldError{{Field: "count", Message: "от 1 до 200"}})
	}
	var ids []string
	for i := 0; i < count; i++ {
		u, _, err := s.DevLogin(ctx, DevUser{FirstName: "Демо", LastName: fmt.Sprintf("Участник %d", i+1)})
		if err != nil {
			return ids, err
		}
		r, err := s.Register(ctx, u, eventID)
		if err != nil {
			return ids, err
		}
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// CancelAsOwner cancels a registration on behalf of whoever holds it. It is
// how a demo participant «передумал» and frees a seat for the queue.
func (s *Service) CancelAsOwner(ctx context.Context, regID string) (*ActionResult, error) {
	var owner domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		r, err := tx.Registration(ctx, regID)
		if err != nil {
			return notFound(err, "registration", regID)
		}
		owner, err = tx.User(ctx, r.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Cancel(ctx, owner, regID, "plans_changed")
}

// DevState is a snapshot for GET /dev/state.
type DevState struct {
	Now    string            `json:"now"`
	Offset string            `json:"clock_offset"`
	Timing map[string]string `json:"timing"`
	Events []DevEvent        `json:"events"`
	Outbox []DevNotif        `json:"notifications"`
}

// DevEvent is an event with its registrations.
type DevEvent struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	StartsAt      string   `json:"starts_at"`
	StartsIn      string   `json:"starts_in"`
	Status        string   `json:"status"`
	Capacity      *int     `json:"capacity"`
	Registered    int      `json:"registered"`
	Waitlist      int      `json:"waitlist"`
	Registrations []DevReg `json:"registrations"`
}

// DevReg is one registration.
type DevReg struct {
	ID             string  `json:"id"`
	User           string  `json:"user"`
	MaxUserID      *int64  `json:"max_user_id"`
	Status         string  `json:"status"`
	QueuePosition  *int    `json:"queue_position,omitempty"`
	OfferExpiresAt *string `json:"offer_expires_at,omitempty"`
	CancelReason   *string `json:"cancel_reason,omitempty"`
}

// DevNotif is one outbox row.
type DevNotif struct {
	ID             int64           `json:"id"`
	Type           string          `json:"type"`
	Status         string          `json:"status"`
	RegistrationID string          `json:"registration_id,omitempty"`
	EventID        string          `json:"event_id"`
	Attempts       int             `json:"attempts"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      string          `json:"created_at"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

// State builds the dev snapshot.
func (s *Service) State(ctx context.Context, withPayload bool) (DevState, error) {
	now := s.clock.Now()
	st := DevState{Now: domain.FormatTime(now, domain.LocationOf("Europe/Moscow")), Offset: "0s", Events: []DevEvent{}, Outbox: []DevNotif{}}
	if tc, ok := s.clock.(interface{ Offset() time.Duration }); ok {
		st.Offset = tc.Offset().String()
	}
	t := s.cfg.Timing
	st.Timing = map[string]string{
		"reminder_24h": t.Reminder24h.String(), "confirmation": t.Confirmation.String(),
		"confirmation_retry": t.ConfirmationRetry.String(), "reminder_1h": t.Reminder1h.String(),
		"registration_closes": t.RegistrationCloses.String(), "offer_ttl": t.OfferTTL.String(),
		"offer_ttl_soon": t.OfferTTLSoon.String(),
	}
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		events, err := tx.Events(ctx, "")
		if err != nil {
			return err
		}
		users, err := tx.Users(ctx)
		if err != nil {
			return err
		}
		for _, e := range events {
			regs, err := tx.RegistrationsByEvent(ctx, e.ID)
			if err != nil {
				return err
			}
			c := counts(e, regs)
			de := DevEvent{ID: e.ID, Title: e.Title, StartsAt: domain.FormatTime(e.StartsAt, e.Location()),
				StartsIn: e.StartsAt.Sub(now).Round(time.Second).String(), Status: e.Status, Capacity: e.Capacity,
				Registered: c.Registered, Waitlist: c.Waitlist, Registrations: []DevReg{}}
			for _, r := range regs {
				u := users[r.UserID]
				dr := DevReg{ID: r.ID, User: u.DisplayName(), MaxUserID: u.MaxUserID, Status: r.Status,
					QueuePosition: r.QueuePosition, CancelReason: r.CancelReason}
				if r.OfferExpiresAt != nil {
					dr.OfferExpiresAt = ptr(domain.FormatTime(*r.OfferExpiresAt, e.Location()))
				}
				de.Registrations = append(de.Registrations, dr)
			}
			st.Events = append(st.Events, de)
		}
		notifs, err := tx.RecentNotifications(ctx, 50)
		if err != nil {
			return err
		}
		for _, n := range notifs {
			dn := DevNotif{ID: n.ID, Type: n.Type, Status: n.Status, RegistrationID: n.RegistrationID, EventID: n.EventID,
				Attempts: n.Attempts, LastError: n.LastError, CreatedAt: n.CreatedAt.Format(time.RFC3339)}
			if withPayload {
				dn.Payload = n.Payload
			}
			st.Outbox = append(st.Outbox, dn)
		}
		return nil
	})
	return st, err
}
