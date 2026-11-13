package httpapi

import (
	"context"
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
