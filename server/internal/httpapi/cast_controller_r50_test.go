package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"strings"
	"testing"
	"time"
)

// Source regression for Release's normal checkpoint: no execution in the
// per-item implementation loop. Real router/store/family checks, no auth mocks.
func TestReturningCastControllerRequiresOriginalAndCurrentBothFamilies(t *testing.T) {
	f := socialHTTP(t)
	var boot social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &boot)
	var target social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: boot.Bootstrap.Code, ExpectedBootstrapID: boot.Bootstrap.ID, DeviceID: "cast-r50"}), 200, &target)
	var registered social.CastControllerResponse
	proof := strings.Repeat("a", 48)
	f.decode(f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/register", social.CastControllerRegister{BootstrapID: boot.Bootstrap.ID, ControlToken: proof}), 200, &registered)
	validate := social.CastControllerValidation{Binding: registered.Binding, AttemptID: "returning-1"}
	var result social.CastControllerResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/controllers/validate", validate), 200, &result)
	check := social.CastControllerCheck{BindingID: result.Binding.ID, ControlToken: proof, AttemptID: result.AttemptID, Ticket: result.Ticket}
	f.decode(f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/check", check), 200, &social.CastControllerResponse{})
	// Neither another viewer nor the receiver may exercise sender validation.
	for _, bearer := range []string{f.member, target.Session.AccessToken} {
		if w := f.as(bearer, "POST", "/v1/cast/controllers/validate", validate); w.Code != 403 {
			t.Fatalf("foreign sender validation: %d", w.Code)
		}
	}
	changed := validate
	changed.Binding.TargetGeneration = "2"
	if w := f.as(f.token, "POST", "/v1/cast/controllers/validate", changed); w.Code != 403 {
		t.Fatalf("changed binding: %d", w.Code)
	}
	check.Ticket = strings.Repeat("b", 43)
	if w := f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/check", check); w.Code != 403 {
		t.Fatalf("invented ticket: %d", w.Code)
	}
	// A replacement receiver family/generation cannot recover the original proof.
	// authorization_access derives its expiry from the family token row, so
	// expiring that exact token is the whole fact; the projection is never written.
	expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := f.db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, expired, identity.Digest(target.Session.AccessToken)); err != nil {
		t.Fatal(err)
	}
	var renewed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: target.DeviceToken, DeviceID: "cast-r50"}), 200, &renewed)
	if w := f.as(f.token, "POST", "/v1/cast/controllers/validate", validate); w.Code != 403 {
		t.Fatalf("replaced target validation: %d", w.Code)
	}
	// The carried binding is checked under the successor, while old tickets
	// and the retired target bearer cannot be replayed.
	var carried social.CastControllerResponse
	f.decode(f.as(renewed.Session.AccessToken, "POST", "/v1/cast/controllers/check", social.CastControllerCheck{BindingID: registered.Binding.ID, ControlToken: proof}), 200, &carried)
	if carried.Binding.TargetFamilyID == registered.Binding.TargetFamilyID || carried.Binding.TargetGeneration == registered.Binding.TargetGeneration {
		t.Fatal("target binding did not rotate")
	}
	if w := f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/check", social.CastControllerCheck{BindingID: registered.Binding.ID, ControlToken: proof}); w.Code == 200 {
		t.Fatal("old bearer accepted")
	}
	oldTicket := social.CastControllerCheck{BindingID: registered.Binding.ID, ControlToken: proof, AttemptID: result.AttemptID, Ticket: result.Ticket}
	if w := f.as(renewed.Session.AccessToken, "POST", "/v1/cast/controllers/check", oldTicket); w.Code == 200 {
		t.Fatal("old ticket crossed rotation")
	}
	f.decode(f.as(f.token, "POST", "/v1/cast/controllers/validate", social.CastControllerValidation{Binding: carried.Binding, AttemptID: "renew-1"}), 200, &social.CastControllerResponse{})
	// Refusal leaves actual target state alone; no Stop/queue/media grant exists here.
	var state string
	if e := f.db.QueryRow(`SELECT state FROM social_cast_devices WHERE id=?`, target.Device.ID).Scan(&state); e != nil || state != "active" {
		t.Fatalf("refusal mutated target: %s %v", state, e)
	}
}

func TestCastRotationDoesNotCarryExpiredSender(t *testing.T) {
	f := socialHTTP(t)
	var boot social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &boot)
	var target social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: boot.Bootstrap.Code, ExpectedBootstrapID: boot.Bootstrap.ID, DeviceID: "sender-expired-tv"}), 200, &target)
	proof := strings.Repeat("a", 48)
	var bound social.CastControllerResponse
	f.decode(f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/register", social.CastControllerRegister{BootstrapID: boot.Bootstrap.ID, ControlToken: proof}), 200, &bound)
	expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := f.db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, expired, identity.Digest(f.token)); err != nil {
		t.Fatal(err)
	}
	var renewed social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: target.DeviceToken, DeviceID: "sender-expired-tv"}), 200, &renewed)
	if w := f.as(renewed.Session.AccessToken, "POST", "/v1/cast/controllers/check", social.CastControllerCheck{BindingID: bound.Binding.ID, ControlToken: proof}); w.Code == 200 {
		t.Fatal("expired sender relationship carried")
	}
}
