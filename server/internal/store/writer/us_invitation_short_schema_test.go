package writer

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Native purpose/format controls only. SQL does not prove AEAD authenticity or
// current issuance/disclosure authority; crypto/key proofs stay outside writer.
func TestUSShortInvitationConcreteResultSchema(t *testing.T) {
	for _, kind := range []string{"local", "portico"} {
		for _, invitationExpiry := range []int64{1750, 2000000} {
			t.Run(fmt.Sprintf("%s/%d", kind, invitationExpiry), func(t *testing.T) {
				w, _, input := usPolicyReceiptFixture(t)
				key := []any{input.request.ownerID, input.identity.Incarnation[:], input.identity.Sequence}
				const header = `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,?,?,?,?,553,1,'invitation_short_issue_v1',1000,zeroblob(32),0)`
				if _, err := w.db.ExecContext(t.Context(), header, append(append([]any(nil), key...), input.identity.OperationID[:], input.digest[:])...); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE security_command_receipts SET result_body=X'' WHERE owner_id=?`, key[0]); err == nil {
					t.Fatal("generic secret body accepted")
				}
				const insert = `INSERT INTO security_invitation_short_results(owner_id,command_incarnation,command_sequence,command_kind,invitation_id,invitation_revision,invitation_code_hash,invitation_role,invitation_expires_ms,server_id,server_public_key,server_key_incarnation,server_reset_generation,sign_in_kind,kind_revision,issued_ms,expires_ms,lookup_hash,binding_hash,result_key_id,result_nonce,result_body,result_aad_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
				expires := min(int64(601000), invitationExpiry)
				kindRevision := int64(4)
				if kind == "portico" {
					kindRevision = 0
				}
				v := append(append([]any(nil), key...), int64(553), "invite", int64(7), strings.Repeat("a", 64), "member", invitationExpiry, usLinkFixtureServer, make([]byte, 32), "key-life", int64(3), kind, kindRevision, int64(1000), expires, strings.Repeat("b", 64), strings.Repeat("c", 64), "payload-key", make([]byte, 12), make([]byte, 35), make([]byte, 32))
				for _, change := range []func([]any){
					func(v []any) { v[3] = int64(552) }, func(v []any) { v[5] = int64(0) }, func(v []any) { v[6] = strings.Repeat("A", 64) }, func(v []any) { v[7] = "owner" },
					func(v []any) { v[8] = int64(1000) }, func(v []any) { v[9] = "" }, func(v []any) { v[9] = usLinkFixtureServer[:46] + "F" }, func(v []any) { v[10] = nil }, func(v []any) { v[11] = "" }, func(v []any) { v[12] = int64(-1) },
					func(v []any) { v[13] = "hosted" }, func(v []any) { v[14] = int64(-1) }, func(v []any) { v[15] = int64(math.MaxInt64) }, func(v []any) { v[16] = expires - 1 },
					func(v []any) { v[13], v[14] = "local", int64(0) },
					func(v []any) { v[17] = strings.Repeat("B", 64) }, func(v []any) { v[18] = nil }, func(v []any) { v[19] = "" }, func(v []any) { v[20] = make([]byte, 11) }, func(v []any) { v[21] = make([]byte, 34) }, func(v []any) { v[21] = make([]byte, 36) }, func(v []any) { v[22] = nil },
				} {
					bad := append([]any(nil), v...)
					change(bad)
					if _, err := w.db.ExecContext(t.Context(), insert, bad...); err == nil {
						t.Fatal("invalid concrete purpose/clock/hash/wire accepted")
					}
				}
				if _, err := w.db.ExecContext(t.Context(), insert, v...); err != nil {
					t.Fatal("valid short result refused", err)
				}
				var bytes int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_invitation_short_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...).Scan(&bytes); err != nil {
					t.Fatal(err)
				}
				want := len(input.request.ownerID) + 16 + len("invite") + 64 + len("member") + 47 + 32 + len("key-life") + len(kind) + 64 + 64 + len("payload-key") + 12 + 35 + 32
				if bytes != int64(want) {
					t.Fatal("actual short wire/native bytes", bytes, want)
				}
				if _, err := w.db.ExecContext(t.Context(), `DELETE FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...); err == nil {
					t.Fatal("parent deleted before concrete child")
				}
				if _, err := w.db.ExecContext(t.Context(), `DELETE FROM security_invitation_short_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `DELETE FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,?,?,?,?,552,1,'invitation_revoke_v1',1000,zeroblob(32),0)`, append(append([]any(nil), key...), input.identity.OperationID[:], input.digest[:])...); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), insert, v...); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
					t.Fatal("short result accepted under another purpose", err)
				}
			})
		}
	}
}
