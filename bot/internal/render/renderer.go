// Package render turns domain objects into platform-neutral messages.
//
// This is the only place that knows what the bot says. Handlers and services
// pass structured data in and get a domain.Message out; they never concatenate
// user-facing strings themselves. That separation is what makes the copy
// editable in one file the night before a demo, and what keeps a future
// second locale from becoming a rewrite.
//
// The renderer produces domain.Message, not a MAX request. The MAX adapter
// performs that last conversion, so the renderer has no idea MAX exists.
package render

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/domain"
)

// Renderer produces every message the bot sends.
//
// Implemented by *MessageRenderer. The interface exists so services can be
// tested against a fake and so an alternative renderer (a shorter variant for
// a demo, say) can be swapped in without touching the services.
type Renderer interface {
	// Notification renders an outbound notification of any supported type.
	Notification(n domain.Notification) (domain.Message, error)

	// ActionSucceeded renders the replacement message shown after the user's
	// action was accepted.
	ActionSucceeded(action domain.ActionType, event domain.Event) domain.Message
	// ActionFailed renders the replacement message for a failed action.
	ActionFailed(action domain.ActionType, err error) domain.Message
	// ActionNotice returns the short toast shown on the button itself.
	ActionNotice(action domain.ActionType, err error) string

	// Greeting is the bot_started message.
	Greeting(miniAppURL string) domain.Message
	// FreeText is the fallback reply to arbitrary user text.
	FreeText(miniAppURL string) domain.Message
	// Help is the /help reply.
	Help(miniAppURL string) domain.Message
}

// MessageRenderer is the Russian-language renderer.
type MessageRenderer struct {
	// now supplies the current time for "сегодня"/"завтра" decisions.
	now Clock
	// defaultMiniAppURL is used when a notification carries no per-event URL.
	defaultMiniAppURL string
	// openApp switches mini app buttons from plain links to open_app buttons,
	// which open the mini app inside MAX. Off by default: see WithOpenAppButtons.
	openApp bool
}

// Option customises a renderer.
type Option func(*MessageRenderer)

// WithClock overrides the time source.
func WithClock(now Clock) Option {
	return func(r *MessageRenderer) {
		if now != nil {
			r.now = now
		}
	}
}

// WithDefaultMiniAppURL sets the fallback mini app link.
func WithDefaultMiniAppURL(url string) Option {
	return func(r *MessageRenderer) { r.defaultMiniAppURL = strings.TrimSpace(url) }
}

// WithOpenAppButtons makes mini app buttons open the mini app inside MAX
// instead of a browser.
//
// A plain link opens the page in a browser, where the mini app gets no
// initData and cannot tell who the user is. An open_app button opens it
// inside MAX with initData, and passes the event id as the start parameter so
// the mini app lands on the right card.
//
// It is off by default because it only works once the mini app is registered
// for this bot in MAX. Until then MAX may reject every message that carries
// such a button, and no notification would get through.
func WithOpenAppButtons(enabled bool) Option {
	return func(r *MessageRenderer) { r.openApp = enabled }
}

// New builds a renderer.
func New(opts ...Option) *MessageRenderer {
	renderer := &MessageRenderer{now: time.Now}
	for _, opt := range opts {
		opt(renderer)
	}
	return renderer
}

// ErrUnsupportedType is returned for a notification type the renderer does not
// know. Validation catches this earlier; the check here is the safety net that
// turns a future unhandled case into an error instead of an empty message.
var ErrUnsupportedType = errors.New("render: unsupported notification type")

// Notification renders an outbound notification.
func (r *MessageRenderer) Notification(n domain.Notification) (domain.Message, error) {
	switch n.Type {
	case domain.NotificationRegistrationCreated:
		return r.registrationCreated(n)
	case domain.NotificationReminder24h:
		return r.reminder24h(n)
	case domain.NotificationConfirmationRequired, domain.NotificationConfirmationRetry:
		return r.confirmationRequired(n)
	case domain.NotificationReminder1h:
		return r.reminder1h(n)
	case domain.NotificationWaitlistOffer:
		return r.waitlistOffer(n)
	case domain.NotificationEventUpdated:
		return r.eventUpdated(n)
	case domain.NotificationEventCancelled:
		return r.eventCancelled(n)
	default:
		return domain.Message{}, fmt.Errorf("%w: %q", ErrUnsupportedType, n.Type)
	}
}

