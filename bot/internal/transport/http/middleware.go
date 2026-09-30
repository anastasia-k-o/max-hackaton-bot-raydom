// Package http is the HTTP adapter.
//
// Handlers here are deliberately thin: decode, validate, delegate to an
// application service, encode. No business logic, no MAX SDK, no message text.
// If a handler in this package starts making decisions, that is the signal it
// has taken on work belonging in internal/app.
package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/observability"
)

// Header names. The two authentication headers are different mechanisms and
// must never be conflated:
//
//	HeaderInternalAPIKey  Core Backend -> Bot   (our own API)
//	HeaderMaxSecret       MAX          -> Bot   (webhook, set by us on subscribe)
const (
	// HeaderInternalAPIKey authenticates the Core Backend (and Postman).
	HeaderInternalAPIKey = "X-Internal-Api-Key"
	// HeaderMaxSecret is the secret MAX echoes back on every webhook.
	// The name is fixed by MAX, not by us.
	HeaderMaxSecret = "X-Max-Bot-Api-Secret"
	// HeaderRequestID carries a caller-supplied correlation id.
	HeaderRequestID = "X-Request-Id"
)

// requestIDMiddleware assigns a correlation id to every request and puts it,
// together with a request-scoped logger, on the context.
//
// A caller-supplied X-Request-Id wins so a trace can span the Core Backend and
// the bot; otherwise one is generated.
func requestIDMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := strings.TrimSpace(r.Header.Get(HeaderRequestID))
			if requestID == "" || len(requestID) > 128 {
				requestID = newRequestID()
			}

			ctx := observability.WithRequestID(r.Context(), requestID)
			scoped := logger.With(observability.KeyRequestID, requestID)
			ctx = observability.WithLogger(ctx, scoped)

			w.Header().Set(HeaderRequestID, requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// loggingMiddleware emits one structured line per request.
//
// It records method, path, status and duration. It never logs headers or
// bodies, which is what keeps tokens, webhook secrets and API keys out of the
// logs by construction rather than by remembering to redact them.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		observability.LoggerFrom(r.Context()).Info("http request",
			observability.KeyMethod, r.Method,
			observability.KeyPath, r.URL.Path,
			observability.KeyStatus, recorder.status,
			observability.KeyDurationMS, time.Since(started).Milliseconds(),
		)
	})
}

// recoverMiddleware turns a panic into a 500 instead of a dead process.
//
// A malformed webhook must never take the bot down; MAX would keep retrying
// into a process that is no longer listening.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				observability.LoggerFrom(r.Context()).Error("panic recovered",
					"panic", recovered,
					observability.KeyPath, r.URL.Path,
				)
				writeError(w, r, contracts.CodeInternalError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// internalAPIKeyMiddleware guards the Core Backend -> Bot API.
//
// The comparison is constant-time: a naive == leaks key material through
// response timing, which is a cheap mistake to avoid.
func internalAPIKeyMiddleware(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := r.Header.Get(HeaderInternalAPIKey)
			if !secureEqual(provided, expected) {
				observability.LoggerFrom(r.Context()).Warn("internal api key rejected",
					observability.KeyPath, r.URL.Path,
				)
				writeError(w, r, contracts.CodeUnauthorized, "invalid or missing "+HeaderInternalAPIKey)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxWebhookSecretMiddleware guards the MAX -> Bot webhook.
//
// When no secret is configured (mock mode, where webhooks are simulated from
// Postman) the check is skipped. In real mode the config layer makes
// MAX_WEBHOOK_SECRET mandatory, so an unprotected webhook cannot reach
// production by accident.
func maxWebhookSecretMiddleware(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" {
				next.ServeHTTP(w, r)
				return
			}
			provided := r.Header.Get(HeaderMaxSecret)
			if !secureEqual(provided, expected) {
				observability.LoggerFrom(r.Context()).Warn("max webhook secret rejected")
				writeError(w, r, contracts.CodeUnauthorized, "invalid or missing "+HeaderMaxSecret)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// chain applies middleware so that the first argument is the outermost layer.
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// secureEqual compares two secrets in constant time.
func secureEqual(provided, expected string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// newRequestID generates a random correlation id.
func newRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// A non-unique id is far better than failing the request over it.
		return "req-" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return "req-" + hex.EncodeToString(buf)
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}
