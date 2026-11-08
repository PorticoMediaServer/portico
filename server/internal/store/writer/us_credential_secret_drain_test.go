package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func usSecretDrainFixture(t *testing.T, kind string) (*executor, usCredentialSecretDrainScope, int64) {
	t.Helper()
	w := wc008NativeExecutor(t)
	s := usCredentialSecretDrainScope{credential: "credential", kind: kind, account: "account", profile: "profile", member: strings.Repeat("a", 32), lifetime: strings.Repeat("b", 32), family: "family", server: "server", secretHash: usCredentialFixtureHash("secret-drain-key"), familyIncarnation: [16]byte{1}, familyGeneration: 1}
	wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'','profile')`)
	wc008NativeExec(t, w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked,revoked_reason,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES('family','server','account','profile','local','member',1,'horizon',1,1,'explicit_signout',?,?,?,1)`, s.member, s.lifetime, s.familyIncarnation[:])
	for _, id := range []string{"credential", "other"} {
		family := s.family
		hash := s.secretHash
		if id == "other" {
			family = "unrelated-family"
			hash = usCredentialFixtureHash("other-drain-key")
		}
		wc008NativeExec(t, w, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,revoked_ms) VALUES(?,?,'account','profile','Key','[]',?,'hint',?,'device',0,1)`, id, kind, hash, family)
		length := 62
		if kind == "app_password" {
			length = 64
		}
		wc008NativeExec(t, w, `INSERT INTO security_credential_secrets(credential_id,kind,account_id,member_incarnation,profile_id,profile_lifetime,family_id,family_incarnation,secret_hash,key_id,nonce,body,aad_digest) VALUES(?,?,'account',?,'profile',?,?,?,?, 'key',zeroblob(12),zeroblob(?),zeroblob(32))`, id, kind, s.member, s.lifetime, family, s.familyIncarnation[:], hash, length)
	}

	ownerInc := [16]byte{9}
	operation := [16]byte{8}
	hint := "abcdef"
	length := 62
	if kind == "app_password" {
		hint = ""
		length = 64
	}
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,last_sequence,retained_count,retained_variable_bytes,created_ms) VALUES('receipt-owner',?,'system','server',1,1,1,1)`, ownerInc[:])
	wc008NativeExec(t, w, `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES('receipt-owner',?,1,?,zeroblob(32),521,1,'credential_issue_v1',1,zeroblob(32),0)`, ownerInc[:], operation[:])
	wc008NativeExec(t, w, `INSERT INTO security_credential_results(owner_id,command_incarnation,command_sequence,command_kind,credential_id,kind,name,scope_count,hint,created_ms,expires_ms,last_used_ms,revoked_ms,state,ended_reason,account_id,member_incarnation,username,profile_id,profile_lifetime,profile_name,family_id,family_incarnation,device_id,secret_hash,secret_key_id,secret_nonce,secret_body,secret_aad_digest) VALUES('receipt-owner',?,1,521,'credential',?,'Original name',1,?,1,0,0,0,'active','','account',?,'Original user','profile',?,'Original profile','family',?,'device',?,'original-key',zeroblob(12),?,zeroblob(32))`, ownerInc[:], kind, hint, s.member, s.lifetime, s.familyIncarnation[:], s.secretHash, bytes.Repeat([]byte{0x5a}, length))
	wc008NativeExec(t, w, `INSERT INTO security_credential_result_scopes(owner_id,command_incarnation,command_sequence,ordinal,scope) VALUES('receipt-owner',?,1,0,'read')`, ownerInc[:])
	var bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_secrets WHERE credential_id='credential'`).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	return w, s, bytes
}
func usSecretDrainTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	turn := wc008NativeTurn(t, w, USMemberLibraries, usCredentialSecretDrainDefinitions())
	turn.transaction.descriptor.ID = 582
	return turn
}
func TestUSCredentialSecretDrainChildBeforeParent(t *testing.T) {
	for _, kind := range []string{"key", "app_password"} {
		t.Run(kind, func(t *testing.T) {
			w, s, bytes := usSecretDrainFixture(t, kind)
			if _, e := w.db.ExecContext(t.Context(), `DELETE FROM api_credentials WHERE id='credential'`); e == nil {
				t.Fatal("source deleted before child drain")
			}
			var originalBody []byte
			var originalName string
			var originalBytes int64
			if e := w.db.QueryRowContext(t.Context(), `SELECT name,secret_body,variable_bytes FROM security_credential_results WHERE credential_id='credential'`).Scan(&originalName, &originalBody, &originalBytes); e != nil {
				t.Fatal(e)
			}
			turn := usSecretDrainTurn(t, w)
			out, err := drainUSCredentialSecret(t.Context(), turn, s)
			if err != nil || !out.removed || out.variableBytes != bytes {
				t.Fatal(out, err)
			}
			again, err := drainUSCredentialSecret(t.Context(), turn, s)
			if err != nil || again != (usCredentialSecretDrainEffect{}) {
				t.Fatal("double refund", again, err)
			}
			// Parent source and family remain until the independently authorized parent
			// chooses retirement; a child effect is not a source-delete capability.
			var yes int
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM api_credentials WHERE id='credential'`).Scan(&yes); err != nil || yes != 1 {
				t.Fatal(yes, err)
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM api_credentials WHERE id='credential'`); err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM security_credential_secrets WHERE credential_id='other'`).Scan(&yes); err != nil || yes != 1 {
				t.Fatal("foreign secret changed", yes, err)
			}
			var savedBody []byte
			var savedName string
			var savedBytes int64
			if e := w.db.QueryRowContext(t.Context(), `SELECT name,secret_body,variable_bytes FROM security_credential_results WHERE credential_id='credential'`).Scan(&savedName, &savedBody, &savedBytes); e != nil {
				t.Fatal(e)
			}
			if savedName != originalName || string(savedBody) != string(originalBody) || savedBytes != originalBytes {
				t.Fatal("immutable issued response changed after source/secret retirement")
			}
			var scope string
			if e := w.db.QueryRowContext(t.Context(), `SELECT scope FROM security_credential_result_scopes WHERE owner_id='receipt-owner'`).Scan(&scope); e != nil || scope != "read" {
				t.Fatal("immutable scope cohort changed", e)
			}

		})
	}
}
func TestUSCredentialSecretDrainPrefixRollback(t *testing.T) {
	w, s, bytes := usSecretDrainFixture(t, "key")
	turn := usSecretDrainTurn(t, w)
	delete(turn.steps, usSecretDrainAbsent) // fail after actual secret DELETE
	out, err := drainUSCredentialSecret(t.Context(), turn, s)
	if err == nil || out != (usCredentialSecretDrainEffect{}) {
		t.Fatal(out, err)
	}
	if _, e := turn.transaction.rollback(err); !errors.Is(e, err) {
		t.Fatal(e)
	}
	var restored int64
	if e := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_secrets WHERE credential_id='credential'`).Scan(&restored); e != nil || restored != bytes {
		t.Fatal(restored, e)
	}
	if _, e := w.db.ExecContext(t.Context(), `DELETE FROM api_credentials WHERE id='credential'`); e == nil {
		t.Fatal("source guard lost after rollback")
	}
}
func TestUSCredentialSecretDrainScopeAndLeaf(t *testing.T) {
	for _, mode := range []string{"family_incarnation", "generation", "ack", "secret_binding", "live_source", "live_family", "ordinary_family", "descendant", "wrong_hash", "source_hash", "row_hash", "server", "member", "row_member"} {
		t.Run(mode, func(t *testing.T) {
			w, s, _ := usSecretDrainFixture(t, "key")
			switch mode {
			case "wrong_hash":
				s.secretHash = usCredentialFixtureHash("wrong-hash")
			case "source_hash":
				wc008NativeExec(t, w, `UPDATE api_credentials SET secret_hash=? WHERE id='credential'`, usCredentialFixtureHash("changed-source-hash"))
			case "row_hash":
				wc008NativeExec(t, w, `UPDATE security_credential_secrets SET secret_hash=? WHERE credential_id='credential'`, usCredentialFixtureHash("changed-row-hash"))
			case "server":
				s.server = "other-server"
			case "member":
				s.member = strings.Repeat("d", 32)
			case "row_member":
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_member_incarnation='dddddddddddddddddddddddddddddddd' WHERE id='family'`)
			case "family_incarnation":
				s.familyIncarnation[0]++
			case "generation":
				s.familyGeneration++
			case "ack":
				s.acknowledged++
			case "secret_binding":
				wc008NativeExec(t, w, `UPDATE security_credential_secrets SET profile_lifetime='dddddddddddddddddddddddddddddddd' WHERE credential_id='credential'`)
			case "live_source":
				wc008NativeExec(t, w, `UPDATE api_credentials SET revoked_ms=0 WHERE id='credential'`)
			case "live_family":
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=0,revoked_reason='' WHERE id='family'`)
			case "ordinary_family":
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=0 WHERE id='family'`)
			case "descendant":
				wc008NativeExec(t, w, `UPDATE api_credentials SET parent_id='credential' WHERE id='other'`)
			}
			turn := usSecretDrainTurn(t, w)
			if out, e := drainUSCredentialSecret(t.Context(), turn, s); e == nil || out != (usCredentialSecretDrainEffect{}) {
				t.Fatal(out, e)
			}
		})
	}
}
func TestUSCredentialSecretDrainFullFrameAndLifetime(t *testing.T) {
	w, s, _ := usSecretDrainFixture(t, "key")
	turn := usSecretDrainTurn(t, w)
	tx := turn.transaction
	for _, mode := range []string{"purpose", "begun", "done", "policy", "durability", "synchronous"} {
		id, begun, done, policy, durability, synchronous := tx.descriptor.ID, tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous
		switch mode {
		case "purpose":
			tx.descriptor.ID = USMemberLibraries
		case "begun":
			tx.begun = false
		case "done":
			tx.done = true
		case "policy":
			tx.policyVerified = false
		case "durability":
			tx.descriptor.Durability = Normal
		case "synchronous":
			tx.settings.Synchronous = 1
		}
		out, e := drainUSCredentialSecret(t.Context(), turn, s)
		tx.descriptor.ID, tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous = id, begun, done, policy, durability, synchronous
		if out != (usCredentialSecretDrainEffect{}) || !errors.Is(e, ErrInvalid) {
			t.Fatal(mode, out, e)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if out, e := drainUSCredentialSecret(canceled, turn, s); e == nil || out != (usCredentialSecretDrainEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUSCredentialSecretDrainNativePrepare(t *testing.T) {
	w, _, _ := usSecretDrainFixture(t, "key")
	wc008NativePrepare(t, w, usCredentialSecretDrainDefinitions())
}

func TestUSCredentialSecretDrainExactDeleteCAS(t *testing.T) {
	w, s, bytes := usSecretDrainFixture(t, "key")
	turn := usSecretDrainTurn(t, w)
	row, err := wc008DraftQuery(t.Context(), turn, usSecretDrainDelete, s.credential, s.kind, s.account, s.member, s.profile, s.lifetime, s.family, s.familyIncarnation[:], s.secretHash, bytes+1)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if err = row.Scan(&got); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(got, err)
	}
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_secrets WHERE credential_id='credential'`).Scan(&got); err != nil || got != bytes {
		t.Fatal(got, err)
	}
}

func TestUSCredentialSecretDrainUnacknowledgedFamily(t *testing.T) {
	for _, expected := range []int64{0, 1} {
		t.Run(fmt.Sprint(expected), func(t *testing.T) {
			w, s, _ := usSecretDrainFixture(t, "key")
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_last_sequence=1,command_acknowledged_sequence=0,revoke_command_sequence=1,revoke_operation_id=zeroblob(16),revoke_request_digest=zeroblob(32),revoke_command_kind='admin_revoke',revoke_result_version=1,revoke_issuer_account_id='issuer',revoke_issuer_profile_id='issuer-profile',revoke_issuer_member_incarnation='cccccccccccccccccccccccccccccccc',revoke_issuer_profile_lifetime='dddddddddddddddddddddddddddddddd',revoke_issuer_family_id='issuer-family',revoke_result_reason='admin_revoke',revoke_result_at='2026-11-08T00:00:00Z',revoke_applied_ms=1,revoke_result_count=1,revoke_result_digest=zeroblob(32),revoked_reason='admin_revoke' WHERE id='family'`)
			s.acknowledged = expected
			turn := usSecretDrainTurn(t, w)
			if out, e := drainUSCredentialSecret(t.Context(), turn, s); out != (usCredentialSecretDrainEffect{}) || !errors.Is(e, ErrConflict) {
				t.Fatal(out, e)
			}
		})
	}
}
