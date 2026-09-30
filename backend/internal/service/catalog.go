package service

import (
	"context"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// The read side of the mini app: catalog, «Мои записи», «Мои мероприятия»,
// the organiser's report and recommendations. The rules are the mocks'
// (frontend/src/api/mocks/handlers.js), function for function.

// CatalogQuery is GET /events.
type CatalogQuery struct {
	CityID     string
	Q          string
	CategoryID string
	TagIDs     []string
	Date       string // today | tomorrow | weekend | week
	Daypart    string // morning | day | evening
	District   string
	HasSeats   bool
	Sort       string // starts_at | published_at | popularity
	Page       int
	PageSize   int
}

// CatalogItem is one card of the catalog: no time, address or seats — the
// spec keeps those for verified users on the full card.
type CatalogItem struct {
	ID                   string       `json:"id"`
	Title                string       `json:"title"`
	CategoryID           string       `json:"category_id"`
	Tags                 []domain.Tag `json:"tags"`
	ShortDescription     string       `json:"short_description"`
	CoverURL             *string      `json:"cover_url"`
	AuthorID             string       `json:"author_id"`
	MyRegistrationStatus *string      `json:"my_registration_status"`
}

// CatalogPage is a page of the catalog.
type CatalogPage struct {
	Items    []CatalogItem `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int           `json:"total"`
}

// snapshot is everything the read side needs, loaded once per request.
type snapshot struct {
	events []domain.Event
	regs   map[string][]domain.Registration // by event
	now    time.Time
}

func (s *Service) snapshot(ctx context.Context, tx *store.Tx) (snapshot, error) {
	snap := snapshot{regs: map[string][]domain.Registration{}, now: s.clock.Now()}
	var err error
	if snap.events, err = tx.Events(ctx, ""); err != nil {
		return snap, err
	}
	all, err := tx.RegistrationsByStatus(ctx, append(slices.Clone(domain.ActiveStatuses), domain.RegCancelled)...)
	if err != nil {
		return snap, err
	}
	for _, r := range all {
		snap.regs[r.EventID] = append(snap.regs[r.EventID], r)
	}
	return snap, nil
}

func (snap snapshot) counts(e domain.Event) Counts { return counts(e, snap.regs[e.ID]) }

func (snap snapshot) mine(e domain.Event, viewer *domain.User) *domain.Registration {
	if viewer == nil {
		return nil
	}
	for _, r := range snap.regs[e.ID] {
		if r.UserID == viewer.ID && domain.IsActive(r.Status) {
			r := r
			return &r
		}
	}
	return nil
}

func (snap snapshot) item(e domain.Event, viewer *domain.User) CatalogItem {
	it := CatalogItem{
		ID: e.ID, Title: e.Title, CategoryID: e.CategoryID, Tags: domain.TagsOf(e.TagIDs),
		ShortDescription: e.ShortDescription, CoverURL: e.CoverURL, AuthorID: e.AuthorID,
	}
	if m := snap.mine(e, viewer); m != nil {
		st := m.Status
		it.MyRegistrationStatus = &st
	}
	return it
}

// visible is what the catalog and recommendations may show.
func (snap snapshot) visible(e domain.Event) bool {
	return e.Status == domain.EventPublished && e.StartsAt.After(snap.now)
}

// Catalog lists published upcoming events with the mini app's filters.
func (s *Service) Catalog(ctx context.Context, q CatalogQuery, viewer *domain.User) (CatalogPage, error) {
	var page CatalogPage
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		snap, err := s.snapshot(ctx, tx)
		if err != nil {
			return err
		}
		needle := strings.ToLower(strings.TrimSpace(q.Q))
		var list []domain.Event
		for _, e := range snap.events {
			switch {
			case !snap.visible(e):
			case q.CityID != "" && e.CityID != q.CityID:
			case needle != "" && !strings.Contains(strings.ToLower(e.Title+" "+e.ShortDescription+" "+e.Description), needle):
			case q.CategoryID != "" && e.CategoryID != q.CategoryID:
			case len(q.TagIDs) > 0 && !slices.ContainsFunc(q.TagIDs, func(t string) bool { return slices.Contains(e.TagIDs, t) }):
			case !matchDate(e, q.Date, snap.now) || !matchDaypart(e, q.Daypart):
			case q.District != "" && e.District != q.District:
			case q.HasSeats && e.Capacity != nil && snap.counts(e).Registered >= *e.Capacity:
			default:
				list = append(list, e)
			}
		}
		sort.SliceStable(list, func(i, j int) bool {
			switch q.Sort {
			case "published_at":
				return list[i].PublishedAt.After(list[j].PublishedAt)
			case "popularity":
				return list[i].Popularity > list[j].Popularity
			default:
				return list[i].StartsAt.Before(list[j].StartsAt)
			}
		})
		page = CatalogPage{Page: max(1, q.Page), PageSize: q.PageSize, Total: len(list), Items: []CatalogItem{}}
		if page.PageSize <= 0 {
			page.PageSize = 12
		}
		page.PageSize = min(page.PageSize, 50)
		from := (page.Page - 1) * page.PageSize
		for i := from; i < len(list) && i < from+page.PageSize; i++ {
			page.Items = append(page.Items, snap.item(list[i], viewer))
		}
		return nil
	})
	return page, err
}

// cityDay is the calendar day of t in the event's city.
func cityDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func matchDate(e domain.Event, when string, now time.Time) bool {
	if when == "" {
		return true
	}
	loc := e.Location()
	diff := int(math.Round(cityDay(e.StartsAt, loc).Sub(cityDay(now, loc)).Hours() / 24))
	switch when {
	case "today":
		return diff == 0
	case "tomorrow":
		return diff == 1
	case "week":
		return diff >= 0 && diff < 7
	case "weekend":
		wd := e.StartsAt.In(loc).Weekday()
		return diff >= 0 && diff < 7 && (wd == time.Saturday || wd == time.Sunday)
	}
	return true
}

func matchDaypart(e domain.Event, part string) bool {
	h := e.StartsAt.In(e.Location()).Hour()
	switch part {
	case "morning":
		return h < 12
	case "day":
		return h >= 12 && h < 17
	case "evening":
		return h >= 17
	}
	return true
}

// MyRegistration is a registration with its event, for «Мои записи».
type MyRegistration struct {
	Registration domain.Registration
	Event        domain.Event
}

// MyRegistrations lists all of a user's registrations, cancelled included.
func (s *Service) MyRegistrations(ctx context.Context, viewer domain.User) ([]MyRegistration, error) {
	if err := requireVerified(viewer); err != nil {
		return nil, err
	}
	var out []MyRegistration
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		regs, err := tx.RegistrationsByUser(ctx, viewer.ID)
		if err != nil {
			return err
		}
		for _, r := range regs {
			e, err := tx.Event(ctx, r.EventID)
			if err != nil {
				return err
			}
			out = append(out, MyRegistration{Registration: r, Event: e})
		}
		return nil
	})
	return out, err
}

// MyEvents lists the organiser's events as full cards.
func (s *Service) MyEvents(ctx context.Context, viewer domain.User) ([]EventView, error) {
	if err := requireVerified(viewer); err != nil {
		return nil, err
	}
	var out []EventView
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		events, err := tx.EventsByAuthor(ctx, viewer.ID)
		if err != nil {
			return err
		}
		for _, e := range events {
			v, err := s.view(ctx, tx, &viewer, e.ID)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// Participant is one line of the organiser's report.
type Participant struct {
	RegistrationID string  `json:"registration_id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	CancelReason   *string `json:"cancel_reason"`
	CancelledLate  bool    `json:"cancelled_late"`
	BotAvailable   bool    `json:"bot_available"`
}

// Report is the organiser's report (ТЗ, раздел 8).
type Report struct {
	EventID            string         `json:"event_id"`
	Views              int            `json:"views"`
	RegistrationsTotal int            `json:"registrations_total"`
	RegisteredNow      int            `json:"registered_now"`
	Capacity           *int           `json:"capacity"`
	Confirmed          int            `json:"confirmed"`
	NoAnswer           int            `json:"no_answer"`
	CancelledTotal     int            `json:"cancelled_total"`
	CancelledLate      int            `json:"cancelled_late"`
	CancelReasons      map[string]int `json:"cancel_reasons"`
	WaitlistSize       int            `json:"waitlist_size"`
	FromWaitlist       int            `json:"from_waitlist"`
	Participants       []Participant  `json:"participants"`
}

// EventReport builds the report. Only the author sees it.
func (s *Service) EventReport(ctx context.Context, viewer domain.User, eventID string) (Report, error) {
	var rep Report
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		e, err := tx.Event(ctx, eventID)
		if err != nil {
			return notFound(err, "event", eventID)
		}
		if err := requireAuthor(viewer, e); err != nil {
			return err
		}
		regs, err := tx.RegistrationsByEvent(ctx, e.ID)
		if err != nil {
			return err
		}
		users, err := tx.Users(ctx)
		if err != nil {
			return err
		}
		c := counts(e, regs)
		rep = Report{EventID: e.ID, Views: e.Views, RegisteredNow: c.Registered, Capacity: e.Capacity,
			WaitlistSize: c.Waitlist, RegistrationsTotal: e.ExtraRegistered, CancelReasons: map[string]int{},
			Participants: []Participant{}}
		for _, r := range regs {
			if r.Status != domain.RegWaitlist {
				rep.RegistrationsTotal++
			}
			switch r.Status {
			case domain.RegConfirmed:
				rep.Confirmed++
			case domain.RegRegistered:
				rep.NoAnswer++
			case domain.RegCancelled:
				rep.CancelledTotal++
				if r.CancelledLate {
					rep.CancelledLate++
				}
				reason := "none"
				if r.CancelReason != nil {
					reason = *r.CancelReason
				}
				rep.CancelReasons[reason]++
			}
			if r.FromWaitlist {
				rep.FromWaitlist++
			}
			u := users[r.UserID]
			name := u.DisplayName()
			if name == "" {
				name = "Участник"
			}
			rep.Participants = append(rep.Participants, Participant{RegistrationID: r.ID, Name: name, Status: r.Status,
				CancelReason: r.CancelReason, CancelledLate: r.CancelledLate, BotAvailable: u.BotAvailable})
		}
		return nil
	})
	return rep, err
}

