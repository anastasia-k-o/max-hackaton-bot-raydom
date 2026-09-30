package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"hackatonCore/internal/domain"
)

const eventColumns = `id, title, short_description, description, category_id, tag_ids, starts_at,
	duration_min, timezone, city_id, district, address, how_to_find, lat, lon, capacity,
	extra_registered, extra_waitlist, level, age_limit, bring, author_id, author_name, contact,
	cover_url, status, moderation_flags, published_at, views, popularity, version, created_at, updated_at`

func scanEvent(row interface{ Scan(...any) error }) (domain.Event, error) {
	var (
		e                          domain.Event
		tags, flags                string
		starts, published, cr, upd int64
		lat, lon                   sql.NullFloat64
		capacity                   sql.NullInt64
		level, age, bring, cover   sql.NullString
	)
	err := row.Scan(&e.ID, &e.Title, &e.ShortDescription, &e.Description, &e.CategoryID, &tags, &starts,
		&e.DurationMin, &e.Timezone, &e.CityID, &e.District, &e.Address, &e.HowToFind, &lat, &lon, &capacity,
		&e.ExtraRegistered, &e.ExtraWaitlist, &level, &age, &bring, &e.AuthorID, &e.AuthorName, &e.Contact,
		&cover, &e.Status, &flags, &published, &e.Views, &e.Popularity, &e.Version, &cr, &upd)
	if err != nil {
		return e, err
	}
	e.TagIDs, e.ModerationFlags = parseList(tags), parseList(flags)
	e.StartsAt, e.PublishedAt, e.CreatedAt, e.UpdatedAt = fromUnix(starts), fromUnix(published), fromUnix(cr), fromUnix(upd)
	e.Lat, e.Lon = floatPtr(lat), floatPtr(lon)
	e.Capacity = intPtr(capacity)
	e.Level, e.AgeLimit, e.Bring, e.CoverURL = stringPtr(level), stringPtr(age), stringPtr(bring), stringPtr(cover)
	return e, nil
}

// SaveEvent inserts or replaces an event.
func (t *Tx) SaveEvent(ctx context.Context, e domain.Event) error {
	_, err := t.tx.ExecContext(ctx, `INSERT OR REPLACE INTO events (`+eventColumns+`) VALUES (`+placeholders(33)+`)`,
		e.ID, e.Title, e.ShortDescription, e.Description, e.CategoryID, jsonList(e.TagIDs), unix(e.StartsAt),
		e.DurationMin, e.Timezone, e.CityID, e.District, e.Address, e.HowToFind, nullFloat(e.Lat), nullFloat(e.Lon),
		nullInt(e.Capacity), e.ExtraRegistered, e.ExtraWaitlist, nullString(e.Level), nullString(e.AgeLimit),
		nullString(e.Bring), e.AuthorID, e.AuthorName, e.Contact, nullString(e.CoverURL), e.Status,
		jsonList(e.ModerationFlags), unix(e.PublishedAt), e.Views, e.Popularity, e.Version,
		unix(e.CreatedAt), unix(e.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: save event %s: %w", e.ID, err)
	}
	return nil
}

// Event loads one event.
func (t *Tx) Event(ctx context.Context, id string) (domain.Event, error) {
	e, err := scanEvent(t.tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// Events lists events, ordered by start. An empty status lists all.
func (t *Tx) Events(ctx context.Context, status string) ([]domain.Event, error) {
	query := `SELECT ` + eventColumns + ` FROM events`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	return t.queryEvents(ctx, query+` ORDER BY starts_at`, args...)
}

// UpcomingEvents lists published events that have not started by now and
// start before until. The scheduler looks only at these.
func (t *Tx) UpcomingEvents(ctx context.Context, now, until time.Time) ([]domain.Event, error) {
	return t.queryEvents(ctx, `SELECT `+eventColumns+` FROM events
		WHERE status = ? AND starts_at > ? AND starts_at <= ? ORDER BY starts_at`,
		domain.EventPublished, unix(now), unix(until))
}

// EventsByAuthor lists an organiser's events.
func (t *Tx) EventsByAuthor(ctx context.Context, authorID string) ([]domain.Event, error) {
	return t.queryEvents(ctx, `SELECT `+eventColumns+` FROM events WHERE author_id = ? ORDER BY starts_at`, authorID)
}

func (t *Tx) queryEvents(ctx context.Context, query string, args ...any) ([]domain.Event, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddView counts a card view.
func (t *Tx) AddView(ctx context.Context, eventID string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE events SET views = views + 1 WHERE id = ?`, eventID)
	return err
}

// AddComplaint stores a complaint about an event.
func (t *Tx) AddComplaint(ctx context.Context, eventID, userID, text string, at time.Time) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO complaints (event_id, user_id, text, created_at) VALUES (?, ?, ?, ?)`,
		eventID, userID, text, unix(at))
	return err
}
