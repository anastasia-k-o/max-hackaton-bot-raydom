// Package observability wires structured logging and request correlation.
//
// The bot logs with log/slog in JSON. Every inbound request carries a request
// id, and that id travels on the context so any layer can attach it without
// threading a logger through every signature.
package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Log attribute keys. Using constants keeps field names consistent across
// packages, which is what makes the logs greppable.
const (
	KeyRequestID        = "request_id"
	KeyNotificationType = "notification_type"
	KeyRegistrationID   = "registration_id"
	KeyEventID          = "event_id"
	KeyMaxUserID        = "max_user_id"
	KeyCallbackAction   = "callback_action"
	KeyUpdateType       = "update_type"
	KeyMessageID        = "message_id"
	KeyResult           = "result"
	KeyUpstreamError    = "upstream_error"
	KeyStatus           = "status"
	KeyDurationMS       = "duration_ms"
	KeyMethod           = "method"
	KeyPath             = "path"
	KeyComponent        = "component"
)

// NewLogger builds the process logger at the requested level.
//
// Unknown levels fall back to info rather than failing: a typo in LOG_LEVEL
// should not stop the bot from starting.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}

type contextKey struct{ name string }

var (
	requestIDKey = contextKey{"request_id"}
	loggerKey    = contextKey{"logger"}
)

// WithRequestID stores a correlation id on the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestIDFrom returns the correlation id stored on the context, or "".
func RequestIDFrom(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok {
		return value
	}
	return ""
}

// WithLogger stores a request-scoped logger on the context.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// LoggerFrom returns the request-scoped logger, falling back to the default
// logger so callers never have to nil-check.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}
