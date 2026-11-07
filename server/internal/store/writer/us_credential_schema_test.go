package writer

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// Targeted controls for the three concrete new carriers. No production opener,
// crypto, accepting command, full suite or durability qualification.
func TestUSCredentialResultSchema(t *testing.T) {
	w := usSchemaDraftFixture(t)
	tx, _ := usFinalSchemaTurn(t, w)
	usControlOwner(t, tx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.conn.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.conn.ExecContext(t.Context(), q, args...); err == nil {
			t.Fatal("invalid exact carrier accepted")
		}
	}
	header := func(seq, kind int64, codec string) {
		t.Helper()
		op := make([]byte, 16)
		op[0] = byte(seq)
		exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES('control-owner',zeroblob(16),?,?,zeroblob(32),?,1,?,0,zeroblob(32),0)`, seq, op, kind, codec)
	}
	header(1, 520, "password_replace_v1")
	exec(`INSERT INTO security_password_results(owner_id,command_incarnation,command_sequence,account_id,member_incarnation,profile_id,profile_lifetime,member_revision,profile_revision,previous_epoch,result_epoch) VALUES('control-owner',zeroblob(16),1,'account',?,'profile',?,2,3,4,5)`, strings.Repeat("a", 32), strings.Repeat("b", 32))
	refuse(`UPDATE security_password_results SET result_epoch=4`)
	refuse(`UPDATE security_password_results SET command_kind=521`)
	const credential = `INSERT INTO security_credential_results(owner_id,command_incarnation,command_sequence,command_kind,credential_id,kind,name,scope_count,hint,created_ms,expires_ms,last_used_ms,revoked_ms,state,ended_reason,account_id,member_incarnation,username,profile_id,profile_lifetime,profile_name,family_id,family_incarnation,device_id,secret_hash,secret_key_id,secret_nonce,secret_body,secret_aad_digest) VALUES('control-owner',zeroblob(16),?,?,'credential',?,'Name',1,?,1000,0,0,?,?,?,'account',?,'user','profile',?,'Profile','family',zeroblob(16),'device',?,?,?,?,?)`
	for j, kind := range []string{"key", "token", "app_password"} {
		seq := int64(j + 2)
		header(seq, 521, "credential_issue_v1")
		hint, body := "abcdef", 62+j
		if kind == "app_password" {
			hint = ""
		}
		exec(credential, seq, int64(521), kind, hint, int64(0), "active", "", strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 64), "immutable-key", make([]byte, 12), make([]byte, body), make([]byte, 32))
		exec(`INSERT INTO security_credential_result_scopes VALUES('control-owner',zeroblob(16),?,0,'read')`, seq)
	}
	header(5, 522, "credential_revoke_v1")
	exec(credential, int64(5), int64(522), "token", "abcdef", int64(2000), "revoked", "", strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 64), nil, nil, nil, nil)
	refuse(`UPDATE security_credential_results SET secret_nonce=NULL WHERE command_sequence=2`)
	refuse(`UPDATE security_credential_results SET secret_body=zeroblob(63) WHERE command_sequence=2`)
	refuse(`UPDATE security_credential_results SET secret_key_id='key' WHERE command_sequence=5`)
	refuse(`UPDATE security_credential_results SET state='ended' WHERE command_sequence=5`)
	refuse(`UPDATE security_credential_results SET command_kind=522 WHERE command_sequence=2`)
	refuse(`INSERT INTO security_credential_result_scopes VALUES('control-owner',zeroblob(16),2,4,'admin')`)
	refuse(`INSERT INTO security_credential_result_scopes VALUES('control-owner',zeroblob(16),2,1,'unknown')`)
	refuse(`INSERT INTO security_credential_result_scopes VALUES('control-owner',zeroblob(16),1,0,'read')`)
	refuse(`DELETE FROM security_credential_results WHERE command_sequence=2`)
	refuse(`DELETE FROM security_command_receipts WHERE command_sequence=2`)
	refuse(`UPDATE security_command_receipts SET result_codec='credential_revoke_v1' WHERE command_sequence=2`)
	refuse(`UPDATE security_command_receipts SET result_key_id='generic-body' WHERE command_sequence=2`)
	var bytes int64
	if err := tx.conn.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_credential_results WHERE command_sequence=2`).Scan(&bytes); err != nil || bytes <= 0 {
		t.Fatal("native exact byte debt", err)
	}
	var bad int
	if err := tx.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&bad); err != nil || bad != 0 {
		t.Fatal("native FK", err)
	}
	t.Logf("credential delta SHA256=%x; explicit 520/521/522 carriers and isolated negative controls PASS", sha256.Sum256([]byte(usSchemaDraftSQL)))
}
