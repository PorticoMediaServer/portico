package httpapi

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
)

func castPair(t *testing.T, f *socialFixture, device string) social.CastReceiverSession {
	t.Helper()
	var bootstrap social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol}), 201, &bootstrap)
	var redeemed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: device}), 200, &redeemed)
	return redeemed
}

// SV-007: a refused code is charged to its source and to the server, in a
// transaction the refusal can't roll back; another source keeps its own budget.
func TestCastFailedAttemptsAreChargedDurably(t *testing.T) {
	f := socialHTTP(t)
	ctx := context.Background()
	store := f.d.Social()
	wrong := social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: "ZZZZZZ", ExpectedBootstrapID: "cbs_missing", DeviceID: "cast-guess", Source: "198.51.100.0/24"}
	for i := 0; i < social.CastFailuresPerSource; i++ {
		var fault *social.Fault
		if _, e := store.CastRedeem(ctx, wrong); !asFault(e, &fault) || fault.Code != "cast_code_not_found" {
			t.Fatalf("attempt %d: %v", i, e)
		}
	}
	// A fresh store (a restart) reads the same budget.
	var fault *social.Fault
	if _, e := f.d.Social().CastRedeem(ctx, wrong); !asFault(e, &fault) || fault.Code != "rate_limited" {
		t.Fatalf("the source's failures were not budgeted: %v", e)
	}
	other := wrong
	other.Source = "203.0.113.0/24"
	if _, e := store.CastRedeem(ctx, other); !asFault(e, &fault) || fault.Code != "cast_code_not_found" {
		t.Fatalf("one source's failures locked out another: %v", e)
	}
	var global int
	if e := f.d.DB.QueryRow(`SELECT count FROM social_rate WHERE bucket='cast-fail'`).Scan(&global); e != nil || global != social.CastFailuresPerSource+1 {
		t.Fatalf("server-wide failures %d %v", global, e)
	}
	// Success is not charged: a receiver reconnecting after every restart never
	// spends its own source's budget.
	paired := castPair(t, f, "cast-ok")
	token := paired.DeviceToken
	for i := 0; i < social.CastFailuresPerSource+2; i++ {
		out, e := store.CastReconnect(ctx, social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: token, Source: "192.0.2.0/24"})
		if e != nil {
			t.Fatalf("reconnect %d: %v", i, e)
		}
		token = out.DeviceToken
	}
}

// SV-009: a reconnect whose answer was lost recovers with the token it replaced,
// for the same device, once, within the window; the lost answer's session ends.
func TestCastLostReconnectRecovers(t *testing.T) {
	f := socialHTTP(t)
	paired := castPair(t, f, "cast-1")
	var lost social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: paired.DeviceToken}), 200, &lost)
	// The receiver never saw `lost`. Without its device id the old token is refused.
	w := f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: paired.DeviceToken})
	if w.Code != 404 {
		t.Fatalf("old token without device id %d %s", w.Code, w.Body.String())
	}
	if w = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: paired.DeviceToken, DeviceID: "cast-other"}); w.Code != 404 {
		t.Fatalf("old token for another device %d %s", w.Code, w.Body.String())
	}
	var recovered social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: paired.DeviceToken, DeviceID: "cast-1"}), 200, &recovered)
	if recovered.DeviceToken == lost.DeviceToken || recovered.Device.Generation != "3" {
		t.Fatalf("recovery did not rotate: %+v", recovered.Device)
	}
	// One live session per device: the lost answer's and the pairing's are gone.
	for name, bearer := range map[string]string{"lost": lost.Session.AccessToken, "paired": paired.Session.AccessToken} {
		if w = f.as(bearer, "GET", "/v1/me", nil); w.Code != 401 {
			t.Fatalf("%s session still works: %d", name, w.Code)
		}
	}
	if w = f.as(recovered.Session.AccessToken, "GET", "/v1/me", nil); w.Code != 200 {
		t.Fatalf("recovered session %d %s", w.Code, w.Body.String())
	}
	// The lost token is dead, and using the new token ends the old one's window.
	if w = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: lost.DeviceToken, DeviceID: "cast-1"}); w.Code != 404 {
		t.Fatalf("superseded lost token %d", w.Code)
	}
	var next social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: recovered.DeviceToken, DeviceID: "cast-1"}), 200, &next)
	if w = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: paired.DeviceToken, DeviceID: "cast-1"}); w.Code != 404 {
		t.Fatalf("pairing token outlived a used rotation %d", w.Code)
	}
	// Outside the window the replaced token is refused.
	if _, e := f.d.DB.Exec(`UPDATE social_cast_devices SET previous_valid_until_ms=? WHERE device_id='cast-1'`, time.Now().Add(-time.Second).UnixMilli()); e != nil {
		t.Fatal(e)
	}
	if w = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: recovered.DeviceToken, DeviceID: "cast-1"}); w.Code != 404 {
		t.Fatalf("replaced token outside the window %d", w.Code)
	}
	// Revoking the pairing ends the session it plays with.
	if w = f.as(f.token, "DELETE", "/v1/cast/devices/"+next.Device.ID, nil); w.Code != 204 {
		t.Fatalf("revoke %d", w.Code)
	}
	if w = f.as(next.Session.AccessToken, "GET", "/v1/receivers", nil); w.Code != 401 {
		t.Fatalf("revoked receiver's session still works: %d", w.Code)
	}
}

