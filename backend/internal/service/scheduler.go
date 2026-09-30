package service

import (
	"context"
	"fmt"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// TickReport says what one scheduler pass did.
type TickReport struct {
	Now           string   `json:"now"`
	Queued        []string `json:"queued"`         // "type → registration"
	ExpiredOffers []string `json:"expired_offers"` // registration ids
}

// Tick is one pass of the scheduler: expire stale offers, then queue every
// time-based message that is due. It is safe to run as often as you like —
// each message has a dedup key and is queued once.
//
// Every rule has an upper bound as well as a lower one: a reminder that is
// due 24 hours before the start is not sent 3 hours before because the
// server was down in between; the next milestone takes over instead.
func (s *Service) Tick(ctx context.Context) (TickReport, error) {
	now := s.clock.Now()
	rep := TickReport{Now: now.Format(time.RFC3339), Queued: []string{}, ExpiredOffers: []string{}}
	t := s.cfg.Timing

	err := s.run(ctx, func(tx *store.Tx) error {
		offered, err := tx.RegistrationsByStatus(ctx, domain.RegOffered)
		if err != nil {
			return err
		}
		for _, r := range offered {
			if r.OfferExpiresAt == nil || r.OfferExpiresAt.After(now) {
				continue
			}
			e, err := tx.Event(ctx, r.EventID)
			if err != nil {
				return err
			}
			if err := s.expireOffer(ctx, tx, r, e, now); err != nil {
				return err
			}
			rep.ExpiredOffers = append(rep.ExpiredOffers, r.ID)
		}

		events, err := tx.UpcomingEvents(ctx, now, now.Add(t.Reminder24h))
		if err != nil {
			return err
		}
		for _, e := range events {
			regs, err := tx.RegistrationsByEvent(ctx, e.ID)
			if err != nil {
				return err
			}
			left := e.StartsAt.Sub(now)
			for _, r := range regs {
				if r.Status != domain.RegRegistered && r.Status != domain.RegConfirmed {
					continue
				}
				for _, typ := range s.dueTypes(ctx, tx, e, r, left) {
					user, err := tx.User(ctx, r.UserID)
					if err != nil {
						return err
					}
					key := fmt.Sprintf("%s:%s:%d", typ, r.ID, e.StartsAt.Unix())
					added, err := s.enqueue(ctx, tx, typ, user, e, &r, nil, key, now)
					if err != nil {
						return err
					}
					if added {
						rep.Queued = append(rep.Queued, typ+" → "+r.ID)
					}
				}
			}
		}
		return nil
	})
	return rep, err
}

// dueTypes lists the messages a registration should get at this moment.
// The key includes the start time, so moving an event re-arms its reminders.
func (s *Service) dueTypes(ctx context.Context, tx *store.Tx, e domain.Event, r domain.Registration, left time.Duration) []string {
	t := s.cfg.Timing
	start := e.StartsAt
	var due []string

	// Signing up 20 hours before is not a reason to get "tomorrow!" as well
	// as "you're in": the day-before reminder is for early birds only.
	if left <= t.Reminder24h && left > t.Confirmation && !r.CreatedAt.After(start.Add(-t.Reminder24h)) {
		due = append(due, TypeReminder24h)
	}
	if r.Status == domain.RegRegistered && left <= t.Confirmation && left > t.RegistrationCloses {
		due = append(due, TypeConfirmationRequired)
	}
	if r.Status == domain.RegRegistered && left <= t.ConfirmationRetry && left > t.RegistrationCloses {
		// A retry only follows a first request that was actually made.
		first := fmt.Sprintf("%s:%s:%d", TypeConfirmationRequired, r.ID, start.Unix())
		if sent, err := tx.HasNotification(ctx, first); err == nil && sent {
			due = append(due, TypeConfirmationRetry)
		}
	}
	if left <= t.Reminder1h && left > 0 && !r.CreatedAt.After(start.Add(-t.Reminder1h)) {
		due = append(due, TypeReminder1h)
	}
	return due
}