// Recommendation is one «Для вас» card with the reason it is there.
type Recommendation struct {
	Event  CatalogItem `json:"event"`
	Reason string      `json:"reason"`
}

// Recommendations are «Для вас»: by interests when the user has them, by
// popularity otherwise. Weights follow the spec (раздел 4): matching tags,
// closeness in time, a fresh announcement, the user's district, minus full.
func (s *Service) Recommendations(ctx context.Context, viewer *domain.User, cityID string) ([]Recommendation, string, error) {
	var (
		out  = []Recommendation{}
		mode = "popular"
	)
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		snap, err := s.snapshot(ctx, tx)
		if err != nil {
			return err
		}
		var interests []string
		district := ""
		if viewer != nil {
			interests = viewer.Interests
			if viewer.District != nil {
				district = *viewer.District
			}
		}
		personal := len(interests) > 0
		if personal {
			mode = "personal"
		}
		type scored struct {
			rec     Recommendation
			score   float64
			matched int
		}
		var list []scored
		for _, e := range snap.events {
			if !snap.visible(e) || (cityID != "" && e.CityID != cityID) {
				continue
			}
			if viewer != nil && (e.AuthorID == viewer.ID || snap.mine(e, viewer) != nil) {
				continue
			}
			var matched []string
			for _, t := range e.TagIDs {
				if slices.Contains(interests, t) {
					matched = append(matched, t)
				}
			}
			score := float64(e.Popularity) / 10
			if personal {
				score = float64(len(matched) * 3)
			}
			fresh := snap.now.Sub(e.PublishedAt) < 24*time.Hour
			if e.StartsAt.Sub(snap.now) < 7*24*time.Hour {
				score += 1.5
			}
			if fresh {
				score += 1
			}
			if district != "" && e.District == district {
				score++
			}
			if e.Capacity != nil && snap.counts(e).Registered >= *e.Capacity {
				score -= 2
			}
			if personal && len(matched) == 0 && score <= 3 {
				continue
			}
			reason := "Популярно сейчас"
			switch {
			case len(matched) > 0:
				names := []string{}
				for _, t := range matched[:min(2, len(matched))] {
					tag, _ := domain.TagByID(t)
					names = append(names, "«"+tag.Name+"»")
				}
				reason = "Вам интересно: " + strings.Join(names, ", ")
			case fresh:
				reason = "Новая афиша"
			}
			list = append(list, scored{rec: Recommendation{Event: snap.item(e, viewer), Reason: reason}, score: score, matched: len(matched)})
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
		for _, x := range list {
			out = append(out, x.rec)
		}
		return nil
	})
	return out, mode, err
}

