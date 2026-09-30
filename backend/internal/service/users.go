package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// Bot status reasons (bot/docs/INTEGRATION_MINIAPP.md §6.2).
var BotStatusReasons = []string{"bot_started", "bot_stopped", "dialog_removed"}

// SessionTTL is how long a mini app session lives.
const SessionTTL = 7 * 24 * time.Hour

// UserByMaxID resolves the person a bot request speaks for.
func (s *Service) UserByMaxID(ctx context.Context, maxUserID int64) (domain.User, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.UserByMaxID(ctx, maxUserID)
		return notFound(err, "user with max_user_id", fmt.Sprint(maxUserID))
	})
	return u, err
}

// UserBySession resolves a session token. Sessions live in real time, not
// on the dev clock: travelling a week ahead must not log everyone out.
func (s *Service) UserBySession(ctx context.Context, token string) (domain.User, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.SessionUser(ctx, token, time.Now())
		return err
	})
	return u, err
}

// NewSession issues a session for a user.
func (s *Service) NewSession(ctx context.Context, userID string) (string, error) {
	token := NewToken()
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.CreateSession(ctx, token, userID, time.Now(), SessionTTL)
	})
	return token, err
}

// ReportBotStatus records whether the bot can write to a MAX user. A user
// seen for the first time is created: they pressed «Начать» in the bot
// before ever opening the mini app, and their status must not be lost.
func (s *Service) ReportBotStatus(ctx context.Context, maxUserID int64, available bool, reason string, occurredAt time.Time) (bool, error) {
	if maxUserID <= 0 {
		return false, domain.Invalid([]domain.FieldError{{Field: "max_user_id", Message: "положительное число"}})
	}
	if !slices.Contains(BotStatusReasons, reason) {
		return false, domain.Invalid([]domain.FieldError{{Field: "reason", Message: "bot_started, bot_stopped или dialog_removed"}})
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	changed := false
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		u, err := tx.UserByMaxID(ctx, maxUserID)
		if errors.Is(err, store.ErrNotFound) {
			u = domain.User{
				ID: newID("user"), MaxUserID: &maxUserID, FirstName: "Пользователь MAX",
				CityID: domain.DefaultCity, BotAvailable: available, BotStatusAt: &occurredAt,
				NotifyReminders: true, NotifyRecommendation: true, CreatedAt: time.Now(),
			}
			changed = true
			return tx.SaveUser(ctx, u)
		}
		if err != nil {
			return err
		}
		changed, err = tx.SetBotAvailable(ctx, u.ID, available, occurredAt)
		return err
	})
	if err == nil {
		s.log.Info("bot status", "max_user_id", maxUserID, "available", available, "reason", reason, "applied", changed)
	}
	return changed, err
}

// DevUser is the input of the dev login: a real MAX id for a person who
// will read the messages in MAX, or none for a demo participant.
type DevUser struct {
	MaxUserID *int64 `json:"max_user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsAuthor  *bool  `json:"is_author"`
}

// DevLogin finds or creates a verified user, bypassing MAX. Dev mode only:
// it is how one person plays both the organiser and the participant.
func (s *Service) DevLogin(ctx context.Context, in DevUser) (domain.User, string, error) {
	var u domain.User
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		found := false
		if in.MaxUserID != nil {
			existing, err := tx.UserByMaxID(ctx, *in.MaxUserID)
			switch {
			case err == nil:
				u, found = existing, true
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		if !found {
			u = domain.User{
				ID: newID("user"), MaxUserID: in.MaxUserID, CityID: domain.DefaultCity, BotAvailable: true,
				NotifyReminders: true, NotifyRecommendation: true, CreatedAt: time.Now(), FirstName: "Участник",
			}
		}
		if in.FirstName != "" {
			u.FirstName = in.FirstName
		}
		if in.LastName != "" {
			u.LastName = in.LastName
		}
		u.IsVerified = true
		if in.IsAuthor != nil {
			u.IsAuthor = *in.IsAuthor
		}
		return tx.SaveUser(ctx, u)
	})
	if err != nil {
		return u, "", err
	}
	token, err := s.NewSession(ctx, u.ID)
	return u, token, err
}
