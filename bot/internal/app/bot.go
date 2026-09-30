package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/idempotency"
	"hackatonBotMAX/internal/maxapi"
	"hackatonBotMAX/internal/observability"
	"hackatonBotMAX/internal/render"
)

// BotService handles everything coming *from* MAX.
//
// This is the type the whole ports-and-adapters arrangement exists to protect.
// It is written against core.Gateway, so the switch from StubCoreGateway to
// HTTPCoreGateway — and later to a direct in-process call if the bot and the
// backend become one binary — is a change in main.go and nothing else.
type BotService struct {
	max         maxapi.Client
	gateway     core.Gateway
	renderer    render.Renderer
	idempotency idempotency.Store
	// miniAppURL is the default link offered in greetings and fallbacks.
	miniAppURL string
}

// BotServiceOption customises the service.
type BotServiceOption func(*BotService)

// WithMiniAppURL sets the default mini app link.
func WithMiniAppURL(url string) BotServiceOption {
	return func(s *BotService) { s.miniAppURL = strings.TrimSpace(url) }
}

// NewBotService wires the service.
func NewBotService(
	max maxapi.Client,
	gateway core.Gateway,
	renderer render.Renderer,
	store idempotency.Store,
	opts ...BotServiceOption,
) *BotService {
	if store == nil {
		store = idempotency.NoopStore{}
	}
	service := &BotService{max: max, gateway: gateway, renderer: renderer, idempotency: store}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// UpdateOutcome describes what the service did with an update. It is returned
// for logging and tests, not for the user.
type UpdateOutcome struct {
	// Handled is false for update types the bot ignores.
	Handled bool
	// Duplicate is true when the update was a redelivery.
	Duplicate bool
	// Action is the decoded action, for message_callback updates.
	Action domain.ActionType
	// Result summarises the outcome: "ok", "business_error", "unavailable".
	Result string
}

// Outcome result values.
const (
	ResultOK            = "ok"
	ResultIgnored       = "ignored"
	ResultDuplicate     = "duplicate"
	ResultBusinessError = "business_error"
	ResultUnavailable   = "unavailable"
	ResultBadPayload    = "bad_payload"
)

// HandleUpdate dispatches one MAX update.
//
// It never returns an error for an update it simply does not handle: an
// unknown event type is acknowledged and dropped, because failing the webhook
// would make MAX retry an event the bot will never understand. Errors are
// reserved for failures the caller should know about, and even then the user
// has already been answered.
func (s *BotService) HandleUpdate(ctx context.Context, update maxapi.Update) (UpdateOutcome, error) {
	logger := observability.LoggerFrom(ctx).With(
		observability.KeyUpdateType, string(update.Type),
		observability.KeyMaxUserID, update.ActorUserID(),
	)
	// Handlers log through the context; give them the fields above so a
	// webhook-delivered update is as traceable as a polled one.
	ctx = observability.WithLogger(ctx, logger)

	// Deduplicate before doing anything with side effects. MAX may redeliver
	// a webhook when our response was slow, and confirming a registration
	// twice would send the user two answers.
	if key := update.IdempotencyKey(); key != "" {
		seen, err := s.idempotency.Seen(ctx, "update:"+key)
		if err != nil {
			logger.Warn("idempotency check failed, proceeding", observability.KeyUpstreamError, err.Error())
		}
		if seen {
			logger.Info("duplicate update ignored", observability.KeyResult, ResultDuplicate)
			return UpdateOutcome{Handled: true, Duplicate: true, Result: ResultDuplicate}, nil
		}
	}

	switch update.Type {
	case maxapi.UpdateBotStarted:
		return s.handleBotStarted(ctx, update)
	case maxapi.UpdateBotStopped:
		return s.handleDialogClosed(ctx, update, core.ReasonBotStopped)
	case maxapi.UpdateDialogRemoved:
		return s.handleDialogClosed(ctx, update, core.ReasonDialogRemoved)
	case maxapi.UpdateMessageCreated:
		return s.handleMessageCreated(ctx, update)
	case maxapi.UpdateMessageCallback:
		return s.handleCallback(ctx, update)
	default:
		// Not an error: MAX can deliver event types the bot never asked for,
		// and new ones may appear at any time.
		logger.Debug("update type not handled", observability.KeyResult, ResultIgnored)
		return UpdateOutcome{Handled: false, Result: ResultIgnored}, nil
	}
}

// handleBotStarted greets a user who just opened the dialog and tells the
// Core Backend the bot can now reach them.
func (s *BotService) handleBotStarted(ctx context.Context, update maxapi.Update) (UpdateOutcome, error) {
	userID := update.ActorUserID()
	if userID == 0 {
		return UpdateOutcome{Handled: false, Result: ResultIgnored}, nil
	}

	// Reported before the greeting and regardless of how it goes: MAX has
	// told us the dialog is open, and that is what the backend needs to know.
	s.reportBotStatus(ctx, update, true, core.ReasonBotStarted)

	message := s.renderer.Greeting(s.miniAppURL)
	if _, err := s.max.SendMessage(ctx, maxapi.SendMessageRequest{UserID: userID, Message: message}); err != nil {
		observability.LoggerFrom(ctx).Error("greeting send failed", observability.KeyUpstreamError, err.Error())
		return UpdateOutcome{Handled: true, Result: ResultUnavailable}, fmt.Errorf("send greeting: %w", err)
	}
	return UpdateOutcome{Handled: true, Result: ResultOK}, nil
}

// handleDialogClosed handles bot_stopped and dialog_removed: the user can no
// longer be reached, and the Core Backend is told so. Nothing is sent to the
// user; MAX would refuse it anyway.
func (s *BotService) handleDialogClosed(ctx context.Context, update maxapi.Update, reason core.BotStatusReason) (UpdateOutcome, error) {
	if update.ActorUserID() == 0 {
		return UpdateOutcome{Handled: false, Result: ResultIgnored}, nil
	}
	if !s.reportBotStatus(ctx, update, false, reason) {
		return UpdateOutcome{Handled: true, Result: ResultUnavailable}, nil
	}
	return UpdateOutcome{Handled: true, Result: ResultOK}, nil
}

// reportBotStatus tells the Core Backend whether the bot can reach the user
// behind update. It reports success.
//
// Best effort by design. A failure is logged and swallowed: returning it would
// make a webhook answer 502 and MAX redeliver an update the idempotency store
// has already marked as seen, so the retry would be dropped anyway and the
// user would gain nothing.
func (s *BotService) reportBotStatus(ctx context.Context, update maxapi.Update, available bool, reason core.BotStatusReason) bool {
	req := core.BotStatusRequest{
		MaxUserID:  update.ActorUserID(),
		Available:  available,
		Reason:     reason,
		OccurredAt: update.OccurredAt(),
		RequestID:  observability.RequestIDFrom(ctx),
	}
	if err := s.gateway.ReportBotStatus(ctx, req); err != nil {
		observability.LoggerFrom(ctx).Warn("bot status not reported to core",
			"reason", string(reason),
			"available", available,
			observability.KeyUpstreamError, err.Error(),
		)
		return false
	}
	return true
}

// handleMessageCreated replies to free-form text.
//
// Support is deliberately minimal. The bot is a notification channel, not an
// alternative to the mini app: a command menu here would invite users to look
// for a catalogue that does not exist.
func (s *BotService) handleMessageCreated(ctx context.Context, update maxapi.Update) (UpdateOutcome, error) {
	userID := update.ActorUserID()
	if userID == 0 {
		return UpdateOutcome{Handled: false, Result: ResultIgnored}, nil
	}
	// Ignore the bot's own messages, which would otherwise loop.
	if update.Message != nil && update.Message.Sender.IsBot {
		return UpdateOutcome{Handled: false, Result: ResultIgnored}, nil
	}

	text := strings.ToLower(strings.TrimSpace(update.MessageText()))
	var message domain.Message
	switch {
	case strings.HasPrefix(text, "/help"):
		message = s.renderer.Help(s.miniAppURL)
	case strings.HasPrefix(text, "/start"):
		message = s.renderer.Greeting(s.miniAppURL)
	default:
		message = s.renderer.FreeText(s.miniAppURL)
	}

	if _, err := s.max.SendMessage(ctx, maxapi.SendMessageRequest{UserID: userID, Message: message}); err != nil {
		observability.LoggerFrom(ctx).Error("fallback reply failed", observability.KeyUpstreamError, err.Error())
		return UpdateOutcome{Handled: true, Result: ResultUnavailable}, fmt.Errorf("send fallback: %w", err)
	}
	// Info, not Debug: this line is how a developer finds their own
	// max_user_id (DEMO_MAX_USER_ID) — write to the bot, read the log.
	observability.LoggerFrom(ctx).Info("message answered", observability.KeyResult, ResultOK)
	return UpdateOutcome{Handled: true, Result: ResultOK}, nil
}

// handleCallback is the heart of the inbound path:
//
//	decode payload -> CoreGateway -> answer the callback, replacing the message
//
// The answer always happens, on every branch. MAX keeps a spinner on the
// button until the bot answers, so an unanswered callback is a visibly broken
// bot regardless of what went wrong behind it.
func (s *BotService) handleCallback(ctx context.Context, update maxapi.Update) (UpdateOutcome, error) {
	logger := observability.LoggerFrom(ctx)

	if update.Callback == nil || update.Callback.CallbackID == "" {
		logger.Warn("message_callback without callback payload", observability.KeyResult, ResultBadPayload)
		return UpdateOutcome{Handled: false, Result: ResultBadPayload}, nil
	}

	callbackID := update.Callback.CallbackID
	// Identity comes from the update body, never from the payload: a payload
	// round-trips through the client and cannot be trusted to say who acted.
	userID := update.ActorUserID()

	payload, err := callback.Decode(update.Callback.Payload)
	if err != nil {
		// A stale button from an older bot version, or a hand-crafted value.
		// The user gets a polite explanation, not a parse error.
		logger.Warn("callback payload rejected",
			observability.KeyResult, ResultBadPayload,
			observability.KeyUpstreamError, err.Error(),
		)
		s.answerFailure(ctx, callbackID, "", err)
		return UpdateOutcome{Handled: true, Result: ResultBadPayload}, nil
	}

	action := domain.CallbackAction{
		Action:         payload.Action,
		RegistrationID: payload.RegistrationID,
		EventID:        payload.EventID,
		MaxUserID:      userID,
		CallbackID:     callbackID,
		MessageID:      update.MessageID(),
		RequestID:      observability.RequestIDFrom(ctx),
		OccurredAt:     update.OccurredAt(),
	}

	logger = logger.With(
		observability.KeyCallbackAction, string(action.Action),
		observability.KeyRegistrationID, action.RegistrationID,
		observability.KeyEventID, action.EventID,
	)

	result, err := s.dispatch(ctx, action)
	if err != nil {
		outcome := ResultBusinessError
		if core.CodeOf(err) == core.CodeUnavailable {
			outcome = ResultUnavailable
		}
		logger.Warn("core action failed",
			observability.KeyResult, outcome,
			observability.KeyUpstreamError, err.Error(),
		)
		s.answerFailure(ctx, callbackID, action.Action, err)
		return UpdateOutcome{Handled: true, Action: action.Action, Result: outcome}, nil
	}

	// Replace the original message. The replacement carries no action
	// buttons, which is what retires the stale "Приду / Не смогу" pair so it
	// cannot fire a second backend call.
	event := domain.Event{ID: action.EventID, MiniAppURL: s.miniAppURL}
	if result != nil && result.Event != nil {
		event.ID = result.Event.ID
		event.Title = result.Event.Title
		event.Address = result.Event.Address
		if result.Event.StartsAt != nil {
			event.StartsAt = *result.Event.StartsAt
		}
	}

	replacement := s.renderer.ActionSucceeded(action.Action, event)
	answer := maxapi.AnswerCallbackRequest{
		CallbackID:   callbackID,
		Notification: s.renderer.ActionNotice(action.Action, nil),
		Message:      &replacement,
	}
	if err := s.max.AnswerCallback(ctx, answer); err != nil {
		// The business action already succeeded upstream; only the
		// acknowledgement failed. Log it and report it, but do not undo
		// anything or tell the user it failed.
		logger.Error("callback answer failed",
			observability.KeyResult, ResultUnavailable,
			observability.KeyUpstreamError, err.Error(),
		)
		return UpdateOutcome{Handled: true, Action: action.Action, Result: ResultUnavailable},
			fmt.Errorf("answer callback: %w", err)
	}

	logger.Info("callback handled", observability.KeyResult, ResultOK)
	return UpdateOutcome{Handled: true, Action: action.Action, Result: ResultOK}, nil
}

// dispatch routes a decoded action to the gateway.
func (s *BotService) dispatch(ctx context.Context, action domain.CallbackAction) (*core.ActionResult, error) {
	req := core.ActionRequest{
		RegistrationID: action.RegistrationID,
		EventID:        action.EventID,
		MaxUserID:      action.MaxUserID,
		RequestID:      action.RequestID,
		OccurredAt:     action.OccurredAt,
	}

	switch action.Action {
	case domain.ActionConfirmRegistration:
		return s.gateway.ConfirmRegistration(ctx, req)
	case domain.ActionCancelRegistration:
		return s.gateway.CancelRegistration(ctx, req)
	case domain.ActionAcceptWaitlist:
		return s.gateway.AcceptWaitlistOffer(ctx, req)
	case domain.ActionDeclineWaitlist:
		return s.gateway.DeclineWaitlistOffer(ctx, req)
	default:
		// Unreachable while Decode and ActionType stay in sync; kept so a
		// future action added to one and not the other degrades into a
		// polite message instead of a nil-pointer panic.
		return nil, fmt.Errorf("%w: %q", callback.ErrUnknownAction, action.Action)
	}
}

// answerFailure acknowledges a callback that could not be carried out.
//
// The user sees plain Russian and a way forward. Whether the buttons survive
// depends on the failure: a permanent one retires them, a temporary one leaves
// them so the user can simply press again.
func (s *BotService) answerFailure(ctx context.Context, callbackID string, action domain.ActionType, cause error) {
	answer := maxapi.AnswerCallbackRequest{
		CallbackID:   callbackID,
		Notification: s.renderer.ActionNotice(action, cause),
	}

	if isPermanentFailure(cause) {
		replacement := s.renderer.ActionFailed(action, cause)
		answer.Message = &replacement
	}

	if err := s.max.AnswerCallback(ctx, answer); err != nil {
		observability.LoggerFrom(ctx).Error("failure answer could not be delivered",
			observability.KeyUpstreamError, err.Error())
	}
}

// isPermanentFailure reports whether retrying could never help.
//
// The distinction drives the UX: a cancelled event or an expired offer means
// the buttons should go away, while "the backend is down" means leaving them
// in place so the user can retry in a minute.
func isPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, callback.ErrUnknownAction) ||
		errors.Is(err, callback.ErrUnsupportedVersion) ||
		errors.Is(err, callback.ErrMalformed) ||
		errors.Is(err, callback.ErrEmptyPayload) {
		return true
	}

	var coreErr *core.Error
	if errors.As(err, &coreErr) {
		return coreErr.IsBusiness()
	}
	return false
}
