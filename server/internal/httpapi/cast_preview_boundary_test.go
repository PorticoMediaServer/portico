package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"strings"
	"testing"
	"time"
)

// The preview is only an offer. An incorrect expected bootstrap cannot replace
// the already accepted target, even when both codes address the same device.
func TestCastPreviewAndWrongAcceptanceLeaveActiveTargetUnchanged(t *testing.T) {
	f := socialHTTP(t)
	var first social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &first)
	var target social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: first.Bootstrap.Code, DeviceID: "cast-preview", ExpectedBootstrapID: first.Bootstrap.ID}), 200, &target)
	proof := strings.Repeat("a", 48)
	var registered social.CastControllerResponse
	f.decode(f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/register", social.CastControllerRegister{BootstrapID: first.Bootstrap.ID, ControlToken: proof}), 200, &registered)
	f.decode(f.as(f.token, "POST", "/v1/cast/controllers/validate", social.CastControllerValidation{Binding: registered.Binding, AttemptID: "offer-check"}), 200, &social.CastControllerResponse{})
	type snapshot struct {
		token, bootstrap, family, receiver, state, controller, targetFamily, ticket, attempt, receiverState string
		generation, ticketExpiry                                                                            int64
	}
	read := func() snapshot {
		var v snapshot
		if e := f.db.QueryRow(`SELECT d.token_digest,d.bootstrap_id,d.session_family_id,d.receiver_id,d.state,d.generation,c.id,c.target_family_id,c.ticket_digest,c.ticket_attempt,c.ticket_expires_ms,r.state FROM social_cast_devices d JOIN social_cast_controllers c ON c.target_id=d.id JOIN social_receivers r ON r.id=d.receiver_id WHERE d.id=?`, target.Device.ID).Scan(&v.token, &v.bootstrap, &v.family, &v.receiver, &v.state, &v.generation, &v.controller, &v.targetFamily, &v.ticket, &v.attempt, &v.ticketExpiry, &v.receiverState); e != nil {
			t.Fatal(e)
		}
		return v
	}
	before := read()
	var second social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &second)
	var offer social.CastPreviewResponse
	f.decode(f.as("", "POST", "/v1/cast/preview", social.CastPreviewRequest{ProtocolVersion: social.Protocol, Code: second.Bootstrap.Code, DeviceID: "cast-preview"}), 200, &offer)
	if offer.ControllerBootstrapID != second.Bootstrap.ID {
		t.Fatal("preview bootstrap mismatch")
	}
	if got := read(); got != before {
		t.Fatalf("preview changed active target: %+v", got)
	}
	wrong := social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: second.Bootstrap.Code, DeviceID: "cast-preview", ExpectedBootstrapID: first.Bootstrap.ID}
	if w := f.as("", "POST", "/v1/cast/redeem", wrong); w.Code == 200 {
		t.Fatal("wrong bootstrap accepted")
	}
	if got := read(); got != before {
		t.Fatalf("wrong acceptance changed active target: %+v", got)
	}
	wrong.ExpectedBootstrapID = second.Bootstrap.ID
	var replacement social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", wrong), 200, &replacement)
	if replacement.Device.Generation == target.Device.Generation || replacement.DeviceToken == target.DeviceToken {
		t.Fatal("acceptance did not replace target")
	}
}

