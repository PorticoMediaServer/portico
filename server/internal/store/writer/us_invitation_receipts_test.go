package writer

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestUSInvitationReceiptReplay(t *testing.T) {
	for _, kind := range []CommandID{usInvitationCreate, usInvitationEdit, usInvitationRevoke} {
		for _, fault := range []string{"", "digest", "missing", "extra", "memory", "deny", "empty-wire", "null-wire"} {
			t.Run(fmt.Sprint(kind, fault), func(t *testing.T) {
				w, _, input := usPolicyReceiptFixture(t)
				input.kind = kind
				q := usPolicyReceiptRequestFor(input)
				_, _, v := invitationResultFixture()
				v.kind, v.ownerID, v.issuerServer = kind, q.ownerID, input.request.issuer.server
				if kind != usInvitationCreate {
					v.serverID = ""
					v.revision = 2
					v.code = usInvitationProtectedCode{}
					if kind == usInvitationRevoke {
						v.state = "revoked"
					}
				} else {
					v.code.manifest = usInvitationCodeAAD(q.reference.Identity, v.ownerID, v.issuerServer, v.id, v.codeHash, v.code.keyID)
				}
				digest, e := usInvitationResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
				if e != nil {
					t.Fatal(e)
				}
				want := digest
				if fault == "digest" {
					digest[0] ^= 1
				}
				var key, nonce, body any
				if kind == usInvitationCreate {
					key, nonce, body = v.code.keyID, v.code.nonce, v.code.body
				}
				if fault == "empty-wire" {

					key, nonce, body = "", []byte{}, []byte{}
				}
				exec := func(s string, args ...any) {
					t.Helper()
					if _, e := w.db.ExecContext(t.Context(), s, args...); e != nil {
						t.Fatal(e)
					}
				}
				if fault == "null-wire" {
					if kind != usInvitationCreate {
						return
					}
					key, nonce, body = nil, nil, nil
				}
				// Deliberately corrupt only CHECK invariants to test decoder defenses.
				if fault == "empty-wire" || fault == "null-wire" {
					exec(`PRAGMA ignore_check_constraints=ON`)
				}
				codec, _ := usInvitationCodec(kind)
				exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body) VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?,?)`, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(kind), codec, v.appliedMS, digest[:], v.count, key, nonce, body)
				exec(`INSERT INTO security_invitation_results(owner_id,command_incarnation,command_sequence,invitation_id,server_id,notes,role,state,created_ms,expires_ms,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, v.id, v.serverID, v.notes, v.role, v.state, v.createdMS, v.expiresMS, v.revision, v.boundUsername, v.access.mode, v.access.streams, v.access.allowRemote, v.access.accessTokens, v.access.rating, v.codeHash)
				exec(`PRAGMA ignore_check_constraints=OFF`)
				libs := v.libraries
				if fault == "missing" {
					libs = libs[:1]
				}
				if fault == "extra" {
					libs = append(append([]string{}, libs...), "three")
				}
				for n, id := range libs {
					exec(`INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES(?,?,?,'invitation',?,?)`, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, n, id)
				}
				exec(`UPDATE security_command_owners SET last_sequence=1,retained_count=1,retained_variable_bytes=1000`)
				exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='target'`)
				exec(`DELETE FROM access_invitations`)
				defs := append(usPolicyDefinitions(), usPolicyReceiptDefinitions()...)
				defs = append(defs, usInvitationReceiptDefinitions()...)
				r := nativeInventoryFixtureRegistry(t, w, defs, "us_invitation_receipts_test.go")
				turn, a, cancel := usPolicyReceiptTurn(t, w, r, input)
				if fault == "memory" {
					a.memoryBytes = 1
					delete(turn.steps, usInvitationReceiptHeader)
					delete(turn.steps, usInvitationReceiptResult)
					delete(turn.steps, usInvitationReceiptLibraries)
				}
				if fault == "deny" {
					cancel()
					delete(turn.steps, usSecurityOwner)
					delete(turn.steps, usPolicyReceiptBinding)
				}
				var before, after int64
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); e != nil {
					t.Fatal(e)
				}
				out, err := lookupUSInvitation(t.Context(), turn, a, q)
				if fault == "" {
					if err != nil || out.decision != receiptReplay || out.digest != want || !reflect.DeepEqual(out.result, v) {
						t.Fatal(out, err)
					}
				} else {
					if err == nil || !reflect.DeepEqual(out, usInvitationReceiptLookup{}) {
						t.Fatal(out, err)
					}
					if fault == "memory" {
						var refusal *Refusal
						if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
							t.Fatal(err)
						}
					}
					if fault == "deny" && !errors.Is(err, ErrDisclosureDenied) {
						t.Fatal(err)
					}
				}
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); e != nil || before != after {
					t.Fatal(before, after, e)
				}
			})
		}
	}
}
