package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Notification statuses in the outbox.
const (
	NotifPending = "pending" // waiting to be delivered to the bot
	NotifSent    = "sent"    // the bot accepted it
	NotifFailed  = "failed"  // gave up: the bot refused it, or retries ran out
	NotifSkipped = "skipped" // never sent on purpose: no MAX id, bot blocked, reminders off
	NotifLogged  = "logged"  // NOTIFY_MODE=log: written to the log instead of the bot
)

// Notification is one outbox row.
type Notification struct {
	ID             int64
	RequestID      string
	DedupKey       string
	Type           string
	UserID         string
	EventID        string
	RegistrationID string
	Payload        []byte
	Status         string
	Attempts       int
	NextAttemptAt  time.Time
	LastError      string
	MessageID      string
	CreatedAt      time.Time
	SentAt         *time.Time
}

const notifColumns = `id, request_id, dedup_key, type, user_id, event_id, registration_id, payload,
	status, attempts, next_attempt_at, last_error, message_id, created_at, sent_at`

func scanNotif(row interface{ Scan(...any) error }) (Notification, error) {
	var (
		n                   Notification
		regID, lastErr, mid sql.NullString
		next, created       int64
		sent                sql.NullInt64
		payload             string
	)
	err := row.Scan(&n.ID, &n.RequestID, &n.DedupKey, &n.Type, &n.UserID, &n.EventID, &regID, &payload,
		&n.Status, &n.Attempts, &next, &lastErr, &mid, &created, &sent)
	if err != nil {
		return n, err
	}
	n.RegistrationID, n.LastError, n.MessageID = regID.String, lastErr.String, mid.String
	n.Payload = []byte(payload)
	n.NextAttemptAt, n.CreatedAt = fromUnix(next), fromUnix(created)
	n.SentAt = timePtr(sent)
	return n, nil
}

// EnqueueNotification adds a row unless one with the same dedup key exists.
// It reports whether the row is new.
func (t *Tx) EnqueueNotification(ctx context.Context, n Notification) (bool, error) {
	var regID any
	if n.RegistrationID != "" {
		regID = n.RegistrationID
	}
	var lastErr any
	if n.LastError != "" {
		lastErr = n.LastError
	}
	res, err := t.tx.ExecContext(ctx, `INSERT INTO notifications
		(request_id, dedup_key, type, user_id, event_id, registration_id, payload, status, attempts, next_attempt_at, last_error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?) ON CONFLICT(dedup_key) DO NOTHING`,
		n.RequestID, n.DedupKey, n.Type, n.UserID, n.EventID, regID, string(n.Payload), n.Status,
		unix(n.NextAttemptAt), lastErr, unix(n.CreatedAt))
	if err != nil {
		return false, fmt.Errorf("store: enqueue %s: %w", n.DedupKey, err)
	}
	added, _ := res.RowsAffected()
	return added > 0, nil
}

// HasNotification reports whether a dedup key was ever enqueued.
func (t *Tx) HasNotification(ctx context.Context, dedupKey string) (bool, error) {
	var one int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM notifications WHERE dedup_key = ?`, dedupKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// DueNotifications returns pending rows whose next attempt has come.
func (t *Tx) DueNotifications(ctx context.Context, now time.Time, limit int) ([]Notification, error) {
	return t.queryNotifs(ctx, `SELECT `+notifColumns+` FROM notifications
		WHERE status = ? AND next_attempt_at <= ? ORDER BY id LIMIT ?`, NotifPending, unix(now), limit)
}

// RecentNotifications lists the newest rows, for the dev dashboard.
func (t *Tx) RecentNotifications(ctx context.Context, limit int) ([]Notification, error) {
	return t.queryNotifs(ctx, `SELECT `+notifColumns+` FROM notifications ORDER BY id DESC LIMIT ?`, limit)
}

// NotificationsSince lists rows with id greater than afterID, oldest first.
func (t *Tx) NotificationsSince(ctx context.Context, afterID int64) ([]Notification, error) {
	return t.queryNotifs(ctx, `SELECT `+notifColumns+` FROM notifications WHERE id > ? ORDER BY id`, afterID)
}

// LastNotificationID is the newest outbox id, 0 when empty.
func (t *Tx) LastNotificationID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := t.tx.QueryRowContext(ctx, `SELECT MAX(id) FROM notifications`).Scan(&id)
	return id.Int64, err
}

// MarkNotification records a delivery outcome.
func (t *Tx) MarkNotification(ctx context.Context, id int64, status string, attempts int, next time.Time, lastErr, messageID string, sentAt *time.Time) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE notifications SET status = ?, attempts = ?, next_attempt_at = ?,
		last_error = ?, message_id = ?, sent_at = ? WHERE id = ?`,
		status, attempts, unix(next), nullIfEmpty(lastErr), nullIfEmpty(messageID), nullUnix(sentAt), id)
	return err
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func (t *Tx) queryNotifs(ctx context.Context, query string, args ...any) ([]Notification, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotif(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// StoredResponse is a remembered reply to a mutating request.
type StoredResponse struct {
	StatusCode int
	Body       []byte
}

// Idempotent returns the stored response for key, if any.
func (t *Tx) Idempotent(ctx context.Context, key string) (*StoredResponse, error) {
	var r StoredResponse
	err := t.tx.QueryRowContext(ctx, `SELECT status_code, body FROM idempotency WHERE key = ?`, key).Scan(&r.StatusCode, &r.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// RememberResponse stores the response for key.
func (t *Tx) RememberResponse(ctx context.Context, key string, status int, body []byte, now time.Time) error {
	_, err := t.tx.ExecContext(ctx, `INSERT OR IGNORE INTO idempotency (key, status_code, body, created_at) VALUES (?, ?, ?, ?)`,
		key, status, body, unix(now))
	return err
}
