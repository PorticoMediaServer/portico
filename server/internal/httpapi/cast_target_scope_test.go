package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
)

func pairCastTarget(t *testing.T, f *socialFixture, bearer, device string) social.CastReceiverSession {
	t.Helper()
	var bootstrap social.CastBootstrapResponse
	f.decode(f.as(bearer, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, DisplayName: "Living room TV"}), 201, &bootstrap)
	var redeemed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: device, DisplayName: "Living room TV"}), 200, &redeemed)
	if redeemed.Session.AccessToken == "" {
		t.Fatal("pairing issued no session")
	}
	return redeemed
}

// The owner pairs a television as a Cast target. The television can play, and
// that is all: it is not the owner, it cannot look around the library, manage
// the account or the server, approve or pair another device, or become
// another profile.
func TestACastTargetPairedByTheOwnerCanOnlyPlay(t *testing.T) {
	f := socialHTTP(t)
	f.d.Access = AccessArea{Access: access.New(f.db)}
	f.h = New(f.d)
	screen := pairCastTarget(t, f, f.token, "cast-shared-room")
	bearer := screen.Session.AccessToken

	// What it is: a member's session with the Cast target grant, whoever paired it.
	var role, grant string
	if err := f.db.QueryRow(`SELECT f.role,COALESCE((SELECT g.grant_kind FROM authorization_family_grants g WHERE g.family_id=f.id),'') FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id WHERE t.token_hash=?`, identity.Digest(bearer)).Scan(&role, &grant); err != nil || role != identity.TierMember || grant != identity.GrantCastTarget {
		t.Fatalf("the television's session: role=%q grant=%q %v", role, grant, err)
	}
	var refresh int
	if err := f.db.QueryRow(`SELECT count(*) FROM identity_refresh_credentials c JOIN authorization_family_tokens t ON t.family_id=c.family_id WHERE t.token_hash=?`, identity.Digest(bearer)).Scan(&refresh); err != nil || refresh != 0 {
		t.Fatalf("the television's session holds %d renewal credentials it was never given: %v", refresh, err)
	}

	// What it may do.
	if w := f.as(bearer, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("reading who it is: %d %s", w.Code, w.Body.String())
	}
	if w := f.as(bearer, "GET", "/v1/items/"+f.item, nil); w.Code != 403 {
		t.Fatalf("screen without adopted playback read item: %d", w.Code)
	}
	if id := f.play(bearer); id == "" {
		t.Fatal("the television could not start playback")
	}

	// What it may not. Each is a refusal of a valid session (403), so the
	// television is not signed out by asking.
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/libraries", nil},
		{"GET", "/v1/items?libraryId=" + f.library, nil},
		{"GET", "/v1/search?q=film", nil},
		{"GET", "/v1/home", nil},
		{"GET", "/v1/receivers", nil},
		{"GET", "/v1/devices", nil},
		{"GET", "/v1/direct", nil},
		{"GET", "/v1/direct/members", nil},
		{"GET", "/v1/direct/sessions", nil},
		{"POST", "/v1/direct/profiles", map[string]string{"name": "Mine", "art": "blue"}},
		{"POST", "/v1/direct/profiles/" + f.profile + "/select", map[string]string{}},
		{"POST", "/v1/direct/password", map[string]string{"currentPassword": "long-test-password", "newPassword": "another-long-password"}},
		{"GET", "/v1/admin/access/members", nil},
		{"GET", "/v1/admin/access/devices", nil},
		{"POST", "/v1/admin/access/invitations", map[string]any{"operationId": "cast-invite-0001", "role": "member"}},
		{"GET", "/v1/admin/connectivity/policy", nil},
		{"POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol}},
		{"POST", "/v1/quick-connect/review", map[string]string{"userCode": "2345-6789"}},
		{"POST", "/v1/quick-connect/decision", map[string]string{"requestId": "x", "userCode": "2345-6789", "decision": "approve"}},
		{"DELETE", "/v1/direct/members/account2", map[string]any{"expectedRevision": 1}},
		{"GET", "/v1/me/access-tokens", nil},
		{"POST", "/v1/me/access-tokens", map[string]any{"name": "from the television", "scopes": []string{"read"}}},
		{"POST", "/v1/admin/api-keys", map[string]any{"name": "from the television", "scopes": []string{"admin"}}},
	} {
		w := f.as(bearer, probe.method, probe.path, probe.body)
		if w.Code != 403 {
			t.Errorf("a paired screen asking %s %s: %d %s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}
	// It is still signed in after all those refusals, and the owner's own
	// session, which paired it, is untouched and still the owner's.
	if w := f.as(bearer, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("the television was signed out by being refused: %d", w.Code)
	}
	if w := f.as(f.token, "GET", "/v1/admin/access/members", nil); w.Code != 200 {
		t.Fatalf("the owner's own session: %d %s", w.Code, w.Body.String())
	}
	// A reconnect issues the same kind of session, not a wider one.
	var renewed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: screen.DeviceToken}), 200, &renewed)
	if w := f.as(renewed.Session.AccessToken, "GET", "/v1/libraries", nil); w.Code != 403 {
		t.Fatalf("a reconnected screen reached beyond playback: %d %s", w.Code, w.Body.String())
	}
	if w := f.as(renewed.Session.AccessToken, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("a reconnected screen: %d %s", w.Code, w.Body.String())
	}
}

