package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// EventInput is the body of POST /events and PATCH /events/{id}. Pointer
// fields tell "not sent" (nil) from "cleared"; for nullable fields a PATCH
// sends JSON null, which arrives as a set Null* flag.
type EventInput struct {
	Title            *string          `json:"title"`
	ShortDescription *string          `json:"short_description"`
	Description      *string          `json:"description"`
	CategoryID       *string          `json:"category_id"`
	TagIDs           *[]string        `json:"tag_ids"`
	StartsAt         *string          `json:"starts_at"`
	DurationMin      *int             `json:"duration_min"`
	CityID           *string          `json:"city_id"`
	District         *string          `json:"district"`
	Address          *string          `json:"address"`
	HowToFind        *string          `json:"how_to_find"`
	Capacity         Nullable[int]    `json:"capacity"`
	Level            Nullable[string] `json:"level"`
	AgeLimit         Nullable[string] `json:"age_limit"`
	Bring            Nullable[string] `json:"bring"`
	Contact          Nullable[string] `json:"contact"`
	// OrganizerMessage rides along with an edit and goes to participants
	// together with the list of changes.
	OrganizerMessage *string `json:"organizer_message"`
}

// Nullable is a JSON field that can be absent, null or a value.
type Nullable[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON records that the field was present, null or not.
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// apply merges the input into an event, returning the field errors.
func (in EventInput) apply(e *domain.Event, now time.Time, creating bool) []domain.FieldError {
	var errs []domain.FieldError
	bad := func(field, msg string) { errs = append(errs, domain.FieldError{Field: field, Message: msg}) }

	if in.Title != nil {
		e.Title = strings.TrimSpace(*in.Title)
	}
	if creating || in.Title != nil {
		if e.Title == "" || utf8.RuneCountInString(e.Title) > 80 {
			bad("title", "обязательно, до 80 символов")
		}
	}
	if in.ShortDescription != nil {
		e.ShortDescription = strings.TrimSpace(*in.ShortDescription)
		if utf8.RuneCountInString(e.ShortDescription) > 140 {
			bad("short_description", "до 140 символов")
		}
	}
	if in.Description != nil {
		e.Description = strings.TrimSpace(*in.Description)
	}
	if (creating || in.Description != nil) && e.Description == "" {
		bad("description", "обязательно")
	}
	if in.CategoryID != nil {
		e.CategoryID = *in.CategoryID
	}
	if (creating || in.CategoryID != nil) && !domain.IsCategory(e.CategoryID) {
		bad("category_id", "выберите направление")
	}
	if in.TagIDs != nil {
		e.TagIDs = *in.TagIDs
	}
	if creating || in.TagIDs != nil {
		ok := len(e.TagIDs) > 0
		for _, id := range e.TagIDs {
			if _, known := domain.TagByID(id); !known {
				ok = false
			}
		}
		if !ok {
			bad("tag_ids", "выберите хотя бы один тег")
		}
	}
	if in.CityID != nil {
		e.CityID = *in.CityID
	}
	city, cityOK := domain.CityByID(e.CityID)
	if creating || in.CityID != nil {
		if !cityOK {
			bad("city_id", "неизвестный город")
		} else {
			e.Timezone = city.Timezone
		}
	}
	if in.StartsAt != nil {
		t, err := parseCityTime(*in.StartsAt)
		switch {
		case err != nil:
			bad("starts_at", err.Error())
		case !t.After(now):
			bad("starts_at", "RFC 3339 со смещением, не в прошлом")
		default:
			e.StartsAt = t
		}
	} else if creating {
		bad("starts_at", "обязательно")
	}
	if in.DurationMin != nil {
		e.DurationMin = *in.DurationMin
	}
	if (creating || in.DurationMin != nil) && e.DurationMin <= 0 {
		bad("duration_min", "длительность в минутах")
	}
	if in.District != nil {
		e.District = *in.District
	}
	if (creating || in.District != nil || in.CityID != nil) && cityOK && !slices.Contains(city.Districts, e.District) {
		bad("district", "выберите район")
	}
	if in.Address != nil {
		e.Address = strings.TrimSpace(*in.Address)
	}
	if (creating || in.Address != nil) && (e.Address == "" || utf8.RuneCountInString(e.Address) > 512) {
		bad("address", "обязательно, до 512 символов")
	}
	if in.HowToFind != nil {
		e.HowToFind = strings.TrimSpace(*in.HowToFind)
	}
	if in.Capacity.Set {
		if in.Capacity.Value != nil && *in.Capacity.Value <= 0 {
			bad("capacity", "целое число больше 0 или пусто")
		} else {
			e.Capacity = in.Capacity.Value
		}
	}
	if in.Level.Set {
		e.Level = in.Level.Value
	}
	if in.AgeLimit.Set {
		e.AgeLimit = in.AgeLimit.Value
	}
	if in.Bring.Set {
		e.Bring = in.Bring.Value
	}
	if in.Contact.Set {
		if in.Contact.Value == nil || strings.TrimSpace(*in.Contact.Value) == "" {
			e.Contact = "Профиль автора в MAX"
		} else {
			e.Contact = strings.TrimSpace(*in.Contact.Value)
		}
	}
	if in.OrganizerMessage != nil && utf8.RuneCountInString(*in.OrganizerMessage) > 2000 {
		bad("organizer_message", "до 2000 символов")
	}
	return errs
}

// parseCityTime accepts RFC 3339 with the city's offset and refuses UTC, for
// the same reason the bot does: from a bare UTC time nobody can tell which
// city's clock to show.
func parseCityTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return t, fmt.Errorf("RFC 3339 со смещением города, например 2026-09-22T19:00:00+03:00")
	}
	if _, offset := t.Zone(); offset == 0 {
		return t, fmt.Errorf("нужно смещение города (+03:00), UTC не принимается")
	}
	return t, nil
}