// registrationCreated: "✅ Вы записаны".
func (r *MessageRenderer) registrationCreated(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.RegistrationCreated)
	body.blank()
	body.line(n.Event.Title)
	body.line(formatDateTime(n.Event.StartsAt))
	body.lineIf(n.Event.Address)

	keyboard, err := buildKeyboard(
		r.eventButton(ru.Buttons.OpenEvent, n.Event.ID, r.miniAppURL(n)),
		callbackButton(ru.Buttons.CancelBooking, domain.ActionCancelRegistration, n),
	)
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// reminder24h: "⏰ Напоминание".
func (r *MessageRenderer) reminder24h(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.Reminder24h)
	body.blank()
	body.line(fmt.Sprintf("%s — «%s».",
		capitalise(formatDateTimeRelative(n.Event.StartsAt, r.now())),
		n.Event.Title,
	))
	body.lineIf(addressLine(n.Event.Address))

	keyboard, err := buildKeyboard(
		r.eventButton(ru.Buttons.OpenEvent, n.Event.ID, r.miniAppURL(n)),
		callbackButton(ru.Buttons.CannotAttend, domain.ActionCancelRegistration, n),
	)
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// confirmationRequired covers both the first ask and the retry. The retry uses
// the same copy on purpose: a user who ignored the first message is not helped
// by being told they ignored it.
func (r *MessageRenderer) confirmationRequired(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.ConfirmationRequired)
	body.blank()
	body.line(fmt.Sprintf("«%s» начинается %s.",
		n.Event.Title,
		formatDateTimeRelative(n.Event.StartsAt, r.now()),
	))

	keyboard, err := buildKeyboard(
		callbackButton(ru.Buttons.WillAttend, domain.ActionConfirmRegistration, n),
		callbackButton(ru.Buttons.CannotAttend, domain.ActionCancelRegistration, n),
	)
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// reminder1h: "📍 Скоро начало".
func (r *MessageRenderer) reminder1h(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.Reminder1h)
	body.blank()
	body.line(fmt.Sprintf("«%s» начинается через час.", n.Event.Title))
	body.lineIf(addressLine(n.Event.Address))

	// The "Маршрут" button is only meaningful when there is an address to
	// route to; otherwise the mini app link stands on its own.
	first := r.eventButton(ru.Buttons.OpenEvent, n.Event.ID, r.miniAppURL(n))
	if n.Event.Address != "" {
		first = linkButton(ru.Buttons.Route, routeURL(n.Event.Address))
	}

	keyboard, err := buildKeyboard(
		first,
		callbackButton(ru.Buttons.CannotAttend, domain.ActionCancelRegistration, n),
	)
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// waitlistOffer: "🔥 Освободилось место".
func (r *MessageRenderer) waitlistOffer(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.WaitlistOffer)
	body.blank()
	body.line(fmt.Sprintf("Для вас освободилось место на мероприятии «%s».", n.Event.Title))
	body.line(formatDateTime(n.Event.StartsAt))
	if n.OfferExpiresAt != nil {
		body.blank()
		body.line(fmt.Sprintf("%s %s.",
			ru.OfferValidUntil,
			formatDateTimeRelative(*n.OfferExpiresAt, r.now()),
		))
	}

	keyboard, err := buildKeyboard(
		callbackButton(ru.Buttons.TakeSeat, domain.ActionAcceptWaitlist, n),
		callbackButton(ru.Buttons.DeclineSeat, domain.ActionDeclineWaitlist, n),
	)
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// eventUpdated: "⚠️ Мероприятие изменено", with an itemised diff.
func (r *MessageRenderer) eventUpdated(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.EventUpdated)
	body.blank()
	body.line(fmt.Sprintf("«%s»", n.Event.Title))

	if len(n.Changes) > 0 {
		body.blank()
		body.line(ru.ChangesIntro)
		for _, change := range n.Changes {
			body.line("• " + formatChange(change))
		}
	}
	if n.OrganizerMessage != "" {
		body.blank()
		body.line(ru.OrganizerNote)
		body.line(n.OrganizerMessage)
	}

	body.blank()
	body.line(fmt.Sprintf("Начало: %s", formatDateTime(n.Event.StartsAt)))
	body.lineIf(addressLine(n.Event.Address))

	keyboard, err := buildKeyboard(r.eventButton(ru.Buttons.OpenEvent, n.Event.ID, r.miniAppURL(n)))
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// eventCancelled: "❌ Мероприятие отменено". No action buttons: there is
// nothing left to confirm or cancel.
func (r *MessageRenderer) eventCancelled(n domain.Notification) (domain.Message, error) {
	body := newBody(ru.Headings.EventCancelled)
	body.blank()
	body.line(fmt.Sprintf("«%s» было отменено организатором.", n.Event.Title))
	if n.OrganizerMessage != "" {
		body.blank()
		body.line(ru.OrganizerNote)
		body.line(n.OrganizerMessage)
	}

	keyboard, err := buildKeyboard(r.eventButton(ru.Buttons.OpenEvent, n.Event.ID, r.miniAppURL(n)))
	if err != nil {
		return domain.Message{}, err
	}
	return body.message(keyboard), nil
}

