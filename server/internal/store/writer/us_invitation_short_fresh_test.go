package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
	"testing"
)

func TestUSInvitationShortFreshNative(t *testing.T) {
	for _, mode := range []string{"local", "portico", "absent", "max-revision"} {
		t.Run(mode, func(t *testing.T) {
			w, _, input := usInvitationSnapshotFixture(t)
			q := shortFixture()
			q.identity = input.identity
			q.scope = usInvitationScope{ownerID: input.request.ownerID, issuer: input.request.issuer, operationID:"short-operation"}
			q.invitationID = "us-invitation"
			q.codeHash = usInvitationFixtureHash
			now := w.now().UnixMilli()
			q.issuedMS = now - 500
			q.invitationExpiryMS = now + 1000
			if _, e := w.db.ExecContext(t.Context(), `UPDATE access_invitations SET expires_ms=? WHERE id=?`, q.invitationExpiryMS, q.invitationID); e != nil {
				t.Fatal(e)
			}
			if mode == "max-revision" {
				q.expectedRevision = math.MaxInt64
				q.invitationRevision = math.MaxInt64
				if _, e := w.db.ExecContext(t.Context(), `UPDATE access_invitations SET revision=? WHERE id=?`, q.invitationRevision, q.invitationID); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "portico" || mode == "absent" {
				q.signInKind = "portico"
			}
			if mode == "absent" {
				q.kindRevision = 0
			}
			if mode == "absent" {
				if _, e := w.db.ExecContext(t.Context(), `DELETE FROM identity_state`); e != nil {
					t.Fatal(e)
				}
			} else {
				if _, e := w.db.ExecContext(t.Context(), `INSERT INTO identity_state(singleton,sign_in_kind,revision,authority_generation) VALUES(1,?,1,1) ON CONFLICT(singleton) DO UPDATE SET sign_in_kind=excluded.sign_in_kind,revision=1`, q.signInKind); e != nil {
					t.Fatal(e)
				}
			}
			for _, s := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,'fixture')`, []any{q.serverID, q.publicKey, q.keyIncarnation}},
				{`INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,?)`, []any{q.serverID, q.publicKey, q.resetGeneration}},
			} {
				if _, e := w.db.ExecContext(t.Context(), s.sql, s.args...); e != nil {
					t.Fatal(e)
				}
			}
			defs := []stmt.Definition{}
			for _, d := range [][]stmt.Definition{usInvitationSourceDefinitions(), usInvitationLinkDefinitions(), usInvitationShortSourceDefinitions(), usInvitationShortEffectDefinitions(), usInvitationShortFreshDefinitions()} {
				defs = append(defs, d...)
			}
			r := nativeInventoryFixtureRegistry(t, w, defs, "us_invitation_snapshot.go", "us_invitation_link.go", "us_invitation_short_snapshot.go", "us_invitation_short_statements.go", "us_invitation_short_fresh.go")
			turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
			c := shortPrepare(t, q)
			for n := 0; n < 2; n++ {
				old, e := checkUSInvitationShortFresh(t.Context(), turn, a, c, now)
				if e == nil {
					e = applyUSInvitationShort(t.Context(), turn, a, c, old)
				}
				if e != nil {
					t.Fatalf("effect %d: %v", n, e)
				}
			}
			for _, mutate := range []func(*usInvitationShortRequest){func(v *usInvitationShortRequest) { v.role = "admin" }, func(v *usInvitationShortRequest) { v.invitationExpiryMS++ }, func(v *usInvitationShortRequest) { v.resetGeneration++ }, func(v *usInvitationShortRequest) { v.keyIncarnation = "other" }, func(v *usInvitationShortRequest) { v.kindRevision++ }, func(v *usInvitationShortRequest) { v.issuedMS = now + 1 }} {
				bad := q
				mutate(&bad)
				cmd := shortPrepare(t, bad)
				if _, e := checkUSInvitationShortFresh(t.Context(), turn, a, cmd, now); e == nil {
					t.Fatal("stale witness accepted")
				}
			}
		})
	}
}
