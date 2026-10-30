package httpapi

import "strings"

// castTargetRoute says whether a request is one a screen paired as a Cast
// target may make. The list is everything the receiver page does once it is
// paired, and nothing else:
//
//   - the playback session API: start, control, report, renew, events, and
//     saying what the screen itself can play;
//   - the media a playback session hands it;
//   - the artwork for the title it is showing;
//   - reading who it is, signing itself out, and undoing its own pairing.
//
// Everything else is refused for such a session (identity.Principal.
// PermitsRequest), by default: a route added tomorrow is closed to a paired
// screen unless it is added here on purpose. Pairing and re-pairing
// (/v1/cast/redeem, /v1/cast/reconnect) carry no session at all and are not
// part of this.
func castTargetRoute(method, path string) bool {
	if castOccurrenceRoute(method, path) {
		return true
	} // Always followed by live occurrence authority.
	read := method == "GET" || method == "HEAD"
	switch {
	case strings.HasPrefix(path, "/v1/user-mutations/"):
		// The receipt handler also requires this exact family and a Stop
		// operation; this grants no access to Saved or Settings receipts.
		return read && !strings.Contains(strings.TrimPrefix(path, "/v1/user-mutations/"), "/")
	case path == "/v1/playback" || strings.HasPrefix(path, "/v1/playback/"):
		return true
	case strings.HasPrefix(path, "/v1/media/"):
		return read
	case strings.HasPrefix(path, "/v1/items/"):
		// A title's artwork, and what belongs to a playback session of it
		// (its subtitle plan). Not the title's detail, nor anything else of it.
		return read && (strings.Contains(path, "/art/") || strings.HasSuffix(path, "/image") || strings.Contains(path, "/playback/"))
	case strings.HasPrefix(path, "/v1/metadata/"):
		return read && strings.Contains(path, "/art/")
	case path == "/v1/me/devices/current/capabilities":
		// What this screen can play: the playback API needs it before a start.
		return true
	case path == "/v1/cast/controllers/register" || path == "/v1/cast/controllers/check":
		// Exact receiver family/generation and original controller checks in store.
		return method == "POST"
	case path == "/v1/me":
		return read
	case path == "/v1/sessions/current":
		return method == "DELETE"
	case strings.HasPrefix(path, "/v1/cast/devices/"):
		// Undoing a pairing. The store lets a screen undo its own and no
		// other (social.RevokeCastDevice).
		return method == "DELETE" && !strings.Contains(strings.TrimPrefix(path, "/v1/cast/devices/"), "/")
	}
	return false
}
