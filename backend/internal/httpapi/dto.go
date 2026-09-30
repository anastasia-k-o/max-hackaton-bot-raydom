package httpapi

import (
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/service"
)

// DTOs follow frontend/src/types/index.js field for field: the mini app
// renders them as they come.

type registrationDTO struct {
	ID             string  `json:"id"`
	EventID        string  `json:"event_id"`
	Status         string  `json:"status"`
	QueuePosition  *int    `json:"queue_position"`
	OfferExpiresAt *string `json:"offer_expires_at"`
	CancelReason   *string `json:"cancel_reason"`
	CancelledLate  bool    `json:"cancelled_late"`
	CreatedAt      string  `json:"created_at"`
	ConfirmedAt    *string `json:"confirmed_at"`
	CancelledAt    *string `json:"cancelled_at"`
}

func timeOrNil(t *time.Time, loc *time.Location) *string {
	if t == nil {
		return nil
	}
	s := domain.FormatTime(*t, loc)
	return &s
}

func toRegistrationDTO(r domain.Registration, loc *time.Location) registrationDTO {
	return registrationDTO{
		ID: r.ID, EventID: r.EventID, Status: r.Status, QueuePosition: r.QueuePosition,
		OfferExpiresAt: timeOrNil(r.OfferExpiresAt, loc), CancelReason: r.CancelReason,
		CancelledLate: r.CancelledLate, CreatedAt: domain.FormatTime(r.CreatedAt, loc),
		ConfirmedAt: timeOrNil(r.ConfirmedAt, loc), CancelledAt: timeOrNil(r.CancelledAt, loc),
	}
}

type authorDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type eventDetailsDTO struct {
	ID                   string           `json:"id"`
	Title                string           `json:"title"`
	StartsAt             string           `json:"starts_at"`
	Address              string           `json:"address"`
	MiniAppURL           string           `json:"mini_app_url"`
	CategoryID           string           `json:"category_id"`
	Tags                 []domain.Tag     `json:"tags"`
	ShortDescription     string           `json:"short_description"`
	Description          string           `json:"description"`
	DurationMin          int              `json:"duration_min"`
	Timezone             string           `json:"timezone"`
	CityID               string           `json:"city_id"`
	District             string           `json:"district"`
	HowToFind            string           `json:"how_to_find"`
	Lat                  *float64         `json:"lat"`
	Lon                  *float64         `json:"lon"`
	Capacity             *int             `json:"capacity"`
	RegisteredCount      int              `json:"registered_count"`
	WaitlistCount        int              `json:"waitlist_count"`
	RegistrationClosesAt string           `json:"registration_closes_at"`
	Level                *string          `json:"level"`
	AgeLimit             *string          `json:"age_limit"`
	Bring                *string          `json:"bring"`
	Author               authorDTO        `json:"author"`
	Contact              string           `json:"contact"`
	CoverURL             *string          `json:"cover_url"`
	Status               string           `json:"status"`
	ModerationFlags      []string         `json:"moderation_flags"`
	PublishedAt          string           `json:"published_at"`
	MyRegistration       *registrationDTO `json:"my_registration"`
	NotifiedCount        *int             `json:"notified_count,omitempty"`
}

func (a *api) toEventDetails(v service.EventView) eventDetailsDTO {
	e := v.Event
	loc := e.Location()
	out := eventDetailsDTO{
		ID: e.ID, Title: e.Title, StartsAt: domain.FormatTime(e.StartsAt, loc), Address: e.Address,
		MiniAppURL: a.cardURL(e.ID), CategoryID: e.CategoryID, Tags: domain.TagsOf(e.TagIDs),
		ShortDescription: e.ShortDescription, Description: e.Description, DurationMin: e.DurationMin,
		Timezone: e.Timezone, CityID: e.CityID, District: e.District, HowToFind: e.HowToFind,
		Lat: e.Lat, Lon: e.Lon, Capacity: e.Capacity,
		RegisteredCount: v.Counts.Registered, WaitlistCount: v.Counts.Waitlist,
		RegistrationClosesAt: a.Service.ClosesAt(e),
		Level:                e.Level, AgeLimit: e.AgeLimit, Bring: e.Bring,
		Author:  authorDTO{ID: e.AuthorID, Name: e.AuthorName},
		Contact: e.Contact, CoverURL: e.CoverURL, Status: e.Status, ModerationFlags: e.ModerationFlags,
		PublishedAt: domain.FormatTime(e.PublishedAt, loc),
	}
	if v.Mine != nil {
		dto := toRegistrationDTO(*v.Mine, loc)
		out.MyRegistration = &dto
	}
	return out
}

// cardURL is the event's card link for the mini app. Without MINI_APP_URL
// it is the path on the mini app's own host, which is where the mini app
// runs anyway; the bot, which needs an absolute link, gets none instead.
func (a *api) cardURL(eventID string) string {
	if u := a.Service.EventURL(eventID); u != "" {
		return u
	}
	return "/app/" + eventID
}