// A paired screen can undo its own pairing, at the generation it holds, and
// nothing else of the profile that paired it: not another of its screens.
func TestACastTargetUndoesOnlyItsOwnPairing(t *testing.T) {
	f := socialHTTP(t)
	first := pairCastTarget(t, f, f.token, "cast-kitchen")
	second := pairCastTarget(t, f, f.token, "cast-bedroom")
	undo := func(bearer, device, generation string) int {
		headers := map[string]string{}
		if generation != "" {
			headers["If-Match"] = generation
		}
		r := httptest.NewRequest("DELETE", "/v1/cast/devices/"+device, nil)
		r.Header.Set("Authorization", "Bearer "+bearer)
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		return w.Code
	}
	if code := undo(first.Session.AccessToken, second.Device.ID, second.Device.Generation); code != 404 {
		t.Fatalf("one screen undid another's pairing: %d", code)
	}
	if code := undo(first.Session.AccessToken, first.Device.ID, ""); code != 400 {
		t.Fatalf("a screen undid its pairing without naming its generation: %d", code)
	}
	if code := undo(first.Session.AccessToken, first.Device.ID, "999"); code != 409 {
		t.Fatalf("a screen undid its pairing at a generation it does not hold: %d", code)
	}
	for name, bearer := range map[string]string{"first": first.Session.AccessToken, "second": second.Session.AccessToken} {
		if w := f.as(bearer, "GET", "/v1/me", nil); w.Code != 200 {
			t.Fatalf("the %s screen lost its session to a refused request: %d", name, w.Code)
		}
	}
	if code := undo(first.Session.AccessToken, first.Device.ID, first.Device.Generation); code != 204 {
		t.Fatalf("a screen could not undo its own pairing: %d", code)
	}
	if w := f.as(first.Session.AccessToken, "GET", "/v1/me", nil); w.Code != 401 {
		t.Fatalf("an unpaired screen is still signed in: %d", w.Code)
	}
	if w := f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: first.DeviceToken, DeviceID: "cast-kitchen"}); w.Code == 200 {
		t.Fatal("an unpaired screen reconnected")
	}
	if w := f.as(second.Session.AccessToken, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("the other screen was unpaired too: %d", w.Code)
	}
	// The profile that paired them still unpairs any of its screens.
	if w := f.as(f.token, "DELETE", "/v1/cast/devices/"+second.Device.ID, nil); w.Code != 204 {
		t.Fatalf("the pairing profile could not unpair its screen: %d %s", w.Code, w.Body.String())
	}
}

