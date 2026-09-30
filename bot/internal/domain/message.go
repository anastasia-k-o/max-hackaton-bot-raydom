// Package domain contains the platform-neutral core model of the bot module.
//
// Nothing in this package imports transport, HTTP, or the MAX SDK. The types
// here describe *what* the bot wants to say and *what* a user did, not how it
// travels over the wire. Adapters (internal/maxapi, internal/transport/http)
// translate to and from these types.
package domain

// ButtonKind enumerates the platform-neutral button kinds the bot uses.
//
// The bot deliberately supports only the small subset it needs. MAX has more
// button types (request_contact, request_geo_location, clipboard, ...); adding
// one here is a conscious decision, not an accident of the SDK surface.
type ButtonKind string

const (
	// ButtonCallback posts Payload back to the bot when pressed.
	ButtonCallback ButtonKind = "callback"
	// ButtonLink opens URL in a browser.
	ButtonLink ButtonKind = "link"
	// ButtonOpenApp opens the MAX mini app bound to the bot, inside the
	// messenger. Unlike a link, the mini app then receives initData and can
	// authenticate the user; Payload is handed to it as the start parameter.
	ButtonOpenApp ButtonKind = "open_app"
)

// Button is one keyboard button.
type Button struct {
	Kind ButtonKind
	Text string
	// URL is used by ButtonLink. On ButtonOpenApp it is the fallback the
	// adapter uses when it cannot build an open_app button (see
	// internal/maxapi/real).
	URL string
	// Payload is used by ButtonCallback, where it carries an encoded action
	// (see internal/callback), never human-readable button text. On
	// ButtonOpenApp it is the start parameter for the mini app: the event id,
	// or empty to open the catalog.
	Payload string
}

// ButtonRow is a horizontal row of buttons.
type ButtonRow []Button

// Keyboard is an inline keyboard attached to a message.
type Keyboard struct {
	Rows []ButtonRow
}

// IsEmpty reports whether the keyboard carries no buttons at all.
func (k *Keyboard) IsEmpty() bool {
	if k == nil {
		return true
	}
	for _, row := range k.Rows {
		if len(row) > 0 {
			return false
		}
	}
	return true
}

// TextFormat is the markup applied to Message.Text.
type TextFormat string

const (
	// FormatPlain sends the text as-is with no markup parsing.
	FormatPlain TextFormat = ""
	// FormatMarkdown asks MAX to parse the text as Markdown.
	FormatMarkdown TextFormat = "markdown"
)

// Message is a rendered, platform-neutral outbound message.
//
// The MessageRenderer produces these; the MAX adapter converts them into
// whatever the MAX API expects. Swapping messenger would mean writing a new
// adapter, not rewriting the renderer.
type Message struct {
	Text     string
	Format   TextFormat
	Keyboard *Keyboard
	// DisableLinkPreview suppresses the link preview card for URLs in Text.
	DisableLinkPreview bool
}