// ActionSucceeded renders the message that replaces the original one after a
// successful action.
//
// Crucially, the replacement carries no action buttons (only the mini app
// link). That is what stops a user from pressing "Приду" three more times and
// generating three more backend calls.
func (r *MessageRenderer) ActionSucceeded(action domain.ActionType, event domain.Event) domain.Message {
	var heading string
	switch action {
	case domain.ActionConfirmRegistration:
		heading = ru.Headings.Confirmed
	case domain.ActionCancelRegistration:
		heading = ru.Headings.Cancelled
	case domain.ActionAcceptWaitlist:
		heading = ru.Headings.SeatTaken
	case domain.ActionDeclineWaitlist:
		heading = ru.Headings.SeatDeclined
	default:
		heading = ru.Headings.AlreadyDone
	}

	body := newBody(heading)
	if event.Title != "" {
		body.blank()
		if action == domain.ActionAcceptWaitlist {
			body.line(fmt.Sprintf("Вы записаны на «%s».", event.Title))
		} else {
			body.line(event.Title)
		}
		if !event.StartsAt.IsZero() {
			body.line(formatDateTime(event.StartsAt))
		}
		body.lineIf(event.Address)
	}

	keyboard, err := buildKeyboard(r.eventButton(ru.Buttons.OpenMiniApp, event.ID, r.urlOrDefault(event.MiniAppURL)))
	if err != nil {
		// A bad URL must not cost the user their confirmation message; drop
		// the button and keep the text.
		keyboard = nil
	}
	return body.message(keyboard)
}

// ActionFailed renders the replacement message for a failed action.
//
// The user sees a plain explanation and a way forward, never a Go error
// string, an HTTP status or a stack trace.
func (r *MessageRenderer) ActionFailed(action domain.ActionType, err error) domain.Message {
	body := newBody(ru.Headings.Oops)
	body.blank()
	body.line(userFacingReason(err))
	body.blank()
	body.line("Открыть мини-приложение, чтобы проверить статус записи.")

	keyboard, kbErr := buildKeyboard(r.catalogButton(ru.Buttons.OpenMiniApp, r.defaultMiniAppURL))
	if kbErr != nil {
		keyboard = nil
	}
	return body.message(keyboard)
}

// ActionNotice returns the toast MAX shows on the button after a press.
func (r *MessageRenderer) ActionNotice(action domain.ActionType, err error) string {
	if err == nil {
		switch action {
		case domain.ActionConfirmRegistration:
			return ru.Notices.Confirmed
		case domain.ActionCancelRegistration:
			return ru.Notices.Cancelled
		case domain.ActionAcceptWaitlist:
			return ru.Notices.SeatTaken
		case domain.ActionDeclineWaitlist:
			return ru.Notices.SeatDeclined
		default:
			return ""
		}
	}

	switch core.CodeOf(err) {
	case core.CodeOfferExpired:
		return ru.Notices.Expired
	case core.CodeUnavailable:
		return ru.Notices.Unavailable
	case core.CodeNotFound, core.CodeEventCancelled, core.CodeRegistrationCancelled, core.CodeConflict, core.CodeForbidden:
		return ru.Notices.Conflict
	default:
		return ru.Notices.Unavailable
	}
}

// Greeting is the bot_started message.
//
// It deliberately does not advertise a catalogue, search or recommendations:
// the bot is a notification channel, and promising otherwise would send users
// looking for features that live in the mini app.
func (r *MessageRenderer) Greeting(miniAppURL string) domain.Message {
	body := newBody(ru.Greeting)
	keyboard, err := buildKeyboard(r.catalogButton(ru.Buttons.OpenMiniApp, r.urlOrDefault(miniAppURL)))
	if err != nil {
		keyboard = nil
	}
	return body.message(keyboard)
}

