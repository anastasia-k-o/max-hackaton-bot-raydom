// Package store keeps the backend's state in SQLite.
//
// One file, no server, nothing to install: the right trade for a hackathon.
// All writes go through one connection, so transactions are serialised and
// the seat-counting rules never race each other. The rest of the backend
// talks to the store through Tx and never sees SQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Store is the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (and creates if needed) the database at path. ":memory:" gives
// a private in-memory database, for tests.
func Open(path string) (*Store, error) {
	dsn := "file::memory:?_foreign_keys=on&_txlock=immediate"
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: create %s: %w", dir, err)
			}
		}
		dsn = "file:" + path + "?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// One connection: SQLite has one writer anyway, and a single connection
	// makes every transaction see the previous one's writes.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database for /health.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Tx is a running transaction with the typed queries.
type Tx struct {
	tx *sql.Tx
}

// InTx runs fn in a transaction, committing when it returns nil.
func (s *Store) InTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(&Tx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ErrNotFound is returned by single-row getters.
var ErrNotFound = errors.New("store: not found")

func (s *Store) migrate() error {
	for i, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("store: migrate step %d: %w", i, err)
		}
	}
	// Columns added after the first release. SQLite has no ADD COLUMN IF
	// NOT EXISTS, so the table is inspected first; an old database keeps
	// its data.
	for _, c := range addedColumns {
		exists, err := s.hasColumn(c.table, c.column)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.column + " " + c.def); err != nil {
				return fmt.Errorf("store: add %s.%s: %w", c.table, c.column, err)
			}
		}
	}
	return nil
}

var addedColumns = []struct{ table, column, def string }{
	{"users", "interests", "TEXT NOT NULL DEFAULT '[]'"},
	{"users", "demo", "INTEGER NOT NULL DEFAULT 0"},
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return false, fmt.Errorf("store: inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// schema is idempotent: every statement can run on an existing database.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		max_user_id INTEGER UNIQUE,
		first_name TEXT NOT NULL,
		last_name TEXT NOT NULL DEFAULT '',
		photo_url TEXT,
		is_verified INTEGER NOT NULL DEFAULT 0,
		is_author INTEGER NOT NULL DEFAULT 0,
		city_id TEXT NOT NULL,
		district TEXT,
		bot_available INTEGER NOT NULL DEFAULT 1,
		bot_status_at INTEGER,
		onboarding_completed INTEGER NOT NULL DEFAULT 0,
		consent_accepted_at INTEGER,
		notify_reminders INTEGER NOT NULL DEFAULT 1,
		notify_recommendations INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS events (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		short_description TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		category_id TEXT NOT NULL,
		tag_ids TEXT NOT NULL DEFAULT '[]',
		starts_at INTEGER NOT NULL,
		duration_min INTEGER NOT NULL,
		timezone TEXT NOT NULL,
		city_id TEXT NOT NULL,
		district TEXT NOT NULL DEFAULT '',
		address TEXT NOT NULL DEFAULT '',
		how_to_find TEXT NOT NULL DEFAULT '',
		lat REAL, lon REAL,
		capacity INTEGER,
		extra_registered INTEGER NOT NULL DEFAULT 0,
		extra_waitlist INTEGER NOT NULL DEFAULT 0,
		level TEXT, age_limit TEXT, bring TEXT,
		author_id TEXT NOT NULL REFERENCES users(id),
		author_name TEXT NOT NULL,
		contact TEXT NOT NULL DEFAULT '',
		cover_url TEXT,
		status TEXT NOT NULL,
		moderation_flags TEXT NOT NULL DEFAULT '[]',
		published_at INTEGER NOT NULL,
		views INTEGER NOT NULL DEFAULT 0,
		popularity INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS events_by_start ON events(status, starts_at)`,
	`CREATE TABLE IF NOT EXISTS registrations (
		id TEXT PRIMARY KEY,
		event_id TEXT NOT NULL REFERENCES events(id),
		user_id TEXT NOT NULL REFERENCES users(id),
		status TEXT NOT NULL,
		queue_position INTEGER,
		offer_expires_at INTEGER,
		cancel_reason TEXT,
		cancelled_late INTEGER NOT NULL DEFAULT 0,
		from_waitlist INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		confirmed_at INTEGER,
		cancelled_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS registrations_by_event ON registrations(event_id, status)`,
	`CREATE INDEX IF NOT EXISTS registrations_by_user ON registrations(user_id)`,
	// The outbox: a notification is written in the same transaction as the
	// change that causes it, then delivered to the bot separately. A crash
	// between the two loses nothing, and dedup_key makes "remind once" a
	// database constraint rather than a hope.
	`CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT NOT NULL UNIQUE,
		dedup_key TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL,
		user_id TEXT NOT NULL,
		event_id TEXT NOT NULL,
		registration_id TEXT,
		payload TEXT NOT NULL,
		status TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at INTEGER NOT NULL,
		last_error TEXT,
		message_id TEXT,
		created_at INTEGER NOT NULL,
		sent_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS notifications_due ON notifications(status, next_attempt_at)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id),
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS complaints (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL REFERENCES events(id),
		user_id TEXT NOT NULL REFERENCES users(id),
		text TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	)`,
	// Responses to mutating requests, keyed by the caller's request id, so a
	// retried click does not act twice.
	`CREATE TABLE IF NOT EXISTS idempotency (
		key TEXT PRIMARY KEY,
		status_code INTEGER NOT NULL,
		body BLOB NOT NULL,
		created_at INTEGER NOT NULL
	)`,
}

// Reset empties every table. Dev mode only.
func (s *Store) Reset(ctx context.Context) error {
	return s.InTx(ctx, func(tx *Tx) error {
		for _, table := range []string{"idempotency", "complaints", "sessions", "notifications", "registrations", "events", "users"} {
			if _, err := tx.tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return fmt.Errorf("store: reset %s: %w", table, err)
			}
		}
		return nil
	})
}

// prefixed qualifies every column of a list with a table alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// placeholders returns "?, ?, ?" for n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
