package idempotency

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreSeen(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	ctx := context.Background()

	seen, err := store.Seen(ctx, "key-1")
	if err != nil {
		t.Fatalf("Seen(): %v", err)
	}
	if seen {
		t.Error("a fresh key should not be reported as seen")
	}

	seen, err = store.Seen(ctx, "key-1")
	if err != nil {
		t.Fatalf("Seen(): %v", err)
	}
	if !seen {
		t.Error("a repeated key should be reported as seen")
	}

	if seen, _ := store.Seen(ctx, "key-2"); seen {
		t.Error("a different key should be independent")
	}
}

// TestEmptyKeysAreNeverDeduplicated: a caller that cannot derive a stable key
// must still get its work done.
func TestEmptyKeysAreNeverDeduplicated(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if seen, _ := store.Seen(ctx, ""); seen {
			t.Fatal("an empty key must never suppress work")
		}
	}
	if store.Len() != 0 {
		t.Errorf("empty keys should not be stored, Len() = %d", store.Len())
	}
}

func TestEntriesExpire(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	store := NewMemoryStore(time.Minute, WithClock(clock))
	ctx := context.Background()

	if seen, _ := store.Seen(ctx, "key"); seen {
		t.Fatal("first call")
	}
	if seen, _ := store.Seen(ctx, "key"); !seen {
		t.Fatal("within the TTL the key should still be seen")
	}

	now = now.Add(61 * time.Second)
	if seen, _ := store.Seen(ctx, "key"); seen {
		t.Error("after the TTL the key should have expired")
	}
}

// TestForgetReleasesTheKey is what lets a failed send be retried.
func TestForgetReleasesTheKey(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	ctx := context.Background()

	_, _ = store.Seen(ctx, "key")
	if err := store.Forget(ctx, "key"); err != nil {
		t.Fatalf("Forget(): %v", err)
	}
	if seen, _ := store.Seen(ctx, "key"); seen {
		t.Error("a forgotten key should be treated as fresh again")
	}
}

// TestMemoryStoreIsBounded: an inspection buffer that grows without limit is
// an outage waiting to happen.
func TestMemoryStoreIsBounded(t *testing.T) {
	store := NewMemoryStore(time.Hour, WithMaxSize(100))
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		if _, err := store.Seen(ctx, fmt.Sprintf("key-%d", i)); err != nil {
			t.Fatalf("Seen(): %v", err)
		}
	}
	if store.Len() > 200 {
		t.Errorf("Len() = %d, the cap is not being enforced", store.Len())
	}
}

// TestSeenIsAtomicUnderConcurrency: exactly one caller may win, or two
// goroutines would both send the same notification.
func TestSeenIsAtomicUnderConcurrency(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	ctx := context.Background()

	const goroutines = 50
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		freshWins int
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			seen, err := store.Seen(ctx, "contended")
			if err != nil {
				t.Error(err)
				return
			}
			if !seen {
				mu.Lock()
				freshWins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if freshWins != 1 {
		t.Errorf("%d goroutines saw the key as fresh, want exactly 1", freshWins)
	}
}

func TestNoopStore(t *testing.T) {
	var store Store = NoopStore{}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		seen, err := store.Seen(ctx, "key")
		if err != nil {
			t.Fatalf("Seen(): %v", err)
		}
		if seen {
			t.Error("NoopStore must never suppress work")
		}
	}
	if err := store.Forget(ctx, "key"); err != nil {
		t.Errorf("Forget(): %v", err)
	}
}

func TestZeroTTLFallsBackToADefault(t *testing.T) {
	store := NewMemoryStore(0)
	if store.ttl <= 0 {
		t.Error("a zero TTL should fall back to a sane default, not disable expiry maths")
	}
}