// FreeText is the fallback reply to arbitrary user text.
func (r *MessageRenderer) FreeText(miniAppURL string) domain.Message {
	body := newBody(ru.FreeTextReply)
	keyboard, err := buildKeyboard(r.catalogButton(ru.Buttons.OpenMiniApp, r.urlOrDefault(miniAppURL)))
	if err != nil {
		keyboard = nil
	}
	return body.message(keyboard)
}

// Help is the /help reply.
func (r *MessageRenderer) Help(miniAppURL string) domain.Message {
	body := newBody(ru.HelpReply)
	keyboard, err := buildKeyboard(r.catalogButton(ru.Buttons.OpenMiniApp, r.urlOrDefault(miniAppURL)))
	if err != nil {
		keyboard = nil
	}
	return body.message(keyboard)
}

// userFacingReason maps an error onto plain Russian.
func userFacingReason(err error) string {
	if err == nil {
		return ru.ErrConflict
	}
	if errors.Is(err, callback.ErrUnknownAction) ||
		errors.Is(err, callback.ErrUnsupportedVersion) ||
		errors.Is(err, callback.ErrMalformed) ||
		errors.Is(err, callback.ErrEmptyPayload) {
		return ru.ErrUnknownAction
	}

	switch core.CodeOf(err) {
	case core.CodeEventCancelled:
		return ru.ErrEventCancelled
	case core.CodeRegistrationCancelled:
		return ru.ErrRegistrationCancelled
	case core.CodeOfferExpired:
		return ru.ErrOfferExpired
	case core.CodeNotFound:
		return ru.ErrNotFound
	case core.CodeUnavailable:
		return ru.ErrUnavailable
	case core.CodeConflict, core.CodeForbidden:
		return ru.ErrConflict
	default:
		return ru.ErrUnavailable
	}
}

// miniAppURL picks the per-event link, falling back to the configured default.
// eventButton is the button that opens one event's card.
//
// With open_app enabled it opens the mini app inside MAX on that card. It
// falls back to a plain link when open_app is off, or when the event id is not
// something MAX accepts as a start parameter.
func (r *MessageRenderer) eventButton(text, eventID, url string) buttonSpec {
	if r.openApp {
		if spec, ok := openAppButton(text, strings.TrimSpace(eventID), url); ok {
			return spec
		}
	}
	return linkButton(text, url)
}

// catalogButton opens the mini app without a particular event.
func (r *MessageRenderer) catalogButton(text, url string) buttonSpec {
	if r.openApp {
		if spec, ok := openAppButton(text, "", url); ok {
			return spec
		}
	}
	return linkButton(text, url)
}

func (r *MessageRenderer) miniAppURL(n domain.Notification) string {
	return r.urlOrDefault(n.Event.MiniAppURL)
}

func (r *MessageRenderer) urlOrDefault(url string) string {
	if trimmed := strings.TrimSpace(url); trimmed != "" {
		return trimmed
	}
	return r.defaultMiniAppURL
}

// formatChange renders one organiser edit as a bullet line.
//
// An unrecognised field key is rendered using the key itself, so the Core
// Backend can introduce a new change kind without waiting for a bot release.
func formatChange(change domain.FieldChange) string {
	label := change.Field
	switch change.Field {
	case domain.ChangeFieldTime:
		label = ru.ChangeTime
	case domain.ChangeFieldAddress:
		label = ru.ChangeAddress
	case domain.ChangeFieldTitle:
		label = ru.ChangeTitle
	case domain.ChangeFieldOther, "":
		label = ru.ChangeOther
	}

	switch {
	case change.Old != "" && change.New != "":
		return fmt.Sprintf("%s: %s → %s", capitalise(label), change.Old, change.New)
	case change.New != "":
		return fmt.Sprintf("%s: %s", capitalise(label), change.New)
	case change.Note != "":
		return fmt.Sprintf("%s: %s", capitalise(label), change.Note)
	default:
		return capitalise(label)
	}
}

func addressLine(address string) string {
	if address == "" {
		return ""
	}
	return fmt.Sprintf("%s %s", ru.AddressPrefix, address)
}

// routeURL builds a maps link for an address.
//
// Yandex Maps is used because the audience is Russian-speaking; the address is
// query-escaped, so an address containing separators cannot break the link.
func routeURL(address string) string {
	return "https://yandex.ru/maps/?text=" + queryEscape(address)
}

// capitalise upper-cases the first rune, leaving the rest untouched.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}