// Suggestion is the answer of POST /ml/suggest-tags.
type Suggestion struct {
	CategoryID *string  `json:"category_id"`
	TagIDs     []string `json:"tag_ids"`
	Confidence float64  `json:"confidence"`
}

// SuggestTags guesses tags from the title and description by keyword stems,
// the mocks' stand-in for the ML service.
func SuggestTags(title, description string) Suggestion {
	text := " " + strings.ToLower(title+" "+description) + " "
	out := Suggestion{TagIDs: []string{}}
	if len([]rune(strings.TrimSpace(text))) < 4 {
		return out
	}
	score := map[string]int{}
	var order []string
	for _, c := range domain.Categories {
		for _, t := range c.Tags {
			for _, k := range domain.TagKeywords[t.ID] {
				if strings.Contains(text, k) {
					out.TagIDs = append(out.TagIDs, t.ID)
					if score[c.ID] == 0 {
						order = append(order, c.ID)
					}
					score[c.ID]++
					break
				}
			}
		}
	}
	best, bestScore := "", 0
	for _, c := range order {
		if score[c] > bestScore {
			best, bestScore = c, score[c]
		}
	}
	if best != "" {
		out.CategoryID = &best
		out.Confidence = math.Min(0.95, 0.5+float64(bestScore)*0.15)
	}
	if len(out.TagIDs) > 4 {
		out.TagIDs = out.TagIDs[:4]
	}
	return out
}

// ParseCatalogQuery reads GET /events parameters.
func ParseCatalogQuery(get func(string) string) CatalogQuery {
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	q := CatalogQuery{
		CityID: get("city_id"), Q: get("q"), CategoryID: get("category_id"), Date: get("date"),
		Daypart: get("daypart"), District: get("district"), Sort: get("sort"),
		Page: atoi(get("page")), PageSize: atoi(get("page_size")),
	}
	if t := strings.TrimSpace(get("tag_ids")); t != "" {
		q.TagIDs = strings.Split(t, ",")
	}
	q.HasSeats = get("has_seats") == "true" || get("has_seats") == "1"
	return q
}
