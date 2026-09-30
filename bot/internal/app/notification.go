// Package app holds the application services.
//
// These services are the bot's actual behaviour. They depend only on ports
// (maxapi.Client, core.Gateway, render.Renderer, idempotency.Store) and know
// nothing about HTTP, JSON or the MAX SDK. That is what makes the transport a
// detail: replacing Postman with the Core Backend, or the HTTP webhook with a
// long-polling loop, means writing a new caller, not changing anything here.
package app

import (
	"context"
	"errors"
	"fmt"

	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/observability"
	"hackatonBotMAX/internal/render"
)

// SendResult is the outcome of sending one notification.
type SendResult struct {
	// MessageID is the MAX message id of the delivered message.
	MessageID string
	// Duplicate is true when this request_id had already been processed and
	// nothing new was sent.
	Duplicate bool
}

// NotificationService renders and delivers outbound notifications.
//
// Note what it does *not* do: it never decides whether a notification should
// be sent. Seat availability, waitlist order, whether a user may still cancel
// — all of that belongs to the Core Backend. This service is told to notify
// someone and does exactly that.
//
// It is also independent of what triggered it. Today an HTTP handler calls
// Send; tomorrow a scheduler can call the same method with no change here.
// That is the seam described in docs/ARCHITECTURE.md.
type NotificationService struct {
	max         maxapi.Client
	renderer    render.Renderer
	idempotency idempotency.Store
}

// NewNotificationService wires the service.
func NewNotificationService(max maxapi.Client, renderer render.Renderer, store idempotency.Store) *NotificationService {
	if store == nil {
		store = idempotency.NoopStore{}
	}
	return &NotificationService{max: max, renderer: renderer, idempotency: store}
}

// ErrSendFailed wraps a failure to hand the message to MAX.
var ErrSendFailed = errors.New("notification: send failed")

// Send renders a notification and delivers it.
//
// Idempotency: a repeated request_id is answered with Duplicate=true and no
// second message. If the send then fails, the key is released so the caller's
// retry is not permanently swallowed — reporting an error while silently
// blocking the retry would be the worst of both worlds.
func (s *NotificationService) Send(ctx context.Context, notification domain.Notification) (*SendResult, error) {
	logger := observability.LoggerFrom(ctx).With(
		observability.KeyNotificationType, string(notification.Type),
		observability.KeyEventID, notification.Event.ID,
		observability.KeyRegistrationID, notification.Registration.ID,
		observability.KeyMaxUserID, notification.Recipient.MaxUserID,
	)

	idempotencyKey := "notification:" + notification.RequestID
	seen, err := s.idempotency.Seen(ctx, idempotencyKey)
	if err != nil {
		// A failing dedupe store must not block delivery: at worst the user
		// sees the message twice, which beats not being told their event was
		// cancelled.
		logger.Warn("idempotency check failed, proceeding", observability.KeyUpstreamError, err.Error())
	}
	if seen {
		logger.Info("duplicate notification ignored", observability.KeyResult, "duplicate")
		return &SendResult{Duplicate: true}, nil
	}

	message, err := s.renderer.Notification(notification)
	if err != nil {
		_ = s.idempotency.Forget(ctx, idempotencyKey)
		logger.Error("render failed", observability.KeyUpstreamError, err.Error())
		return nil, fmt.Errorf("render notification: %w", err)
	}

	sent, err := s.max.SendMessage(ctx, maxapi.SendMessageRequest{
		UserID:  notification.Recipient.MaxUserID,
		Message: message,
	})
	if err != nil {
		_ = s.idempotency.Forget(ctx, idempotencyKey)
		logger.Error("max send failed",
			observability.KeyResult, "error",
			observability.KeyUpstreamError, err.Error(),
		)
		return nil, fmt.Errorf("%w: %w", ErrSendFailed, err)
	}

	logger.Info("notification sent",
		observability.KeyResult, "sent",
		observability.KeyMessageID, sent.MessageID,
	)
	return &SendResult{MessageID: sent.MessageID}, nil
}