// SV-008: the code is spent only with a session. A viewer that can no longer be
// issued one keeps the code unspent, and a Hosted viewer this server can't
// verify is refused before a code exists.
func TestCastIssuanceFailureKeepsTheCode(t *testing.T) {
	f := socialHTTP(t)
	var bootstrap social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol}), 201, &bootstrap)
	if _, e := f.d.DB.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id=?`, f.owner.Viewer.AccountID); e != nil {
		t.Fatal(e)
	}
	w := f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-1"})
	if w.Code != 403 || errorCode(t, w) != "cast_access_ended" {
		t.Fatalf("stale viewer redeemed %d %s", w.Code, w.Body.String())
	}
	var state string
	if e := f.d.DB.QueryRow(`SELECT state FROM social_cast_bootstraps WHERE id=?`, bootstrap.Bootstrap.ID).Scan(&state); e != nil || state != "pending" {
		t.Fatalf("code spent without a session: %q %v", state, e)
	}
	var devices int
	if e := f.d.DB.QueryRow(`SELECT count(*) FROM social_cast_devices`).Scan(&devices); e != nil || devices != 0 {
		t.Fatalf("device stored without a session: %d %v", devices, e)
	}

	store := f.d.Social()
	hosted := identity.Principal{Viewer: identity.Viewer{AccountID: "hosted-account", ProfileID: "hosted-profile", ServerID: f.owner.Viewer.ServerID, Authority: "hosted", Role: "member"}, Epoch: 1}

	var fault *social.Fault
	if _, e := store.CastBootstrapStart(context.Background(), hosted, social.CastBootstrapRequest{ProtocolVersion: social.Protocol}); !asFault(e, &fault) || fault.Code != "cast_access_ended" {
		t.Fatalf("retired authority could start pairing: %v", e)
	}

}

// SV-012: the receiver bundle this server ships speaks this server's routes.
// Every API path literal in receiver.js is inventoried here with the method it
// is sent with, and each must be a registered route (401/400 without a
// credential, never 404/405).
func TestCastReceiverCallsRegisteredRoutes(t *testing.T) {
	f := socialHTTP(t)
	w := f.as("", "GET", "/receiver/cast/receiver.js", nil)
	if w.Code != 200 {
		t.Fatalf("receiver bundle %d", w.Code)
	}
	source := w.Body.String()
	calls := map[string][]string{
		"/v1/cast/preview":                    {"POST"},
		"/v1/cast/controllers/check":          {"POST"},
		"/v1/cast/controllers/register":       {"POST"},
		"/v1/cast/redeem":                     {"POST"},
		"/v1/cast/reconnect":                  {"POST"},
		"/v1/me/devices/current/capabilities": {"PUT"},
		"/v1/playback/sessions":               {"POST"},
		"/v1/playback/sessions/":              {"PATCH", "DELETE"},
		"/v1/items/":                          {"GET"},
		"/v1/queues/":                         {"GET"},
		"/v1/user-mutations/":                 {"GET"},
		"/v1/user-mutations/policy":           {"GET"},
		// A pairing the television took and then could not use: it undoes its own.
		"/v1/cast/devices/": {"DELETE"},
	}
	suffixed := map[string][][2]string{"/v1/playback/sessions/": {{"POST", "/observer"}, {"PUT", "/observation"}}, "/v1/queues/": {{"POST", ":advance"}}}
	for _, m := range regexp.MustCompile(`'(/v\d+/[^'?]*)`).FindAllStringSubmatch(source, -1) {
		if _, ok := calls[m[1]]; !ok && m[1] != "/observer" && m[1] != "/observation" {
			t.Errorf("receiver.js calls %s, which this test doesn't inventory", m[1])
		}
	}
	probe := func(method, path string) {
		t.Helper()
		w := f.as("", method, path, map[string]any{})
		if w.Code == 404 || w.Code == 405 {
			t.Errorf("%s %s is not a registered route: %d %s", method, path, w.Code, w.Body.String())
		}
	}
	for path, methods := range calls {
		if !strings.Contains(source, "'"+path) {
			t.Errorf("receiver.js no longer calls %s", path)
		}
		for _, method := range methods {
			// Pairing and re-pairing carry no session. Everything else the
			// receiver sends, it sends with a paired screen's session, which
			// may make only the requests castTargetRoute lists.
			// Item identity and queue completion need the actual current
			// occurrence, not a blanket path grant. Their credential-bound
			// checks live in TestCastReceiverCurrentOccurrenceRoutes.
			if path != "/v1/cast/preview" && path != "/v1/cast/redeem" && path != "/v1/cast/reconnect" && path != "/v1/items/" && path != "/v1/queues/" {
				target := path
				if strings.HasSuffix(path, "/") {
					target += "ps_probe"
				}
				if !castTargetRoute(method, target) {
					t.Errorf("receiver.js sends %s %s, which a paired screen's session may not", method, target)
				}
				for _, route := range suffixed[path] {
					if !castTargetRoute(route[0], target+route[1]) {
						t.Errorf("receiver.js sends %s %s, which a paired screen's session may not", route[0], target+route[1])
					}
				}
			}
			if strings.HasSuffix(path, "/") {
				probe(method, path+"ps_probe")
				for _, route := range suffixed[path] {
					probe(route[0], path+"ps_probe"+route[1])
				}
				continue
			}
			probe(method, path)
		}
	}
}