func TestCastAcceptedReplacementDerivesFromNewPairingFamily(t *testing.T) {
	f := socialHTTP(t)
	second := f.deviceFor(f.owner.Viewer.AccountID, f.owner.Viewer.ProfileID, f.owner.Viewer.Role, "second-phone")
	for _, v := range []struct{ family, key string }{{f.owner.SessionFamilyID, "lineage-a"}, {second.SessionFamilyID, "lineage-b"}} {
		if _, err := f.db.Exec(`INSERT INTO portico_admitted_families(family_id,signer_key_id) VALUES(?,?)`, v.family, v.key); err != nil {
			t.Fatal(err)
		}
	}
	issue := func(token string) social.CastReceiverSession {
		var boot social.CastBootstrapResponse
		f.decode(f.as(token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &boot)
		var target social.CastReceiverSession
		f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: boot.Bootstrap.Code, ExpectedBootstrapID: boot.Bootstrap.ID, DeviceID: "lineage-tv"}), 200, &target)
		return target
	}
	first := issue(f.token)
	// Revoke A and its descendants before accepting B's independent offer.
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO hosted_signing_revocations(key_id) VALUES('lineage-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.RevokeSessionsAdmittedByRevokedKeysTx(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	replacement := issue(second.AccessToken)
	var signer string
	if err := f.db.QueryRow(`SELECT a.signer_key_id FROM portico_admitted_families a JOIN authorization_family_tokens t ON t.family_id=a.family_id WHERE t.token_hash=?`, identity.Digest(replacement.Session.AccessToken)).Scan(&signer); err != nil || signer != "lineage-b" {
		t.Fatalf("replacement lineage %q %v", signer, err)
	}
	var parentHorizon, childHorizon string
	if err := f.db.QueryRow(`SELECT p.authorization_horizon,c.authorization_horizon FROM authorization_session_families p JOIN authorization_family_tokens t ON t.token_hash=? JOIN authorization_session_families c ON c.id=t.family_id WHERE p.id=?`, identity.Digest(replacement.Session.AccessToken), second.SessionFamilyID).Scan(&parentHorizon, &childHorizon); err != nil || childHorizon > parentHorizon {
		t.Fatalf("child horizon %q exceeds parent %q: %v", childHorizon, parentHorizon, err)
	}
	if w := f.as(first.Session.AccessToken, "POST", "/v1/cast/controllers/check", social.CastControllerCheck{}); w.Code == 200 {
		t.Fatal("old target still accepted")
	}
}

func TestCastReconnectExpiredBearerAndRevokedParent(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			f := socialHTTP(t)
			var boot social.CastBootstrapResponse
			f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &boot)
			var target social.CastReceiverSession
			f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: boot.Bootstrap.Code, ExpectedBootstrapID: boot.Bootstrap.ID, DeviceID: "expired-tv"}), 200, &target)
			var family string
			if err := f.db.QueryRow(`SELECT family_id FROM authorization_family_tokens WHERE token_hash=?`, identity.Digest(target.Session.AccessToken)).Scan(&family); err != nil {
				t.Fatal(err)
			}
			expiry := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
			// Only the target's exact access token expires. authorization_access is
			// the read projection of that row, so writing the projection itself is
			// neither possible nor needed.
			if _, err := f.db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, expiry, identity.Digest(target.Session.AccessToken)); err != nil {
				t.Fatal(err)
			}
			if revoke {
				if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
					t.Fatal(err)
				}
			}
			w := f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: target.DeviceToken, DeviceID: "expired-tv"})
			if revoke && w.Code == 200 {
				t.Fatal("revoked parent renewed")
			}
			if !revoke && w.Code != 200 {
				t.Fatalf("valid expired bearer parent: %d %s", w.Code, w.Body.String())
			}
			if revoke {
				var current string
				if err := f.db.QueryRow(`SELECT session_family_id FROM social_cast_devices WHERE id=?`, target.Device.ID).Scan(&current); err != nil || current != family {
					t.Fatalf("refusal replaced parent %q %v", current, err)
				}
			}
		})
	}
}

