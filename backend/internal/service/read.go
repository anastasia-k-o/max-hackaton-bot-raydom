package service

import (
	"context"
	"errors"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// EventView is an event with what a card needs around it.
type EventView struct {
	Event  domain.Event
	Counts Counts
	Mine   *domain.Registration // the viewer's active registration
}

// ClosesAt is when sign-up ends.
func (s *Service) ClosesAt(e domain.Event) string {
	return domain.FormatTime(e.StartsAt.Add(-s.cfg.Timing.RegistrationCloses), e.Location())
}

// EventURL is the card link for an event.
func (s *Service) EventURL(id string) string { return s.eventURL(id) }

// Event loads the full card. Only verified users see it: time, address and
// seats are what the spec keeps from anonymous visitors.
func (s *Service) Event(ctx context.Context, viewer domain.User, id string) (EventView, error) {
	if err := requireVerified(viewer); err != nil {
		return EventView{}, err
	}
	var v EventView
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		v, err = s.view(ctx, tx, &viewer, id)
		return err
	})
	return v, err
}

func (s *Service) view(ctx context.Context, tx *store.Tx, viewer *domain.User, id string) (EventView, error) {
	e, err := tx.Event(ctx, id)
	if err != nil {
		return EventView{}, notFound(err, "event", id)
	}
	regs, err := tx.RegistrationsByEvent(ctx, e.ID)
	if err != nil {
		return EventView{}, err
	}
	v := EventView{Event: e, Counts: counts(e, regs)}
	if viewer != nil {
		mine, err := tx.ActiveRegistration(ctx, e.ID, viewer.ID)
		switch {
		case err == nil:
			v.Mine = &mine
		case !errors.Is(err, store.ErrNotFound):
			return v, err
		}
	}
	return v, nil
}
