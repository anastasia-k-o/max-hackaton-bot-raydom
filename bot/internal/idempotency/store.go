// Package idempotency remembers which requests have already been handled.
//
// Scope, stated plainly: the default implementation is an in-memory TTL map.
// It deduplicates within one process and forgets everything on restart, which
// is the right trade for a hackathon MVP — the alternative is Redis or
// Postgres, and the bot owns no business data worth that operational cost.
//
// What matters is that Store is an interface. When the bot is deployed as more
// than one replica, the fix is a Redis-backed Store and one line in main, not
// a redesign. This limitation is documented in README and ARCHITECTURE rather
// than hidden.
package idempotency

import (
	"context"
	"sync"
	"time"
)

// Store records processed request keys.
type Store interface {
	// Seen atomically checks whether key was recorded and records it.
	// It returns true when the key had already been seen, meaning the
	// caller should skip the work.
	Seen(ctx context.Context, key string) (bool, error)
	// Forget removes a key. It is used to roll back the reservation when
	// the work fails, so a transient failure does not permanently suppress
	// a legitimate retry.
	Forget(ctx context.Context, key string) error
}

// entry is a recorded key with its expiry.
type entry struct {
	expiresAt time.Time
}

// MemoryStore is an in-process TTL store.
//
// It is safe for concurrent use and bounded in size: expired keys are swept
// lazily on write, and a hard cap prevents unbounded growth if traffic outruns
// the sweep.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]entry
	ttl     time.Duration
	maxSize int
	now     func() time.Time
}

// MemoryOption customises a MemoryStore.
type MemoryOption func(*MemoryStore)

// WithClock overrides the time source, for deterministic tests.
func WithClock(now func() time.Time) MemoryOption {
	return func(s *MemoryStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithMaxSize overrides the hard cap on stored keys.
func WithMaxSize(n int) MemoryOption {
	return func(s *MemoryStore) {
		if n > 0 {
			s.maxSize = n
		}
	}
}

// NewMemoryStore builds an in-memory store with the given TTL.
//
// The TTL should comfortably exceed the window in which MAX might redeliver a
// webhook; 30 minutes is the default and is generous for that purpose.
func NewMemoryStore(ttl time.Duration, opts ...MemoryOption) *MemoryStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	store := &MemoryStore{
		entries: make(map[string]entry),
		ttl:     ttl,
		maxSize: 10000,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

// Seen implements Store.
//
// An empty key is never considered seen and is not recorded: callers that
// cannot derive a stable key must still do their work.
func (s *MemoryStore) Seen(_ context.Context, key string) (bool, error) {
	if key == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if existing, ok := s.entries[key]; ok && existing.expiresAt.After(now) {
		return true, nil
	}

	s.sweepLocked(now)
	s.entries[key] = entry{expiresAt: now.Add(s.ttl)}
	return false, nil
}

// Forget implements Store.
func (s *MemoryStore) Forget(_ context.Context, key string) error {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

// Len reports the number of live entries. Used by tests and /ready diagnostics.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	count := 0
	for _, e := range s.entries {
		if e.expiresAt.After(now) {
			count++
		}
	}
	return count
}

// sweepLocked drops expired entries, and falls back to clearing everything if
// the map is still over its cap afterwards.
//
// Dropping the whole map in that extreme case is acceptable: the cost is at
// worst a duplicate message, whereas unbounded growth is an outage.
func (s *MemoryStore) sweepLocked(now time.Time) {
	if len(s.entries) < s.maxSize {
		return
	}
	for key, e := range s.entries {
		if !e.expiresAt.After(now) {
			delete(s.entries, key)
		}
	}
	if len(s.entries) >= s.maxSize {
		s.entries = make(map[string]entry, s.maxSize/2)
	}
}

// NoopStore disables deduplication entirely. It exists so the behaviour can be
// turned off in tests that want every delivery processed.
type NoopStore struct{}

// Seen always reports false.
func (NoopStore) Seen(context.Context, string) (bool, error) { return false, nil }

// Forget does nothing.
func (NoopStore) Forget(context.Context, string) error { return nil }