func TestCastReconnectRefusalsPreserveAcceptedGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"access_revoked", "horizon", "device_missing", "generation", "signer_revoked", "account_disabled", "profile_deleted"} {
		t.Run(kind, func(t *testing.T) {
			f := socialHTTP(t)
			var boot social.CastBootstrapResponse
			f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &boot)
			var target social.CastReceiverSession
			f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: boot.Bootstrap.Code, ExpectedBootstrapID: boot.Bootstrap.ID, DeviceID: "graph-tv"}), 200, &target)
			proof := strings.Repeat("a", 48)
			var bound social.CastControllerResponse
			f.decode(f.as(target.Session.AccessToken, "POST", "/v1/cast/controllers/register", social.CastControllerRegister{BootstrapID: boot.Bootstrap.ID, ControlToken: proof}), 200, &bound)
			f.decode(f.as(f.token, "POST", "/v1/cast/controllers/validate", social.CastControllerValidation{Binding: bound.Binding, AttemptID: "graph-attempt"}), 200, &social.CastControllerResponse{})
			var family string
			if err := f.db.QueryRow(`SELECT session_family_id FROM social_cast_devices WHERE id=?`, target.Device.ID).Scan(&family); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "access_revoked":
				// An ended access token is the token row's own retirement; the
				// projection reports it and is never written directly.
				_, err = f.db.Exec(`UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=?`, identity.Digest(target.Session.AccessToken))
			case "horizon":
				_, err = f.db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), family)
			case "device_missing":
				_, err = f.db.Exec(`DELETE FROM identity_device_families WHERE family_id=?`, family)
			case "generation":
				_, err = f.db.Exec(`UPDATE authorization_session_families SET current_generation=current_generation+1 WHERE id=?`, family)
			case "account_disabled":
				_, err = f.db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id=?`, f.owner.Viewer.AccountID)
			case "profile_deleted":
				_, err = f.db.Exec(`UPDATE direct_profiles SET deleted=1 WHERE id=?`, f.owner.Viewer.ProfileID)
			case "signer_revoked":
				_, err = f.db.Exec(`INSERT INTO portico_admitted_families(family_id,signer_key_id) VALUES(?,'graph-key'); INSERT INTO hosted_signing_revocations(key_id) VALUES('graph-key')`, family)
			}
			if err != nil {
				t.Fatal(err)
			}
			type graph struct {
				token, bootstrap, family, controller, targetFamily, ticket, attempt string
				generation, ticketExpiry                                            int64
			}
			read := func() graph {
				var g graph
				err := f.db.QueryRow(`SELECT d.token_digest,d.bootstrap_id,d.session_family_id,d.generation,c.id,c.target_family_id,c.ticket_digest,c.ticket_attempt,c.ticket_expires_ms FROM social_cast_devices d JOIN social_cast_controllers c ON c.target_id=d.id WHERE d.id=?`, target.Device.ID).Scan(&g.token, &g.bootstrap, &g.family, &g.generation, &g.controller, &g.targetFamily, &g.ticket, &g.attempt, &g.ticketExpiry)
				if err != nil {
					t.Fatal(err)
				}
				return g
			}
			before := read()
			w := f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: target.DeviceToken, DeviceID: "graph-tv"})
			if w.Code == 200 {
				t.Fatal("invalid parent renewed")
			}
			if after := read(); after != before {
				t.Fatalf("refusal mutated accepted graph: %+v", after)
			}
		})
	}
}

func TestCastAcceptanceAndRenewalNeverCrossBootstrapLineage(t *testing.T) {
	f := socialHTTP(t)
	var a social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &a)
	var old social.CastReceiverSession
	f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: a.Bootstrap.Code, ExpectedBootstrapID: a.Bootstrap.ID, DeviceID: "race-tv"}), 200, &old)
	var b social.CastBootstrapResponse
	f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &b)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	go func() {
		<-start
		results <- f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: old.DeviceToken, DeviceID: "race-tv"})
	}()
	go func() {
		<-start
		results <- f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: b.Bootstrap.Code, ExpectedBootstrapID: b.Bootstrap.ID, DeviceID: "race-tv"})
	}()
	close(start)
	first, second := <-results, <-results
	if first.Code != 200 && second.Code != 200 {
		t.Fatalf("neither accepted: %d %d", first.Code, second.Code)
	}
	var bootstrap, family string
	var generation int64
	if err := f.db.QueryRow(`SELECT bootstrap_id,session_family_id,generation FROM social_cast_devices WHERE id=?`, old.Device.ID).Scan(&bootstrap, &family, &generation); err != nil {
		t.Fatal(err)
	}
	if bootstrap != b.Bootstrap.ID || family == "" || generation < 2 {
		t.Fatalf("acceptance lost or inherited wrong bootstrap: %q %q %d", bootstrap, family, generation)
	}
	if w := f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: old.DeviceToken, DeviceID: "race-tv"}); w.Code == 200 {
		t.Fatal("stale original token recovered accepted replacement")
	}
}

func TestCastRejectedNewSenderCannotReplaceAcceptedTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"expired_offer", "cancelled_offer", "revoked_sender"} {
		t.Run(kind, func(t *testing.T) {
			f := socialHTTP(t)
			second := f.deviceFor(f.owner.Viewer.AccountID, f.owner.Viewer.ProfileID, f.owner.Viewer.Role, "replacement-phone")
			var first social.CastBootstrapResponse
			f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &first)
			var accepted social.CastReceiverSession
			f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: first.Bootstrap.Code, ExpectedBootstrapID: first.Bootstrap.ID, DeviceID: "refused-tv"}), 200, &accepted)
			proof := strings.Repeat("a", 48)
			var bound social.CastControllerResponse
			f.decode(f.as(accepted.Session.AccessToken, "POST", "/v1/cast/controllers/register", social.CastControllerRegister{BootstrapID: first.Bootstrap.ID, ControlToken: proof}), 200, &bound)
			f.decode(f.as(f.token, "POST", "/v1/cast/controllers/validate", social.CastControllerValidation{Binding: bound.Binding, AttemptID: "preserve-ticket"}), 200, &social.CastControllerResponse{})
			var next social.CastBootstrapResponse
			f.decode(f.as(second.AccessToken, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, ReceiverID: "sdk-tv-1"}), 201, &next)
			var err error
			switch kind {
			case "expired_offer":
				_, err = f.db.Exec(`UPDATE social_cast_bootstraps SET expires_at_ms=0 WHERE id=?`, next.Bootstrap.ID)
			case "cancelled_offer":
				_, err = f.db.Exec(`UPDATE social_cast_bootstraps SET state='cancelled' WHERE id=?`, next.Bootstrap.ID)
			case "revoked_sender":
				_, err = f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, second.SessionFamilyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			type graph struct {
				token, bootstrap, family, controller, targetFamily, ticket, attempt, receiverState string
				generation, ticketExpiry                                                           int64
			}
			read := func() graph {
				var g graph
				err := f.db.QueryRow(`SELECT d.token_digest,d.bootstrap_id,d.session_family_id,d.generation,c.id,c.target_family_id,c.ticket_digest,c.ticket_attempt,c.ticket_expires_ms,r.state FROM social_cast_devices d JOIN social_cast_controllers c ON c.target_id=d.id JOIN social_receivers r ON r.id=d.receiver_id WHERE d.id=?`, accepted.Device.ID).Scan(&g.token, &g.bootstrap, &g.family, &g.generation, &g.controller, &g.targetFamily, &g.ticket, &g.attempt, &g.ticketExpiry, &g.receiverState)
				if err != nil {
					t.Fatal(err)
				}
				return g
			}
			before := read()
			for _, request := range []struct {
				path string
				body any
			}{{"/v1/cast/preview", social.CastPreviewRequest{ProtocolVersion: social.Protocol, Code: next.Bootstrap.Code, DeviceID: "refused-tv"}}, {"/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: next.Bootstrap.Code, ExpectedBootstrapID: next.Bootstrap.ID, DeviceID: "refused-tv"}}} {
				if w := f.as("", "POST", request.path, request.body); w.Code == 200 {
					t.Fatalf("%s accepted %s", request.path, kind)
				}
				if after := read(); after != before {
					t.Fatalf("%s changed accepted graph: %+v", request.path, after)
				}
			}
			f.decode(f.as(accepted.Session.AccessToken, "POST", "/v1/cast/controllers/check", social.CastControllerCheck{BindingID: bound.Binding.ID, ControlToken: proof}), 200, &social.CastControllerResponse{})
		})
	}
}
