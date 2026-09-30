package render

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/domain"
)

// Small helpers that keep the renderer methods readable: assembling lines and
// assembling keyboards are mechanical, and inlining them would bury the copy
// under string plumbing.

// body accumulates message lines.
type body struct {
	lines []string
}

func newBody(first string) *body {
	return &body{lines: []string{first}}
}

// line appends a line unconditionally.
func (b *body) line(text string) { b.lines = append(b.lines, text) }

// lineIf appends a line only when it is non-empty, so optional fields do not
// leave blank rows in the message.
func (b *body) lineIf(text string) {
	if strings.TrimSpace(text) != "" {
		b.lines = append(b.lines, text)
	}
}

// blank appends an empty line.
func (b *body) blank() { b.lines = append(b.lines, "") }

// message finalises the text and attaches the keyboard.
func (b *body) message(keyboard *domain.Keyboard) domain.Message {
	return domain.Message{
		Text:     strings.Join(b.lines, "\n"),
		Format:   domain.FormatPlain,
		Keyboard: keyboard,
		// Event messages carry a mini app link; the preview card adds noise
		// and pushes the buttons below the fold on small screens.
		DisableLinkPreview: true,
	}
}

// buttonSpec is a button that may have failed to build.
//
// Encoding a callback payload can fail (an id containing the separator, or an
// id long enough to blow the 1024-byte limit). Carrying the error alongside
// the button keeps the renderer methods free of error plumbing while still
// making the failure impossible to ignore.
type buttonSpec struct {
	button domain.Button
	err    error
	// skip marks a button that should simply be omitted, such as a link
	// button with no URL configured.
	skip bool
}

// linkButton builds a URL button, skipping it when no URL is available.
func linkButton(text, rawURL string) buttonSpec {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return buttonSpec{skip: true}
	}
	if parsed, err := url.Parse(trimmed); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		// A malformed link is dropped rather than sent: MAX rejects the whole
		// message on an invalid button, which would cost the user the entire
		// notification over a cosmetic element.
		return buttonSpec{skip: true}
	}
	return buttonSpec{button: domain.Button{Kind: domain.ButtonLink, Text: text, URL: trimmed}}
}

// appPayloadPattern is the start-parameter shape MAX accepts on an open_app
// button (schema.yaml, OpenAppButton.payload: ^[\w-]{0,512}$). Written out as
// ASCII so it does not depend on how a regex engine reads \w.
var appPayloadPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{0,512}$`)

// openAppButton builds a button that opens the bot's mini app inside MAX.
//
// payload becomes the mini app's start parameter; an empty one opens the
// catalog. fallbackURL is what the MAX adapter sends instead if it cannot
// build an open_app button (the bot's identity is not known yet); it may be
// empty, in which case the adapter drops the button.
//
// A payload MAX would reject makes the caller fall back to a plain link: MAX
// refuses the whole message over one invalid button, and losing the whole
// notification over a cosmetic element is the wrong trade.
func openAppButton(text, payload, fallbackURL string) (buttonSpec, bool) {
	if !appPayloadPattern.MatchString(payload) {
		return buttonSpec{}, false
	}
	fallback := linkButton(text, fallbackURL)
	button := domain.Button{Kind: domain.ButtonOpenApp, Text: text, Payload: payload}
	if !fallback.skip {
		button.URL = fallback.button.URL
	}
	return buttonSpec{button: button}, true
}

// callbackButton builds an action button with an encoded payload.
func callbackButton(text string, action domain.ActionType, n domain.Notification) buttonSpec {
	payload, err := callback.Encode(action, n.Registration.ID, n.Event.ID)
	if err != nil {
		return buttonSpec{err: fmt.Errorf("render button %q: %w", text, err)}
	}
	return buttonSpec{button: domain.Button{Kind: domain.ButtonCallback, Text: text, Payload: payload}}
}

// buildKeyboard assembles a single-row keyboard from the given specs.
//
// One row keeps the buttons side by side, which is how the mock-ups in the
// specification show them. Returns nil when every button was skipped, so an
// empty keyboard is never attached.
func buildKeyboard(specs ...buttonSpec) (*domain.Keyboard, error) {
	row := make(domain.ButtonRow, 0, len(specs))
	for _, spec := range specs {
		if spec.err != nil {
			return nil, spec.err
		}
		if spec.skip {
			continue
		}
		row = append(row, spec.button)
	}
	if len(row) == 0 {
		return nil, nil
	}
	return &domain.Keyboard{Rows: []domain.ButtonRow{row}}, nil
}

// queryEscape is url.QueryEscape, named locally so renderer.go does not need
// the net/url import for one call.
func queryEscape(s string) string { return url.QueryEscape(s) }
