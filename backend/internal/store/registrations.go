package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"hackatonCore/internal/domain"
)

const regColumns = `id, event_id, user_id, status, queue_position, offer_expires_at, cancel_reason,
	cancelled_late, from_waitlist, created_at, confirmed_at, cancelled_at`

func scanReg(row interface{ Scan(...any) error }) (domain.Registration, error) {
	var (
		r                        domain.Registration
		queue, offer, conf, canc sql.NullInt64
		reason                   sql.NullString
		late, fromWL             int
		created                  int64
	)
	err := row.Scan(&r.ID, &r.EventID, &r.UserID, &r.Status, &queue, &offer, &reason,
		&late, &fromWL, &created, &conf, &canc)
	if err != nil {
		return r, err
	}
	r.QueuePosition = intPtr(queue)
	r.OfferExpiresAt = timePtr(offer)
	r.CancelReason = stringPtr(reason)
	r.CancelledLate, r.FromWaitlist = late == 1, fromWL == 1
	r.CreatedAt = fromUnix(created)
	r.ConfirmedAt, r.CancelledAt = timePtr(conf), timePtr(canc)
	return r, nil
}

// SaveRegistration inserts or replaces a registration.
func (t *Tx) SaveRegistration(ctx context.Context, r domain.Registration) error {
	_, err := t.tx.ExecContext(ctx, `INSERT OR REPLACE INTO registrations (`+regColumns+`) VALUES (`+placeholders(12)+`)`,
		r.ID, r.EventID, r.UserID, r.Status, nullInt(r.QueuePosition), nullUnix(r.OfferExpiresAt),
		nullString(r.CancelReason), boolInt(r.CancelledLate), boolInt(r.FromWaitlist), unix(r.CreatedAt),
		nullUnix(r.ConfirmedAt), nullUnix(r.CancelledAt))
	if err != nil {
		return fmt.Errorf("store: save registration %s: %w", r.ID, err)
	}
	return nil
}

// Registration loads one registration.
func (t *Tx) Registration(ctx context.Context, id string) (domain.Registration, error) {
	r, err := scanReg(t.tx.QueryRowContext(ctx, `SELECT `+regColumns+` FROM registrations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// RegistrationsByEvent lists every registration of an event, oldest first.
func (t *Tx) RegistrationsByEvent(ctx context.Context, eventID string) ([]domain.Registration, error) {
	return t.queryRegs(ctx, `SELECT `+regColumns+` FROM registrations WHERE event_id = ? ORDER BY created_at, id`, eventID)
}

// RegistrationsByUser lists a user's registrations, oldest first.
func (t *Tx) RegistrationsByUser(ctx context.Context, userID string) ([]domain.Registration, error) {
	return t.queryRegs(ctx, `SELECT `+regColumns+` FROM registrations WHERE user_id = ? ORDER BY created_at, id`, userID)
}

// RegistrationsByStatus lists registrations in the given statuses.
func (t *Tx) RegistrationsByStatus(ctx context.Context, statuses ...string) ([]domain.Registration, error) {
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = s
	}
	return t.queryRegs(ctx, `SELECT `+regColumns+` FROM registrations WHERE status IN (`+placeholders(len(statuses))+`) ORDER BY created_at, id`, args...)
}

// ActiveRegistration finds the user's live registration for an event.
func (t *Tx) ActiveRegistration(ctx context.Context, eventID, userID string) (domain.Registration, error) {
	r, err := scanReg(t.tx.QueryRowContext(ctx, `SELECT `+regColumns+` FROM registrations
		WHERE event_id = ? AND user_id = ? AND status IN (?, ?, ?, ?) LIMIT 1`,
		eventID, userID, domain.RegRegistered, domain.RegConfirmed, domain.RegWaitlist, domain.RegOffered))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (t *Tx) queryRegs(ctx context.Context, query string, args ...any) ([]domain.Registration, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Registration
	for rows.Next() {
		r, err := scanReg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
