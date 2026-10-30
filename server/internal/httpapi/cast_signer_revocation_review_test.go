package httpapi

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"testing"
)

func TestRevokedSignerCannotRedeemOrReconnectACastTarget(t *testing.T) {
	for _, stage := range []string{"redeem", "reconnect"} {
		t.Run(stage, func(t *testing.T) {
			f := socialHTTP(t)
			if _, err := f.db.Exec(`INSERT INTO portico_admitted_families(family_id,signer_key_id) SELECT family_id,'key-a' FROM authorization_family_tokens WHERE token_hash=?`, identity.Digest(f.token)); err != nil {
				t.Fatal(err)
			}
			var bootstrap social.CastBootstrapResponse
			f.decode(f.as(f.token, "POST", "/v1/cast/bootstrap", social.CastBootstrapRequest{ProtocolVersion: social.Protocol, DisplayName: "Screen"}), 201, &bootstrap)
			var screen social.CastReceiverSession
			if stage == "reconnect" {
				f.decode(f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-revoked"}), 200, &screen)
			}
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.Exec(`INSERT INTO hosted_signing_revocations(key_id) VALUES('key-a')`); err != nil {
				t.Fatal(err)
			}
			if _, err = f.id.RevokeSessionsAdmittedByRevokedKeysTx(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var status int
			if stage == "redeem" {
				status = f.as("", "POST", "/v1/cast/redeem", social.CastRedeemRequest{ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-revoked"}).Code
			} else {
				status = f.as("", "POST", "/v1/cast/reconnect", social.CastReconnectRequest{ProtocolVersion: social.Protocol, DeviceToken: screen.DeviceToken, DeviceID: "cast-revoked"}).Code
			}
			if status != 403 {
				t.Fatalf("%s after signing-key revocation: %d, want 403", stage, status)
			}
		})
	}
}
