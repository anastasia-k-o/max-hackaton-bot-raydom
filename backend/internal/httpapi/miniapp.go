package httpapi

import (
	"net/http"
	"strings"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/service"
)

// The mini app's own endpoints: sign-in, profile, catalog, «Мои записи»,
// reports. Shapes follow frontend/src/types/index.js.

type notificationSettingsDTO struct {
	Reminders       bool `json:"reminders"`
	Recommendations bool `json:"recommendations"`
}

type userDTO struct {
	ID                   string                  `json:"id"`
	MaxUserID            *int64                  `json:"max_user_id"`
	FirstName            string                  `json:"first_name"`
	LastName             string                  `json:"last_name"`
	PhotoURL             *string                 `json:"photo_url"`
	IsVerified           bool                    `json:"is_verified"`
	IsAuthor             bool                    `json:"is_author"`
	CityID               string                  `json:"city_id"`
	District             *string                 `json:"district"`
	BotAvailable         bool                    `json:"bot_available"`
	OnboardingCompleted  bool                    `json:"onboarding_completed"`
	ConsentAcceptedAt    *string                 `json:"consent_accepted_at"`
	NotificationSettings notificationSettingsDTO `json:"notification_settings"`
}

func toUserDTO(u domain.User) userDTO {
	loc := domain.LocationOf("Europe/Moscow")
	if c, ok := domain.CityByID(u.CityID); ok {
		loc = domain.LocationOf(c.Timezone)
	}
	return userDTO{
		ID: u.ID, MaxUserID: u.MaxUserID, FirstName: u.FirstName, LastName: u.LastName, PhotoURL: u.PhotoURL,
		IsVerified: u.IsVerified, IsAuthor: u.IsAuthor, CityID: u.CityID, District: u.District,
		BotAvailable: u.BotAvailable, OnboardingCompleted: u.OnboardingCompleted,
		ConsentAcceptedAt:    timeOrNil(u.ConsentAcceptedAt, loc),
		NotificationSettings: notificationSettingsDTO{Reminders: u.NotifyReminders, Recommendations: u.NotifyRecommendation},
	}
}

// optionalUser is the session user if there is a valid session, else nil.
// Public pages (catalog, recommendations) personalise when they can.
func (a *api) optionalUser(r *http.Request) *domain.User {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return nil
	}
	u, err := a.sessionUser(r)
	if err != nil {
		return nil
	}
	return &u
}

// withUser runs fn for the session user or answers 401.
func (a *api) withUser(fn func(http.ResponseWriter, *http.Request, domain.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := a.sessionUser(r)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		fn(w, r, u)
	}
}

func (a *api) authMax(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InitData string `json:"init_data"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	u, token, err := a.Service.LoginWithInitData(r.Context(), a.Auth, body.InitData)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": toUserDTO(u)})
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		writeJSON(w, http.StatusOK, toUserDTO(u))
	})(w, r)
}

func (a *api) updateMe(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		var p service.ProfilePatch
		if err := decode(r, &p); err != nil {
			a.writeError(w, r, err)
			return
		}
		u, err := a.Service.UpdateMe(r.Context(), u.ID, p)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toUserDTO(u))
	})(w, r)
}

func (a *api) verify(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		var body struct {
			Phone string `json:"phone"`
		}
		if err := decode(r, &body); err != nil {
			a.writeError(w, r, err)
			return
		}
		u, err := a.Service.Verify(r.Context(), u.ID, body.Phone, a.DevMode)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toUserDTO(u))
	})(w, r)
}

func (a *api) consent(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		u, err := a.Service.AcceptConsent(r.Context(), u.ID)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toUserDTO(u))
	})(w, r)
}

func (a *api) getInterests(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		ids := u.Interests
		if ids == nil {
			ids = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"tag_ids": ids})
	})(w, r)
}

func (a *api) setInterests(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		var body struct {
			TagIDs []string `json:"tag_ids"`
		}
		if err := decode(r, &body); err != nil {
			a.writeError(w, r, err)
			return
		}
		ids, err := a.Service.SetInterests(r.Context(), u.ID, body.TagIDs)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tag_ids": ids})
	})(w, r)
}

// myRegistrationDTO is a registration with its event, as «Мои записи» shows it.
type myRegistrationDTO struct {
	registrationDTO
	Event myRegEventDTO `json:"event"`
}

type myRegEventDTO struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	StartsAt   string `json:"starts_at"`
	Address    string `json:"address"`
	MiniAppURL string `json:"mini_app_url"`
	CategoryID string `json:"category_id"`
	Status     string `json:"status"`
	Timezone   string `json:"timezone"`
}

func (a *api) myRegistrations(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		list, err := a.Service.MyRegistrations(r.Context(), u)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		items := make([]myRegistrationDTO, 0, len(list))
		for _, x := range list {
			loc := x.Event.Location()
			items = append(items, myRegistrationDTO{
				registrationDTO: toRegistrationDTO(x.Registration, loc),
				Event: myRegEventDTO{ID: x.Event.ID, Title: x.Event.Title, StartsAt: domain.FormatTime(x.Event.StartsAt, loc),
					Address: x.Event.Address, MiniAppURL: a.cardURL(x.Event.ID), CategoryID: x.Event.CategoryID,
					Status: x.Event.Status, Timezone: x.Event.Timezone},
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})(w, r)
}

func (a *api) myEvents(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		views, err := a.Service.MyEvents(r.Context(), u)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		items := make([]eventDetailsDTO, 0, len(views))
		for _, v := range views {
			items = append(items, a.toEventDetails(v))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})(w, r)
}

func (a *api) catalog(w http.ResponseWriter, r *http.Request) {
	q := service.ParseCatalogQuery(r.URL.Query().Get)
	page, err := a.Service.Catalog(r.Context(), q, a.optionalUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *api) report(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		rep, err := a.Service.EventReport(r.Context(), u, r.PathValue("id"))
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	})(w, r)
}

func (a *api) addView(w http.ResponseWriter, r *http.Request) {
	if err := a.Service.AddView(r.Context(), r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) complain(w http.ResponseWriter, r *http.Request) {
	a.withUser(func(w http.ResponseWriter, r *http.Request, u domain.User) {
		var body struct {
			Text string `json:"text"`
		}
		if err := decode(r, &body); err != nil {
			a.writeError(w, r, err)
			return
		}
		if err := a.Service.Complain(r.Context(), u, r.PathValue("id"), body.Text); err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
	})(w, r)
}

func (a *api) recommendations(w http.ResponseWriter, r *http.Request) {
	items, mode, err := a.Service.Recommendations(r.Context(), a.optionalUser(r), r.URL.Query().Get("city_id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "mode": mode})
}

func (a *api) suggestTags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := decode(r, &body); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, service.SuggestTags(body.Title, body.Description))
}