// The route test itself: what counts as playback for a paired screen.
func TestCastTargetRoutes(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/v1/playback/sessions", true},
		{"PATCH", "/v1/playback/sessions/abc", true},
		{"DELETE", "/v1/playback/sessions/abc", true},
		{"POST", "/v1/playback/sessions/abc/observer", true},
		{"PUT", "/v1/playback/sessions/abc/observation", true},
		{"PUT", "/v1/playback/client-profile", true},
		{"POST", "/v1/playback/route-failures", true},
		{"GET", "/v1/media/grant/segment.ts", true},
		{"HEAD", "/v1/media/grant/audio", true},
		{"POST", "/v1/media/grant", false},
		{"GET", "/v1/items/abc/art/poster", true},
		{"GET", "/v1/items/abc/chapters/3/image", true},
		{"GET", "/v1/items/abc/playback/session/subtitles", true},
		{"GET", "/v1/items/abc", true},
		{"GET", "/v1/items", false},
		{"PUT", "/v1/items/abc/art/poster", false},
		{"DELETE", "/v1/items/abc", false},
		{"GET", "/v1/metadata/show/abc/art/poster", true},
		{"PUT", "/v1/metadata/show/abc", false},
		{"GET", "/v1/me", true},
		{"PUT", "/v1/me/devices/current/capabilities", true},
		{"GET", "/v1/me/devices", false},
		{"PUT", "/v1/me/preferences", false},
		{"DELETE", "/v1/sessions/current", true},
		{"GET", "/v1/sessions/current", false},
		{"GET", "/v1/playbackx", false},
		{"GET", "/v1/libraries", false},
		{"GET", "/v1/admin/access/members", false},
		{"POST", "/v1/cast/bootstrap", false},
		{"DELETE", "/v1/cast/devices/abc", true},
		{"GET", "/v1/cast/devices/abc", false},
		{"DELETE", "/v1/cast/devices/abc/anything", false},
		{"DELETE", "/v1/cast/devices", false},
		{"POST", "/v1/me/access-tokens", false},
		{"POST", "/v1/admin/api-keys", false},
		{"POST", "/v1/quick-connect/decision", false},
		{"GET", "/v1/direct/me", false},
	} {
		if got := castTargetRoute(c.method, c.path); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// A second profile on the owner's account is a member. It used to be let
// through every administrator route, because the route asked what tier the
// account holds and not what tier the session holds.
func TestASecondProfileOfTheOwnersAccountIsNotAnAdministrator(t *testing.T) {
	f := socialHTTP(t)
	f.d.Access = AccessArea{Access: access.New(f.db)}
	f.h = New(f.d)
	account := f.owner.Viewer.AccountID
	if _, err := f.db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary) VALUES('kid-profile',?,'Kid',0)`, account); err != nil {
		t.Fatal(err)
	}
	kid := f.deviceFor(account, "kid-profile", identity.TierMember, "kid-tablet")
	if w := f.as(kid.AccessToken, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("the second profile's session: %d %s", w.Code, w.Body.String())
	}
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/admin/access/members", nil},
		{"GET", "/v1/admin/access/invitations", nil},
		{"GET", "/v1/admin/access/devices", nil},
		{"POST", "/v1/admin/access/invitations", map[string]any{"operationId": "kid-invite-0001", "role": "admin"}},
		{"PUT", "/v1/admin/access/members/account2/role", map[string]any{"expectedRevision": 1, "operationId": "kid-role-0001", "role": "admin"}},
		{"PUT", "/v1/admin/access/members/account2/limits", map[string]any{"expectedRevision": 1, "operationId": "kid-limits-0001", "limits": map[string]any{}}},
	} {
		if w := f.as(kid.AccessToken, probe.method, probe.path, probe.body); w.Code != 403 {
			t.Errorf("a second profile of the owner's account asking %s %s: %d %s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}
	var role string
	if err := f.db.QueryRow(`SELECT role FROM direct_memberships WHERE account_id='account2'`).Scan(&role); err != nil || role != identity.TierMember {
		t.Fatalf("a member was changed by a second profile: %q %v", role, err)
	}
	// The owner's primary profile still administers.
	if w := f.as(f.token, "GET", "/v1/admin/access/members", nil); w.Code != 200 {
		t.Fatalf("the owner: %d %s", w.Code, w.Body.String())
	}
}
