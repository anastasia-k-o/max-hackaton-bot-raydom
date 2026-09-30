package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"hackatonCore/internal/domain"
	"hackatonCore/internal/store"
)

// Two clients call the action endpoints, and they are trusted differently
// (bot/docs/INTEGRATION_MINIAPP.md §6.1):
//
//   - the bot authenticates with X-Api-Key and speaks for the max_user_id in
//     the body — it took that id from MAX's own event, not from the button;
//   - the mini app authenticates with a session, and the body's max_user_id
//     proves nothing: it must match the session or the request is refused.

var (
	errUnauthorized = domain.Errorf(http.StatusUnauthorized, "unauthorized", "missing or invalid credentials")
	errBadBotKey    = domain.Errorf(http.StatusUnauthorized, "unauthorized", "X-Api-Key does not match BOT_API_KEY")
)

// fromBot reports whether the request carries the bot's key. A request that
// carries a wrong key is an error, not an anonymous request.
func (a *api) fromBot(r *http.Request) (bool, error) {
	key := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	if key == "" {
		return false, nil
	}
	if a.BotAPIKey == "" {
		// Dev mode without a key configured: accept the bot as it is, so a
		// missing CORE_API_KEY does not block a first local run.
		if a.DevMode {
			return true, nil
		}
		return false, errBadBotKey
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(a.BotAPIKey)) != 1 {
		return false, errBadBotKey
	}
	return true, nil
}

// sessionUser resolves the Authorization: Bearer token.
func (a *api) sessionUser(r *http.Request) (domain.User, error) {
	auth := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		return domain.User{}, errUnauthorized
	}
	u, err := a.Service.UserBySession(r.Context(), strings.TrimSpace(token))
	if errors.Is(err, store.ErrNotFound) {
		return u, errUnauthorized
	}
	return u, err
}

// actor finds who an action is performed for.
func (a *api) actor(r *http.Request, bodyMaxUserID int64) (domain.User, error) {
	bot, err := a.fromBot(r)
	if err != nil {
		return domain.User{}, err
	}
	if bot {
		if bodyMaxUserID <= 0 {
			return domain.User{}, domain.Invalid([]domain.FieldError{{Field: "max_user_id", Message: "required for requests from the bot"}})
		}
		// A MAX user the backend has never seen cannot own a registration.
		return a.Service.UserByMaxID(r.Context(), bodyMaxUserID)
	}
	u, err := a.sessionUser(r)
	if err != nil {
		return u, err
	}
	if bodyMaxUserID != 0 && (u.MaxUserID == nil || *u.MaxUserID != bodyMaxUserID) {
		return u, domain.Errorf(http.StatusForbidden, "forbidden", "max_user_id in the body does not match the session")
	}
	return u, nil
}
