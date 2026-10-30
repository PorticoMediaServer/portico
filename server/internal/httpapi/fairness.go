package httpapi

import (
	"net/http"
	"net/netip"
	"strings"
)

// Admission decides how much work the server will carry. It does not decide who
// gets to do it, and until now nothing did: a lane was a race, and the client
// that asked most often won most often. That is the whole of "one client can
// hurt another" — a phone stuck in a reconnect loop, a script paging a library
// as fast as the server answers, or a device on a bad link retrying every
// request, can hold most of a lane and leave everyone else queueing behind it.
// None of them is malicious and none of them is stopped by a capacity number.
//
// So each lane keeps a ledger of how many slots each client holds, and a client
// may not exceed its share of the lane. Two properties make this invisible to
// people using the server normally:
//
//   - It engages only when the lane is actually contended AND more than one
//     client is in it. A lone viewer may hold the entire lane however busy it
//     makes it, which is what keeps a quiet server as fast as it has ever been.
//   - The share is far above what any real client asks for. A browsing share is
//     six requests in flight at once from one device; an app issues two or three
//     while a page loads.
//
// A request over its share is not refused while the lane has room for it. It
// waits on the lane's own queue, for the lane's own queue-wait, and is then
// refused with exactly the 503 and Retry-After that a full lane already answers
// — a shape every client already handles.

const (
	// fairnessEngagesAbove is the fraction of a lane's capacity in use above
	// which shares apply, as a percentage. Three quarters leaves an idle or
	// lightly used server completely unrestricted.
	fairnessEngagesAbove = 75
	// fairnessShare is the fraction of a lane one client may hold once shares
	// apply, as a percentage. A quarter means four clients can saturate a lane
	// between them, which is the point: no single one can.
	fairnessShare = 25
	// fairnessFloor is the smallest share, so a narrow lane does not become a
	// one-request-per-client lane. The expensive lane is eight wide; a floor of
	// two keeps a person's search and the detail page behind it both moving.
	fairnessFloor = 2
)

// shareLimit is how many slots of this lane one client may hold.
func (l *lane) shareLimit() int {
	limit := l.spec.capacity * fairnessShare / 100
	if limit < fairnessFloor {
		limit = fairnessFloor
	}
	if limit > l.spec.capacity {
		limit = l.spec.capacity
	}
	return limit
}

// fairnessKey is the unauthenticated peer share. admission.clientKey replaces
// it only for a credential recorded after successful authentication. Merely
// presenting a different token, cookie or grant must not mint another share.
//
// The address is the exact address, not its network. Two hundred devices on one
// home LAN signing in after a restart are two hundred addresses in one /24, and
// grouping them would invent precisely the limit this work is forbidden to
// invent. Grouping belongs in the rate limiter, where the ceiling is counted per
// minute and can be set generously; here the ceiling is concurrency, and a
// household's concurrency is real.
//
// It is never the account: two people in one house are two clients, and a shared
// profile on two televisions is two clients too.
func fairnessKey(r *http.Request, trusted []netip.Prefix) string {
	return "a:" + clientAddress(r, trusted)
}

// presentedCredential is whatever the request offers as identity, without
// deciding whether it is valid. A grant in the path counts: media bodies carry
// no Authorization header, and a stream is exactly the thing one client must not
// be able to take all of.
func presentedCredential(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		if _, value, found := strings.Cut(header, " "); found && value != "" {
			return value
		}
		return header
	}
	if cookie, err := r.Cookie("portico_session"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	// /v1/media/{grant}/... and the other grant-addressed routes. The grant is
	// minted per session, so it identifies the stream rather than the account.
	if rest, found := strings.CutPrefix(r.URL.Path, "/v1/media/"); found {
		grant, _, _ := strings.Cut(rest, "/")
		if grant != "" {
			return grant
		}
	}
	return ""
}