func requireAuthor(actor domain.User, e domain.Event) error {
	if err := requireVerified(actor); err != nil {
		return err
	}
	if e.AuthorID != actor.ID {
		return domain.Errorf(http.StatusForbidden, "forbidden", "user %s is not the author of %s", actor.ID, e.ID)
	}
	return nil
}

// CreateEvent publishes an announcement, or sends it to moderation when the
// text trips the stop list.
func (s *Service) CreateEvent(ctx context.Context, actor domain.User, in EventInput) (domain.Event, error) {
	var e domain.Event
	err := s.run(ctx, func(tx *store.Tx) error {
		if err := requireVerified(actor); err != nil {
			return err
		}
		if !actor.IsAuthor {
			return domain.Errorf(http.StatusForbidden, "forbidden", "author role is off for %s", actor.ID)
		}
		now := s.clock.Now()
		e = domain.Event{
			ID: newID("event"), CityID: actor.CityID, Contact: "Профиль автора в MAX",
			AuthorID: actor.ID, AuthorName: actor.DisplayName(),
			PublishedAt: now, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		if e.CityID == "" {
			e.CityID = domain.DefaultCity
		}
		if errs := in.apply(&e, now, true); len(errs) > 0 {
			return domain.Invalid(errs)
		}
		e.ModerationFlags = domain.ModerationFlags(e.Title, e.Description)
		e.Status = domain.EventPublished
		if len(e.ModerationFlags) > 0 {
			e.Status = domain.EventModeration
		}
		return tx.SaveEvent(ctx, e)
	})
	return e, err
}

// UpdateResult is an edited event plus how many people were told.
type UpdateResult struct {
	Event         domain.Event
	NotifiedCount int
}

// UpdateEvent edits an announcement. A change of time, address or title is
// sent to everyone still signed up; a larger capacity is offered to the queue.
func (s *Service) UpdateEvent(ctx context.Context, actor domain.User, id string, in EventInput) (UpdateResult, error) {
	var res UpdateResult
	err := s.run(ctx, func(tx *store.Tx) error {
		e, err := tx.Event(ctx, id)
		if err != nil {
			return notFound(err, "event", id)
		}
		if err := requireAuthor(actor, e); err != nil {
			return err
		}
		if e.Status == domain.EventCancelled {
			return domain.Errorf(http.StatusConflict, "event_cancelled", "event %s is cancelled", e.ID)
		}
		now := s.clock.Now()
		before := e
		if errs := in.apply(&e, now, false); len(errs) > 0 {
			return domain.Invalid(errs)
		}
		regs, err := tx.RegistrationsByEvent(ctx, e.ID)
		if err != nil {
			return err
		}
		if taken := counts(before, regs).Registered; e.Capacity != nil && *e.Capacity < taken {
			return &domain.Error{Status: http.StatusConflict, Code: "capacity_below_registered",
				Message: fmt.Sprintf("capacity %d is below %d taken seats", *e.Capacity, taken),
				Details: []domain.FieldError{{Field: "capacity", Message: fmt.Sprintf("не меньше %d", taken)}}}
		}

		loc := e.Location()
		var changes []Change
		if !e.StartsAt.Equal(before.StartsAt) {
			changes = append(changes, Change{Field: "time",
				Old: domain.HumanTime(before.StartsAt, e.StartsAt, loc), New: domain.HumanTime(e.StartsAt, before.StartsAt, loc)})
		}
		if e.Address != before.Address {
			changes = append(changes, Change{Field: "address", Old: before.Address, New: e.Address})
		}
		if e.Title != before.Title {
			changes = append(changes, Change{Field: "title", Old: before.Title, New: e.Title})
		}
		message := ""
		if in.OrganizerMessage != nil {
			message = strings.TrimSpace(*in.OrganizerMessage)
		}
		if len(changes) > 0 || message != "" {
			e.Version++
		}
		e.UpdatedAt = now
		if err := tx.SaveEvent(ctx, e); err != nil {
			return err
		}
		if len(changes) > 0 || message != "" {
			data := &EnvelopeData{Changes: changes, OrganizerMessage: message}
			key := fmt.Sprintf("%s:%s:v%d", TypeEventUpdated, e.ID, e.Version)
			res.NotifiedCount, err = s.notifyRegistrations(ctx, tx, e, TypeEventUpdated, data, key,
				func(r domain.Registration) bool { return domain.IsActive(r.Status) }, now)
			if err != nil {
				return err
			}
		}
		if err := s.afterSeatChange(ctx, tx, e, true, now); err != nil {
			return err
		}
		res.Event, err = tx.Event(ctx, e.ID)
		return err
	})
	return res, err
}

// CancelEvent cancels an event and tells everyone still signed up.
func (s *Service) CancelEvent(ctx context.Context, actor domain.User, id, message string) (string, int, error) {
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > 2000 {
		return "", 0, domain.Invalid([]domain.FieldError{{Field: "organizer_message", Message: "до 2000 символов"}})
	}
	status, notified := "accepted", 0
	err := s.run(ctx, func(tx *store.Tx) error {
		e, err := tx.Event(ctx, id)
		if err != nil {
			return notFound(err, "event", id)
		}
		if err := requireAuthor(actor, e); err != nil {
			return err
		}
		if e.Status == domain.EventCancelled {
			status = "noop"
			return nil
		}
		now := s.clock.Now()
		e.Status, e.UpdatedAt = domain.EventCancelled, now
		if err := tx.SaveEvent(ctx, e); err != nil {
			return err
		}
		var data *EnvelopeData
		if message != "" {
			data = &EnvelopeData{OrganizerMessage: message}
		}
		notified, err = s.notifyRegistrations(ctx, tx, e, TypeEventCancelled, data, TypeEventCancelled+":"+e.ID,
			func(r domain.Registration) bool { return domain.IsActive(r.Status) }, now)
		return err
	})
	return status, notified, err
}

// MessageParticipants sends the organiser's text to everyone holding a seat.
func (s *Service) MessageParticipants(ctx context.Context, actor domain.User, id, message string) (int, error) {
	message = strings.TrimSpace(message)
	if message == "" || utf8.RuneCountInString(message) > 2000 {
		return 0, domain.Invalid([]domain.FieldError{{Field: "organizer_message", Message: "от 1 до 2000 символов"}})
	}
	notified := 0
	err := s.run(ctx, func(tx *store.Tx) error {
		e, err := tx.Event(ctx, id)
		if err != nil {
			return notFound(err, "event", id)
		}
		if err := requireAuthor(actor, e); err != nil {
			return err
		}
		if e.Status == domain.EventCancelled {
			return domain.Errorf(http.StatusConflict, "event_cancelled", "event %s is cancelled", e.ID)
		}
		now := s.clock.Now()
		key := fmt.Sprintf("event_message:%s:%s", e.ID, NewToken()[:12])
		notified, err = s.notifyRegistrations(ctx, tx, e, TypeEventUpdated, &EnvelopeData{OrganizerMessage: message}, key,
			func(r domain.Registration) bool { return domain.HoldsSeat(r.Status) }, now)
		return err
	})
	return notified, err
}
