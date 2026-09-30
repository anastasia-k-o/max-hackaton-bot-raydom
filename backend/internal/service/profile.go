package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go/v2"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// AuthConfig is what signing in through MAX needs.
type AuthConfig struct {
	// BotToken is the bot's MAX token: initData is signed with a key derived
	// from it. Empty disables POST /auth/max (dev login still works).
	BotToken string
	// MaxAge rejects initData older than this. The SDK checks only the
	// signature, so without this a leaked initData would work forever.
	MaxAge time.Duration
	// DevLogin accepts init_data "dev" without a signature. Dev mode only.
	DevLogin bool
	// DevMaxUserID is who "dev" signs in as: a real MAX account, so the bot
	// writes to that person in MAX while they use the mini app in a browser.
	// Zero: the seeded demo user.
	DevMaxUserID int64
}

// DemoUserID is the seeded user «Пётр Смирнов» from the mocks, the one the
// dev login signs in as.
const DemoUserID = "user_me"

var errAuthUnavailable = domain.Errorf(http.StatusServiceUnavailable, "upstream_unavailable",
	"sign-in through MAX is not configured: set MAX_BOT_TOKEN for the backend")

// LoginWithInitData checks the mini app's initData and returns the user with
// a fresh session. A first visit creates the user from the data MAX signed.
func (s *Service) LoginWithInitData(ctx context.Context, cfg AuthConfig, initData string) (domain.User, string, error) {
	initData = strings.TrimSpace(initData)
	if cfg.DevLogin && initData == "dev" {
		if cfg.DevMaxUserID > 0 {
			return s.devSessionFor(ctx, cfg.DevMaxUserID)
		}
		return s.devSession(ctx)
	}
	if initData == "" {
		return domain.User{}, "", domain.Invalid([]domain.FieldError{{Field: "init_data", Message: "обязательно: WebApp.initData из MAX"}})
	}
	if cfg.BotToken == "" {
		return domain.User{}, "", errAuthUnavailable
	}
	data, err := maxbot.ValidateInitData(initData, cfg.BotToken)
	if err != nil {
		return domain.User{}, "", domain.Errorf(http.StatusUnauthorized, "unauthorized", "initData rejected: %v", err)
	}
	if data.User.ID <= 0 {
		return domain.User{}, "", domain.Errorf(http.StatusUnauthorized, "unauthorized", "initData carries no user")
	}
	if cfg.MaxAge > 0 {
		signed := authTime(data.AuthDate)
		if signed.IsZero() || time.Since(signed) > cfg.MaxAge {
			return domain.User{}, "", domain.Errorf(http.StatusUnauthorized, "unauthorized",
				"initData is too old (signed %s); reopen the mini app", signed.Format(time.RFC3339))
		}
	}

	var u domain.User
	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.UserByMaxID(ctx, data.User.ID)
		switch {
		case err == nil:
			u = existing
		case errors.Is(err, store.ErrNotFound):
			// The bot may have met this person first (bot_started) and made
			// a stub user; otherwise this is a brand-new user.
			u = domain.User{
				ID: newID("user"), MaxUserID: &data.User.ID, CityID: domain.DefaultCity, BotAvailable: true,
				NotifyReminders: true, NotifyRecommendation: true, CreatedAt: time.Now(),
			}
		default:
			return err
		}
		// MAX is the source of truth for the name and the photo.
		if data.User.FirstName != "" {
			u.FirstName = data.User.FirstName
		}
		u.LastName = data.User.LastName
		if data.User.PhotoURL != "" {
			photo := data.User.PhotoURL
			u.PhotoURL = &photo
		}
		if u.FirstName == "" {
			u.FirstName = "Пользователь MAX"
		}
		return tx.SaveUser(ctx, u)
	})
	if err != nil {
		return u, "", err
	}
	token, err := s.NewSession(ctx, u.ID)
	return u, token, err
}

// authTime reads auth_date, which may come in seconds or milliseconds.
func authTime(v int64) time.Time {
	switch {
	case v <= 0:
		return time.Time{}
	case v > 1e12:
		return time.UnixMilli(v)
	default:
		return time.Unix(v, 0)
	}
}

func (s *Service) devSession(ctx context.Context) (domain.User, string, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.User(ctx, DemoUserID)
		if errors.Is(err, store.ErrNotFound) {
			id := int64(100000001)
			u = domain.User{
				ID: DemoUserID, MaxUserID: &id, FirstName: "Пётр", LastName: "Смирнов", IsVerified: true, IsAuthor: true,
				CityID: domain.DefaultCity, BotAvailable: true, NotifyReminders: true, NotifyRecommendation: true,
				Demo: true, CreatedAt: time.Now(),
			}
			return tx.SaveUser(ctx, u)
		}
		return err
	})
	if err != nil {
		return u, "", err
	}
	token, err := s.NewSession(ctx, u.ID)
	return u, token, err
}

// devSessionFor signs in as a real MAX account without initData. It is how
// the mini app is tested in a browser when it cannot be attached to the bot
// in MAX: the person is the same one the bot writes to, so messages, buttons
// and the mini app all refer to one account.
func (s *Service) devSessionFor(ctx context.Context, maxUserID int64) (domain.User, string, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.UserByMaxID(ctx, maxUserID)
		switch {
		case err == nil:
			u = existing
		case errors.Is(err, store.ErrNotFound):
			u = domain.User{
				ID: newID("user"), MaxUserID: &maxUserID, FirstName: "Тестировщик", CityID: domain.DefaultCity,
				BotAvailable: true, NotifyReminders: true, NotifyRecommendation: true, CreatedAt: time.Now(),
			}
		default:
			return err
		}
		// Verified and an author, so every screen can be tried.
		u.IsVerified, u.IsAuthor = true, true
		return tx.SaveUser(ctx, u)
	})
	if err != nil {
		return u, "", err
	}
	token, err := s.NewSession(ctx, u.ID)
	return u, token, err
}

