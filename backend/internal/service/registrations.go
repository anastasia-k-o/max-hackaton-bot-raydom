package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// ActionResult is the answer to a registration action. Both the bot and the
// mini app read it: {status: accepted|noop, event?}.
type ActionResult struct {
	Status string        `json:"status"`
	Event  *EventSummary `json:"event,omitempty"`
}

// EventSummary is the event block of an action result. The bot uses it to
// show fresh data in the message that replaces the one with buttons.
type EventSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	StartsAt   string `json:"starts_at"`
	Address    string `json:"address,omitempty"`
	MiniAppURL string `json:"mini_app_url,omitempty"`
}

func (s *Service) summary(e domain.Event) *EventSummary {
	return &EventSummary{
		ID: e.ID, Title: e.Title, Address: e.Address, MiniAppURL: s.eventURL(e.ID),
		StartsAt: domain.FormatTime(e.StartsAt, e.Location()),
	}
}

func (s *Service) result(status string, e domain.Event) *ActionResult {
	return &ActionResult{Status: status, Event: s.summary(e)}
}

// Counts are the seat numbers of an event.
type Counts struct {
	Registered int // seats taken, demo participants included
	Waitlist   int
}

func counts(e domain.Event, regs []domain.Registration) Counts {
	c := Counts{Registered: e.ExtraRegistered, Waitlist: e.ExtraWaitlist}
	for _, r := range regs {
		switch {
		case domain.HoldsSeat(r.Status):
			c.Registered++
		case r.Status == domain.RegWaitlist:
			c.Waitlist++
		}
	}
	return c
}

func isFull(e domain.Event, c Counts) bool {
	return e.Capacity != nil && c.Registered >= *e.Capacity
}

// CancelReasons the mini app offers.
var CancelReasons = []string{"ill", "plans_changed", "other"}

