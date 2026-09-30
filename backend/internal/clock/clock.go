// Package clock is the backend's single source of "now".
//
// Every business rule that depends on time — reminders, the confirmation
// window, offer expiry, "registration closes an hour before" — reads the time
// from here and never from time.Now directly. That is what makes the rules
// testable without waiting: tests use a fixed clock, and in dev mode the
// running server can be moved forward ("travel") so that a reminder due
// tomorrow is sent right now.
package clock

import (
	"sync"
	"time"
)

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// Travel is the clock the server runs on: real time plus an adjustable
// offset. The offset is zero unless a developer moves it through the dev API.
type Travel struct {
	mu     sync.RWMutex
	offset time.Duration
	real   func() time.Time
}

// NewTravel returns a clock that starts at real time.
func NewTravel() *Travel { return &Travel{real: time.Now} }

// Now returns real time shifted by the current offset.
func (t *Travel) Now() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.real().Add(t.offset)
}

// Offset is how far the clock is ahead of real time.
func (t *Travel) Offset() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.offset
}

// Advance moves the clock forward (or back, for a negative d).
func (t *Travel) Advance(d time.Duration) time.Time {
	t.mu.Lock()
	t.offset += d
	t.mu.Unlock()
	return t.Now()
}

// Set moves the clock so that Now returns at.
func (t *Travel) Set(at time.Time) time.Time {
	t.mu.Lock()
	t.offset = at.Sub(t.real())
	t.mu.Unlock()
	return t.Now()
}

// Reset returns the clock to real time.
func (t *Travel) Reset() time.Time {
	t.mu.Lock()
	t.offset = 0
	t.mu.Unlock()
	return t.Now()
}

// Fixed is a clock for tests: it only moves when told to.
type Fixed struct {
	mu  sync.Mutex
	now time.Time
}

// NewFixed returns a clock stopped at now.
func NewFixed(now time.Time) *Fixed { return &Fixed{now: now} }

// Now returns the stopped time.
func (f *Fixed) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the stopped time forward.
func (f *Fixed) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}

// Set stops the clock at a new moment.
func (f *Fixed) Set(at time.Time) {
	f.mu.Lock()
	f.now = at
	f.mu.Unlock()
}