// ProfilePatch is PATCH /me. Only what the user may change about themselves.
type ProfilePatch struct {
	IsAuthor             *bool   `json:"is_author"`
	CityID               *string `json:"city_id"`
	District             *string `json:"district"`
	OnboardingCompleted  *bool   `json:"onboarding_completed"`
	NotificationSettings *struct {
		Reminders       *bool `json:"reminders"`
		Recommendations *bool `json:"recommendations"`
	} `json:"notification_settings"`
}

// Me reloads the user.
func (s *Service) Me(ctx context.Context, userID string) (domain.User, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.User(ctx, userID)
		return notFound(err, "user", userID)
	})
	return u, err
}

// updateUser loads, changes and saves a user in one transaction.
func (s *Service) updateUser(ctx context.Context, userID string, change func(*domain.User) error) (domain.User, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.User(ctx, userID)
		if err != nil {
			return notFound(err, "user", userID)
		}
		if err := change(&u); err != nil {
			return err
		}
		return tx.SaveUser(ctx, u)
	})
	return u, err
}

// UpdateMe applies PATCH /me.
func (s *Service) UpdateMe(ctx context.Context, userID string, p ProfilePatch) (domain.User, error) {
	return s.updateUser(ctx, userID, func(u *domain.User) error {
		if p.IsAuthor != nil {
			if *p.IsAuthor && !u.IsVerified {
				return domain.Errorf(http.StatusForbidden, "verification_required", "author role requires verification")
			}
			u.IsAuthor = *p.IsAuthor
		}
		if p.CityID != nil {
			if _, ok := domain.CityByID(*p.CityID); !ok {
				return domain.Invalid([]domain.FieldError{{Field: "city_id", Message: "неизвестный город"}})
			}
			u.CityID = *p.CityID
		}
		if p.District != nil {
			city, _ := domain.CityByID(u.CityID)
			d := strings.TrimSpace(*p.District)
			if d != "" && !slices.Contains(city.Districts, d) {
				return domain.Invalid([]domain.FieldError{{Field: "district", Message: "выберите район из списка"}})
			}
			if d == "" {
				u.District = nil
			} else {
				u.District = &d
			}
		}
		if p.OnboardingCompleted != nil {
			u.OnboardingCompleted = *p.OnboardingCompleted
		}
		if ns := p.NotificationSettings; ns != nil {
			if ns.Reminders != nil {
				u.NotifyReminders = *ns.Reminders
			}
			if ns.Recommendations != nil {
				u.NotifyRecommendation = *ns.Recommendations
			}
		}
		return nil
	})
}

// Verify marks the user verified after WebApp.requestContact().
//
// MAX signs the contact with auth_date and hash, but how that hash is built
// is not documented anywhere we could check, so it is not verified here. A
// confirmed phone number is required; the trust in it is the same as in the
// session that sent it. Revisit once MAX documents the contact signature.
func (s *Service) Verify(ctx context.Context, userID, phone string, devMode bool) (domain.User, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" || (phone == "demo" && !devMode) {
		return domain.User{}, domain.Invalid([]domain.FieldError{{Field: "phone", Message: "нужен подтверждённый номер из MAX"}})
	}
	return s.updateUser(ctx, userID, func(u *domain.User) error {
		u.IsVerified = true
		return nil
	})
}

// AcceptConsent records the consent to personal data processing.
func (s *Service) AcceptConsent(ctx context.Context, userID string) (domain.User, error) {
	now := s.clock.Now()
	return s.updateUser(ctx, userID, func(u *domain.User) error {
		if u.ConsentAcceptedAt == nil {
			u.ConsentAcceptedAt = &now
		}
		return nil
	})
}

// SetInterests replaces the interests. Unknown tags are dropped; an empty
// list clears them; otherwise at least three are needed, as in onboarding.
func (s *Service) SetInterests(ctx context.Context, userID string, tagIDs []string) ([]string, error) {
	known := []string{}
	for _, id := range tagIDs {
		if _, ok := domain.TagByID(id); ok && !slices.Contains(known, id) {
			known = append(known, id)
		}
	}
	if len(known) > 0 && len(known) < 3 {
		return nil, domain.Invalid([]domain.FieldError{{Field: "tag_ids", Message: "минимум 3 тега"}})
	}
	u, err := s.updateUser(ctx, userID, func(u *domain.User) error {
		u.Interests = known
		u.OnboardingCompleted = true
		return nil
	})
	return u.Interests, err
}

// Complain records a complaint about an event.
func (s *Service) Complain(ctx context.Context, actor domain.User, eventID, text string) error {
	if err := requireVerified(actor); err != nil {
		return err
	}
	if len([]rune(text)) > 2000 {
		return domain.Invalid([]domain.FieldError{{Field: "text", Message: "до 2000 символов"}})
	}
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Event(ctx, eventID); err != nil {
			return notFound(err, "event", eventID)
		}
		s.log.Warn("complaint", "event_id", eventID, "user_id", actor.ID)
		return tx.AddComplaint(ctx, eventID, actor.ID, strings.TrimSpace(text), s.clock.Now())
	})
}

// AddView counts a card view. Unknown ids are ignored: a view is not worth
// an error on the user's screen.
func (s *Service) AddView(ctx context.Context, eventID string) error {
	return s.store.InTx(ctx, func(tx *store.Tx) error { return tx.AddView(ctx, eventID) })
}
