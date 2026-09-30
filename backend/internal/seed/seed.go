// Package seed fills an empty database with the mini app's demo data.
//
// demo.json is generated from frontend/src/api/mocks (events, users,
// registrations), so the catalog looks the same with and without mocks.
// Times in it are relative: an event is "in 2 days at 09:00 Moscow time",
// a registration was "created 24 hours ago". They are resolved against the
// moment of seeding, so the demo never goes stale.
package seed

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

//go:embed demo.json
var demoJSON []byte

type demo struct {
	Me            demoUser    `json:"me"`
	OtherUsers    []otherUser `json:"other_users"`
	Events        []demoEvent `json:"events"`
	Registrations []demoReg   `json:"registrations"`
}

type demoUser struct {
	ID        string `json:"id"`
	MaxUserID int64  `json:"max_user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	District  string `json:"district"`
}

type otherUser struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BotAvailable bool   `json:"bot_available"`
}

type startSpec struct {
	Days  *int   `json:"days"`
	Clock string `json:"clock"`
	InMin *int   `json:"in_min"`
}

type demoEvent struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	CategoryID       string    `json:"category_id"`
	TagIDs           []string  `json:"tag_ids"`
	ShortDescription string    `json:"short_description"`
	Description      string    `json:"description"`
	Bring            *string   `json:"bring"`
	Level            *string   `json:"level"`
	AgeLimit         *string   `json:"age_limit"`
	Starts           startSpec `json:"starts"`
	DurationMin      int       `json:"duration_min"`
	District         string    `json:"district"`
	Address          string    `json:"address"`
	HowToFind        string    `json:"how_to_find"`
	Capacity         *int      `json:"capacity"`
	ExtraRegistered  int       `json:"extra_registered"`
	ExtraWaitlist    int       `json:"extra_waitlist"`
	Views            int       `json:"views"`
	Popularity       int       `json:"popularity"`
	PublishedMin     int       `json:"published_min"`
	AuthorID         string    `json:"author_id"`
	AuthorName       string    `json:"author_name"`
	Contact          string    `json:"contact"`
	Status           string    `json:"status"`
	Timezone         string    `json:"timezone"`
}

type demoReg struct {
	ID            string  `json:"id"`
	EventID       string  `json:"event_id"`
	UserID        string  `json:"user_id"`
	Status        string  `json:"status"`
	QueuePosition *int    `json:"queue_position"`
	CancelReason  *string `json:"cancel_reason"`
	CancelledLate bool    `json:"cancelled_late"`
	FromWaitlist  bool    `json:"from_waitlist"`
	CreatedMin    *int    `json:"created_at_min"`
	ConfirmedMin  *int    `json:"confirmed_at_min"`
	CancelledMin  *int    `json:"cancelled_at_min"`
	OfferMin      *int    `json:"offer_expires_at_min"`
}

// IfEmpty seeds the database unless it already has events. It reports
// whether it seeded.
func IfEmpty(ctx context.Context, st *store.Store, now time.Time) (bool, error) {
	seeded := false
	err := st.InTx(ctx, func(tx *store.Tx) error {
		events, err := tx.Events(ctx, "")
		if err != nil || len(events) > 0 {
			return err
		}
		seeded = true
		return load(ctx, tx, now)
	})
	return seeded, err
}

func load(ctx context.Context, tx *store.Tx, now time.Time) error {
	var d demo
	if err := json.Unmarshal(demoJSON, &d); err != nil {
		return fmt.Errorf("seed: parse demo.json: %w", err)
	}
	msk := domain.LocationOf("Europe/Moscow")
	at := func(min *int) *time.Time {
		if min == nil {
			return nil
		}
		t := now.Add(time.Duration(*min) * time.Minute).Truncate(time.Minute)
		return &t
	}

	// The demo user: the one the dev login signs in as. Its MAX id is made
	// up, so it is flagged demo and the bot never writes to it.
	meID := d.Me.MaxUserID
	district := d.Me.District
	users := []domain.User{{
		ID: d.Me.ID, MaxUserID: &meID, FirstName: d.Me.FirstName, LastName: d.Me.LastName,
		IsVerified: true, IsAuthor: true, CityID: domain.DefaultCity, District: &district,
		BotAvailable: true, NotifyReminders: true, NotifyRecommendation: true, Demo: true, CreatedAt: now,
	}}
	for _, o := range d.OtherUsers {
		first, last := splitName(o.Name)
		users = append(users, domain.User{ID: o.ID, FirstName: first, LastName: last, IsVerified: true,
			CityID: domain.DefaultCity, BotAvailable: o.BotAvailable, Demo: true, CreatedAt: now})
	}
	known := map[string]bool{}
	for _, u := range users {
		known[u.ID] = true
	}
	// Organisers exist only as author_id/author_name in the mocks.
	for _, e := range d.Events {
		if !known[e.AuthorID] {
			known[e.AuthorID] = true
			users = append(users, domain.User{ID: e.AuthorID, FirstName: e.AuthorName, IsVerified: true, IsAuthor: true,
				CityID: domain.DefaultCity, BotAvailable: true, Demo: true, CreatedAt: now})
		}
	}
	for _, u := range users {
		if err := tx.SaveUser(ctx, u); err != nil {
			return err
		}
	}

	for _, de := range d.Events {
		starts, err := resolveStart(de.Starts, now, msk)
		if err != nil {
			return fmt.Errorf("seed: event %s: %w", de.ID, err)
		}
		tz := de.Timezone
		if tz == "" {
			tz = "Europe/Moscow"
		}
		published := now.Add(time.Duration(de.PublishedMin) * time.Minute).Truncate(time.Minute)
		e := domain.Event{
			ID: de.ID, Title: de.Title, ShortDescription: de.ShortDescription, Description: de.Description,
			CategoryID: de.CategoryID, TagIDs: de.TagIDs, StartsAt: starts, DurationMin: de.DurationMin,
			Timezone: tz, CityID: domain.DefaultCity, District: de.District, Address: de.Address,
			HowToFind: de.HowToFind, Capacity: de.Capacity, ExtraRegistered: de.ExtraRegistered,
			ExtraWaitlist: de.ExtraWaitlist, Level: de.Level, AgeLimit: de.AgeLimit, Bring: de.Bring,
			AuthorID: de.AuthorID, AuthorName: de.AuthorName, Contact: de.Contact, Status: de.Status,
			ModerationFlags: []string{}, PublishedAt: published, Views: de.Views, Popularity: de.Popularity,
			Version: 1, CreatedAt: published, UpdatedAt: published,
		}
		if err := tx.SaveEvent(ctx, e); err != nil {
			return err
		}
	}

	for _, dr := range d.Registrations {
		created := now
		if c := at(dr.CreatedMin); c != nil {
			created = *c
		}
		r := domain.Registration{
			ID: dr.ID, EventID: dr.EventID, UserID: dr.UserID, Status: dr.Status, QueuePosition: dr.QueuePosition,
			OfferExpiresAt: at(dr.OfferMin), CancelReason: dr.CancelReason, CancelledLate: dr.CancelledLate,
			FromWaitlist: dr.FromWaitlist, CreatedAt: created, ConfirmedAt: at(dr.ConfirmedMin), CancelledAt: at(dr.CancelledMin),
		}
		if err := tx.SaveRegistration(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// resolveStart turns "in 2 days at 09:00" into a moment in Moscow time.
func resolveStart(s startSpec, now time.Time, loc *time.Location) (time.Time, error) {
	if s.InMin != nil {
		return now.Add(time.Duration(*s.InMin) * time.Minute).Truncate(time.Minute), nil
	}
	if s.Days == nil {
		return time.Time{}, fmt.Errorf("no start")
	}
	clock, err := time.Parse("15:04", s.Clock)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad clock %q", s.Clock)
	}
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d+*s.Days, clock.Hour(), clock.Minute(), 0, 0, loc), nil
}

// splitName turns «Мария Л.» into first and last name.
func splitName(name string) (string, string) {
	for i, r := range name {
		if r == ' ' {
			last := []rune(name[i+1:])
			if len(last) > 0 && last[len(last)-1] == '.' {
				last = last[:len(last)-1]
			}
			return name[:i], string(last)
		}
	}
	return name, ""
}