// Register signs the user up for an event, or puts them in the queue when
// it is full.
func (s *Service) Register(ctx context.Context, actor domain.User, eventID string) (domain.Registration, error) {
	var reg domain.Registration
	err := s.run(ctx, func(tx *store.Tx) error {
		if err := requireVerified(actor); err != nil {
			return err
		}
		now := s.clock.Now()
		e, err := tx.Event(ctx, eventID)
		if err != nil {
			return notFound(err, "event", eventID)
		}
		switch {
		case e.Status == domain.EventCancelled:
			return domain.Errorf(http.StatusConflict, "event_cancelled", "event %s is cancelled", e.ID)
		case e.Status != domain.EventPublished:
			return domain.Conflict("event %s is not published (%s)", e.ID, e.Status)
		case e.AuthorID == actor.ID:
			return domain.Errorf(http.StatusConflict, "author_cannot_register", "author is the organiser of %s", e.ID)
		case !now.Before(e.StartsAt.Add(-s.cfg.Timing.RegistrationCloses)):
			return domain.Errorf(http.StatusConflict, "registration_closed", "registration for %s closed %s before the start", e.ID, s.cfg.Timing.RegistrationCloses)
		}
		if _, err := tx.ActiveRegistration(ctx, e.ID, actor.ID); err == nil {
			return domain.Errorf(http.StatusConflict, "already_registered", "user %s already has an active registration for %s", actor.ID, e.ID)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		regs, err := tx.RegistrationsByEvent(ctx, e.ID)
		if err != nil {
			return err
		}
		c := counts(e, regs)
		reg = domain.Registration{ID: newID("registration"), EventID: e.ID, UserID: actor.ID, CreatedAt: now}
		switch {
		case isFull(e, c):
			reg.Status = domain.RegWaitlist
			reg.QueuePosition = ptr(c.Waitlist + 1)
		case e.StartsAt.Sub(now) < s.cfg.Timing.Confirmation:
			// Inside the confirmation window there is no time to ask: the
			// act of signing up is the confirmation.
			reg.Status = domain.RegConfirmed
			reg.ConfirmedAt = ptr(now)
		default:
			reg.Status = domain.RegRegistered
		}
		if err := tx.SaveRegistration(ctx, reg); err != nil {
			return err
		}
		// The bot has no «вы в листе ожидания» message; the queue is shown
		// in the mini app, and the bot speaks up once a seat is offered.
		if reg.Status != domain.RegWaitlist {
			if _, err := s.enqueue(ctx, tx, TypeRegistrationCreated, actor, e, &reg, nil, TypeRegistrationCreated+":"+reg.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return reg, err
}

// ownedRegistration loads a registration of the actor. Someone else's
// registration is reported as missing, not forbidden: its existence is not
// the caller's business.
func ownedRegistration(ctx context.Context, tx *store.Tx, actor domain.User, id string) (domain.Registration, domain.Event, error) {
	r, err := tx.Registration(ctx, id)
	if err != nil {
		return r, domain.Event{}, notFound(err, "registration", id)
	}
	if r.UserID != actor.ID {
		return r, domain.Event{}, domain.NotFound("registration", id)
	}
	e, err := tx.Event(ctx, r.EventID)
	if err != nil {
		return r, e, notFound(err, "event", r.EventID)
	}
	return r, e, nil
}

// Confirm is «Приду».
func (s *Service) Confirm(ctx context.Context, actor domain.User, regID string) (*ActionResult, error) {
	var out *ActionResult
	err := s.run(ctx, func(tx *store.Tx) error {
		r, e, err := ownedRegistration(ctx, tx, actor, regID)
		if err != nil {
			return err
		}
		switch {
		case e.Status == domain.EventCancelled:
			return domain.Errorf(http.StatusConflict, "event_cancelled", "event %s is cancelled", e.ID)
		case r.Status == domain.RegCancelled:
			return domain.Errorf(http.StatusConflict, "registration_cancelled", "registration %s is cancelled", r.ID)
		case r.Status == domain.RegConfirmed:
			out = s.result("noop", e)
			return nil
		case r.Status != domain.RegRegistered:
			return domain.Conflict("cannot confirm registration %s from status %s", r.ID, r.Status)
		}
		now := s.clock.Now()
		r.Status, r.ConfirmedAt = domain.RegConfirmed, ptr(now)
		if err := tx.SaveRegistration(ctx, r); err != nil {
			return err
		}
		out = s.result("accepted", e)
		return nil
	})
	return out, err
}

// Cancel is «Не смогу» / «Отменить запись» / leaving the queue.
func (s *Service) Cancel(ctx context.Context, actor domain.User, regID, reason string) (*ActionResult, error) {
	if reason != "" && !slices.Contains(CancelReasons, reason) {
		return nil, domain.Invalid([]domain.FieldError{{Field: "cancel_reason", Message: "one of ill, plans_changed, other"}})
	}
	var out *ActionResult
	err := s.run(ctx, func(tx *store.Tx) error {
		r, e, err := ownedRegistration(ctx, tx, actor, regID)
		if err != nil {
			return err
		}
		if r.Status == domain.RegCancelled {
			out = s.result("noop", e)
			return nil
		}
		if e.Status == domain.EventCancelled {
			return domain.Errorf(http.StatusConflict, "event_cancelled", "event %s is cancelled", e.ID)
		}
		now := s.clock.Now()
		heldSeat := domain.HoldsSeat(r.Status)
		r.Status, r.CancelledAt, r.QueuePosition, r.OfferExpiresAt = domain.RegCancelled, ptr(now), nil, nil
		if reason != "" {
			r.CancelReason = ptr(reason)
		}
		r.CancelledLate = e.StartsAt.Sub(now) < s.cfg.Timing.RegistrationCloses
		if err := tx.SaveRegistration(ctx, r); err != nil {
			return err
		}
		if err := s.afterSeatChange(ctx, tx, e, heldSeat, now); err != nil {
			return err
		}
		out = s.result("accepted", e)
		return nil
	})
	return out, err
}

// Accept is «Занять место». occurredAt is when the user pressed the button,
// as MAX reports it; a click made in time counts even if its delivery was late.
func (s *Service) Accept(ctx context.Context, actor domain.User, regID string, occurredAt time.Time) (*ActionResult, error) {
	var (
		out     *ActionResult
		expired bool
	)
	err := s.run(ctx, func(tx *store.Tx) error {
		r, e, err := ownedRegistration(ctx, tx, actor, regID)
		if err != nil {
			return err
		}
		if e.Status == domain.EventCancelled {
			return domain.Errorf(http.StatusConflict, "event_cancelled", "event %s is cancelled", e.ID)
		}
		if r.CancelReason != nil && *r.CancelReason == domain.ReasonOfferExpired {
			expired = true
			return nil
		}
		switch r.Status {
		case domain.RegRegistered, domain.RegConfirmed:
			out = s.result("noop", e)
			return nil
		case domain.RegOffered:
		default:
			return domain.Conflict("no active offer for registration %s (status %s)", r.ID, r.Status)
		}

		now := s.clock.Now()
		if r.OfferExpiresAt != nil && s.clickTime(now, occurredAt).After(*r.OfferExpiresAt) {
			// Too late: expire it now instead of waiting for the scheduler,
			// so the seat moves on to the next person right away.
			if err := s.expireOffer(ctx, tx, r, e, now); err != nil {
				return err
			}
			expired = true
			return nil
		}
		soon := e.StartsAt.Sub(now) < s.cfg.Timing.Confirmation
		r.Status, r.FromWaitlist, r.QueuePosition, r.OfferExpiresAt = domain.RegRegistered, true, nil, nil
		if soon {
			r.Status, r.ConfirmedAt = domain.RegConfirmed, ptr(now)
		}
		if err := tx.SaveRegistration(ctx, r); err != nil {
			return err
		}
		out = s.result("accepted", e)
		return nil
	})
	if err == nil && expired {
		// Returned after the commit: the expiry itself must be kept.
		return nil, domain.Errorf(http.StatusGone, "offer_expired", "offer for %s expired", regID)
	}
	return out, err
}

// clickTime is the moment the button was pressed on the server's clock. The
// bot reports occurred_at in real time; in dev mode the server clock may be
// ahead, so the same offset is applied.
func (s *Service) clickTime(now, occurredAt time.Time) time.Time {
	if occurredAt.IsZero() {
		return now
	}
	if tc, ok := s.clock.(interface{ Offset() time.Duration }); ok {
		occurredAt = occurredAt.Add(tc.Offset())
	}
	if occurredAt.After(now) {
		return now
	}
	return occurredAt
}

// Decline is «Отказаться» from an offered seat.
func (s *Service) Decline(ctx context.Context, actor domain.User, regID string) (*ActionResult, error) {
	var out *ActionResult
	err := s.run(ctx, func(tx *store.Tx) error {
		r, e, err := ownedRegistration(ctx, tx, actor, regID)
		if err != nil {
			return err
		}
		if r.Status == domain.RegCancelled {
			out = s.result("noop", e)
			return nil
		}
		if r.Status != domain.RegOffered {
			return domain.Conflict("no active offer for registration %s (status %s)", r.ID, r.Status)
		}
		now := s.clock.Now()
		r.Status, r.CancelledAt, r.CancelReason = domain.RegCancelled, ptr(now), ptr(domain.ReasonOfferDeclined)
		r.QueuePosition, r.OfferExpiresAt = nil, nil
		if err := tx.SaveRegistration(ctx, r); err != nil {
			return err
		}
		if err := s.afterSeatChange(ctx, tx, e, true, now); err != nil {
			return err
		}
		out = s.result("accepted", e)
		return nil
	})
	return out, err
}

// expireOffer cancels an offer whose time ran out and passes the seat on.
func (s *Service) expireOffer(ctx context.Context, tx *store.Tx, r domain.Registration, e domain.Event, now time.Time) error {
	at := now
	if r.OfferExpiresAt != nil {
		at = *r.OfferExpiresAt
	}
	r.Status, r.CancelledAt, r.CancelReason = domain.RegCancelled, ptr(at), ptr(domain.ReasonOfferExpired)
	r.QueuePosition, r.OfferExpiresAt = nil, nil
	if err := tx.SaveRegistration(ctx, r); err != nil {
		return err
	}
	return s.afterSeatChange(ctx, tx, e, true, now)
}

// afterSeatChange renumbers the queue and, if a seat is free, offers it.
func (s *Service) afterSeatChange(ctx context.Context, tx *store.Tx, e domain.Event, seatFreed bool, now time.Time) error {
	if seatFreed {
		if err := s.fillFreeSeats(ctx, tx, &e, now); err != nil {
			return err
		}
	}
	return renumberQueue(ctx, tx, e)
}

// fillFreeSeats offers free seats to the queue, first come first served.
//
// Demo participants from the seed (ExtraWaitlist) queue ahead of everyone
// with a row, like in the mocks: when one of them is first, the seat simply
// goes to them.
func (s *Service) fillFreeSeats(ctx context.Context, tx *store.Tx, e *domain.Event, now time.Time) error {
	if e.Status != domain.EventPublished || !now.Before(e.StartsAt) || e.Capacity == nil {
		return nil
	}
	for {
		regs, err := tx.RegistrationsByEvent(ctx, e.ID)
		if err != nil {
			return err
		}
		if isFull(*e, counts(*e, regs)) {
			return nil
		}
		queue := waitlistOf(regs)
		if e.ExtraWaitlist > 0 && (len(queue) == 0 || *queue[0].QueuePosition > 1) {
			e.ExtraWaitlist--
			e.ExtraRegistered++
			if err := tx.SaveEvent(ctx, *e); err != nil {
				return err
			}
			for _, r := range queue {
				r.QueuePosition = ptr(*r.QueuePosition - 1)
				if err := tx.SaveRegistration(ctx, r); err != nil {
					return err
				}
			}
			continue
		}
		if len(queue) == 0 {
			return nil
		}
		if err := s.offerSeat(ctx, tx, queue[0], *e, now); err != nil {
			return err
		}
	}
}

func (s *Service) offerSeat(ctx context.Context, tx *store.Tx, r domain.Registration, e domain.Event, now time.Time) error {
	ttl := s.cfg.Timing.OfferTTL
	if e.StartsAt.Sub(now) < s.cfg.Timing.Confirmation {
		ttl = s.cfg.Timing.OfferTTLSoon
	}
	expires := now.Add(ttl).Truncate(time.Second)
	if expires.After(e.StartsAt) {
		expires = e.StartsAt
	}
	r.Status, r.OfferExpiresAt, r.QueuePosition = domain.RegOffered, &expires, nil
	if err := tx.SaveRegistration(ctx, r); err != nil {
		return err
	}
	user, err := tx.User(ctx, r.UserID)
	if err != nil {
		return err
	}
	data := &EnvelopeData{OfferExpiresAt: domain.FormatTime(expires, e.Location())}
	key := fmt.Sprintf("%s:%s:%d", TypeWaitlistOffer, r.ID, expires.Unix())
	_, err = s.enqueue(ctx, tx, TypeWaitlistOffer, user, e, &r, data, key, now)
	return err
}

// waitlistOf returns the queue in order.
func waitlistOf(regs []domain.Registration) []domain.Registration {
	var q []domain.Registration
	for _, r := range regs {
		if r.Status == domain.RegWaitlist {
			q = append(q, r)
		}
	}
	sort.SliceStable(q, func(i, j int) bool { return pos(q[i]) < pos(q[j]) })
	return q
}

func pos(r domain.Registration) int {
	if r.QueuePosition == nil {
		return 1 << 30
	}
	return *r.QueuePosition
}

// renumberQueue closes gaps left by people who left the queue. Demo
// participants keep the first ExtraWaitlist places.
func renumberQueue(ctx context.Context, tx *store.Tx, e domain.Event) error {
	regs, err := tx.RegistrationsByEvent(ctx, e.ID)
	if err != nil {
		return err
	}
	for i, r := range waitlistOf(regs) {
		want := e.ExtraWaitlist + i + 1
		if r.QueuePosition != nil && *r.QueuePosition == want {
			continue
		}
		r.QueuePosition = ptr(want)
		if err := tx.SaveRegistration(ctx, r); err != nil {
			return err
		}
	}
	return nil
}
