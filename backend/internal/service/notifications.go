package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// Notification types the bot understands (bot/docs/INTEGRATION_MINIAPP.md §4).
const (
	TypeRegistrationCreated  = "registration_created"
	TypeReminder24h          = "reminder_24h"
	TypeConfirmationRequired = "confirmation_required"
	TypeConfirmationRetry    = "confirmation_retry"
	TypeReminder1h           = "reminder_1h"
	TypeWaitlistOffer        = "waitlist_offer"
	TypeEventUpdated         = "event_updated"
	TypeEventCancelled       = "event_cancelled"
)

// Envelope is the body of the bot's POST /api/v1/notifications. The bot
// rejects unknown fields, so this struct must match its contract exactly.
type Envelope struct {
	RequestID    string        `json:"request_id"`
	Type         string        `json:"type"`
	Recipient    Recipient     `json:"recipient"`
	Event        EnvelopeEvent `json:"event"`
	Registration *EnvelopeReg  `json:"registration,omitempty"`
	Data         *EnvelopeData `json:"data,omitempty"`
}

// Recipient is who gets the message.
type Recipient struct {
	MaxUserID int64 `json:"max_user_id"`
}

// EnvelopeEvent is the event as the message shows it.
type EnvelopeEvent struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	StartsAt   string `json:"starts_at"`
	Address    string `json:"address,omitempty"`
	MiniAppURL string `json:"mini_app_url,omitempty"`
}

// EnvelopeReg names the registration the buttons act on.
type EnvelopeReg struct {
	ID string `json:"id"`
}

// EnvelopeData carries type-specific fields.
type EnvelopeData struct {
	OfferExpiresAt   string   `json:"offer_expires_at,omitempty"`
	Changes          []Change `json:"changes,omitempty"`
	OrganizerMessage string   `json:"organizer_message,omitempty"`
}

// Change is one line of «что изменилось».
type Change struct {
	Field string `json:"field"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
}

// isReminder marks the types a user can switch off in their profile.
// Confirmation requests are not reminders: without an answer the seat is at
// risk, so they are always sent.
func isReminder(typ string) bool { return typ == TypeReminder24h || typ == TypeReminder1h }

// eventURL is the card link, or "" when the mini app's address is unknown.
func (s *Service) eventURL(eventID string) string {
	if s.cfg.MiniAppURL == "" {
		return ""
	}
	return s.cfg.MiniAppURL + "/" + eventID
}

// enqueue writes a notification into the outbox, in the caller's
// transaction. dedupKey makes it at-most-once: the same key is never sent
// twice, which is how "remind once" survives restarts and repeated ticks.
func (s *Service) enqueue(ctx context.Context, tx *store.Tx, typ string, user domain.User, e domain.Event,
	reg *domain.Registration, data *EnvelopeData, dedupKey string, now time.Time,
) (bool, error) {
	env := Envelope{
		RequestID: "ntf_" + NewToken()[:20],
		Type:      typ,
		Event: EnvelopeEvent{
			ID:         e.ID,
			Title:      e.Title,
			StartsAt:   domain.FormatTime(e.StartsAt, e.Location()),
			Address:    e.Address,
			MiniAppURL: s.eventURL(e.ID),
		},
		Data: data,
	}
	if reg != nil {
		env.Registration = &EnvelopeReg{ID: reg.ID}
	}

	status, reason := store.NotifPending, ""
	switch {
	case user.MaxUserID == nil || user.Demo:
		status, reason = store.NotifSkipped, "demo user: nobody reads these messages"
	case !user.BotAvailable:
		status, reason = store.NotifSkipped, "bot cannot write to this user (dialog closed)"
	case isReminder(typ) && !user.NotifyReminders:
		status, reason = store.NotifSkipped, "user turned reminders off"
	default:
		env.Recipient.MaxUserID = *user.MaxUserID
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return false, fmt.Errorf("encode %s: %w", typ, err)
	}
	regID := ""
	if reg != nil {
		regID = reg.ID
	}
	added, err := tx.EnqueueNotification(ctx, store.Notification{
		RequestID:      env.RequestID,
		DedupKey:       dedupKey,
		Type:           typ,
		UserID:         user.ID,
		EventID:        e.ID,
		RegistrationID: regID,
		Payload:        payload,
		Status:         status,
		NextAttemptAt:  time.Now(),
		LastError:      reason,
		CreatedAt:      now,
	})
	if err != nil {
		return false, err
	}
	if added {
		s.log.Info("notification queued", "type", typ, "user_id", user.ID, "event_id", e.ID,
			"registration_id", regID, "status", status, "reason", reason)
	}
	return added, nil
}

// notifyRegistrations sends one message per active registration of an
// event (event_updated, event_cancelled).
func (s *Service) notifyRegistrations(ctx context.Context, tx *store.Tx, e domain.Event, typ string,
	data *EnvelopeData, keyPrefix string, include func(domain.Registration) bool, now time.Time,
) (int, error) {
	regs, err := tx.RegistrationsByEvent(ctx, e.ID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range regs {
		if !include(r) {
			continue
		}
		user, err := tx.User(ctx, r.UserID)
		if err != nil {
			return 0, err
		}
		if _, err := s.enqueue(ctx, tx, typ, user, e, nil, data, keyPrefix+":"+r.ID, now); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}
