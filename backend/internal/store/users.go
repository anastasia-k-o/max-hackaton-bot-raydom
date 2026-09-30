package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"hackatonCore/internal/domain"
)

const userColumns = `id, max_user_id, first_name, last_name, photo_url, is_verified, is_author,
	city_id, district, bot_available, bot_status_at, onboarding_completed, consent_accepted_at,
	notify_reminders, notify_recommendations, interests, demo, created_at`

func scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var (
		u                        domain.User
		maxID, botAt, consentAt  sql.NullInt64
		photo, district          sql.NullString
		verified, author, botOK  int
		onboarded, remind, recom int
		created                  int64
		interests                string
		demo                     int
	)
	err := row.Scan(&u.ID, &maxID, &u.FirstName, &u.LastName, &photo, &verified, &author,
		&u.CityID, &district, &botOK, &botAt, &onboarded, &consentAt, &remind, &recom, &interests, &demo, &created)
	if err != nil {
		return u, err
	}
	u.MaxUserID = int64Ptr(maxID)
	u.PhotoURL = stringPtr(photo)
	u.District = stringPtr(district)
	u.IsVerified, u.IsAuthor, u.BotAvailable = verified == 1, author == 1, botOK == 1
	u.BotStatusAt = timePtr(botAt)
	u.OnboardingCompleted = onboarded == 1
	u.ConsentAcceptedAt = timePtr(consentAt)
	u.NotifyReminders, u.NotifyRecommendation = remind == 1, recom == 1
	u.Interests = parseList(interests)
	u.Demo = demo == 1
	u.CreatedAt = fromUnix(created)
	return u, nil
}

// SaveUser inserts or replaces a user.
func (t *Tx) SaveUser(ctx context.Context, u domain.User) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO users (`+userColumns+`) VALUES (`+placeholders(18)+`)
		ON CONFLICT(id) DO UPDATE SET
			max_user_id = excluded.max_user_id, first_name = excluded.first_name, last_name = excluded.last_name,
			photo_url = excluded.photo_url, is_verified = excluded.is_verified, is_author = excluded.is_author,
			city_id = excluded.city_id, district = excluded.district, bot_available = excluded.bot_available,
			bot_status_at = excluded.bot_status_at, onboarding_completed = excluded.onboarding_completed,
			consent_accepted_at = excluded.consent_accepted_at, notify_reminders = excluded.notify_reminders,
			notify_recommendations = excluded.notify_recommendations, interests = excluded.interests,
			demo = excluded.demo`,
		u.ID, nullInt64(u.MaxUserID), u.FirstName, u.LastName, nullString(u.PhotoURL),
		boolInt(u.IsVerified), boolInt(u.IsAuthor), u.CityID, nullString(u.District),
		boolInt(u.BotAvailable), nullUnix(u.BotStatusAt), boolInt(u.OnboardingCompleted),
		nullUnix(u.ConsentAcceptedAt), boolInt(u.NotifyReminders), boolInt(u.NotifyRecommendation),
		jsonList(u.Interests), boolInt(u.Demo), unix(u.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: save user %s: %w", u.ID, err)
	}
	return nil
}

// User loads a user by id.
func (t *Tx) User(ctx context.Context, id string) (domain.User, error) {
	u, err := scanUser(t.tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// UserByMaxID loads a user by MAX id.
func (t *Tx) UserByMaxID(ctx context.Context, maxUserID int64) (domain.User, error) {
	u, err := scanUser(t.tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE max_user_id = ?`, maxUserID))
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// Users loads users by id, for reports.
func (t *Tx) Users(ctx context.Context) (map[string]domain.User, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT `+userColumns+` FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

// SetBotAvailable records whether the bot can write to a user. Only a newer
// report replaces an older one: MAX may deliver updates out of order.
func (t *Tx) SetBotAvailable(ctx context.Context, userID string, available bool, at time.Time) (bool, error) {
	res, err := t.tx.ExecContext(ctx, `UPDATE users SET bot_available = ?, bot_status_at = ?
		WHERE id = ? AND (bot_status_at IS NULL OR bot_status_at <= ?)`,
		boolInt(available), unix(at), userID, unix(at))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CreateSession stores a session token.
func (t *Tx) CreateSession(ctx context.Context, token, userID string, now time.Time, ttl time.Duration) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		token, userID, unix(now), unix(now.Add(ttl)))
	return err
}

// SessionUser resolves a live session to its user.
func (t *Tx) SessionUser(ctx context.Context, token string, now time.Time) (domain.User, error) {
	u, err := scanUser(t.tx.QueryRowContext(ctx, `SELECT `+prefixed("u.", userColumns)+`
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token = ? AND s.expires_at > ?`, token, unix(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}
