package real

import (
	"context"
	"net/http"
	"sync/atomic"
)

// statusRecorder is an http.RoundTripper that remembers the HTTP status of a
// request whose context carries a slot.
//
// The SDK turns a non-2xx reply from MAX into an error built from the JSON
// body alone and drops the status code. Most of the time that is enough, but
// "this user blocked the bot" is documented in MAX's schema only as a status
// (403), not as an error code. Recording the status here keeps that one
// distinction reliable without guessing at error codes.
//
// Requests without a slot pass through untouched, so wrapping the client's
// transport changes nothing for any other call.
type statusRecorder struct {
	next http.RoundTripper
}

func (t statusRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if resp != nil {
		if slot, ok := req.Context().Value(statusSlotKey{}).(*statusSlot); ok {
			slot.value.Store(int32(resp.StatusCode))
		}
	}
	return resp, err
}

type statusSlotKey struct{}

// statusSlot receives the status of the request made with its context. When
// the SDK retries, the last attempt wins, which is the one whose error the
// caller sees.
type statusSlot struct {
	value atomic.Int32
}

func (s *statusSlot) code() int { return int(s.value.Load()) }

// withStatusSlot returns a context that makes statusRecorder report into the
// returned slot.
func withStatusSlot(ctx context.Context) (context.Context, *statusSlot) {
	slot := &statusSlot{}
	return context.WithValue(ctx, statusSlotKey{}, slot), slot
}
