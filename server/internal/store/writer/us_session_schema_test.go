package writer

import (
	"crypto/sha256"
	"database/sql"
	"strings"
	"testing"
)

func TestUSSessionResultSchemaAndOneRefreshReceipt(t *testing.T) {
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
			t.Fatal("invalid session carrier accepted")
		}
	}
	header := func(seq, kind int64, codec string) {
		t.Helper()
		op := make([]byte, 16)
		op[0] = byte(seq)
		exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES('control-owner',zeroblob(16),?,?,zeroblob(32),?,1,?,0,zeroblob(32),0)`, seq, op, kind, codec)
	}
	header(1, 511, "session_renewal_v1")
	header(2, 512, "session_refresh_v1")
	columns := []string{"owner_id", "command_incarnation", "command_sequence", "command_kind", "wire_variant", "protocol_version", "family_id", "family_incarnation", "server_id", "account_id", "member_incarnation", "profile_id", "profile_lifetime", "device_id", "installation_id", "authority", "role", "epoch", "authority_generation", "access_predecessor_generation", "generation", "access_predecessor_hash", "access_hash", "access_expires_at", "authorization_horizon", "request_id", "controller_id", "controller_epoch", "refresh_predecessor_generation", "refresh_predecessor_hash", "refresh_hash", "prior_receipt_hash", "key_id", "access_nonce", "access_body", "refresh_nonce", "refresh_body", "aad_digest"}
	values := map[string]any{"owner_id": "control-owner", "command_incarnation": make([]byte, 16), "command_sequence": int64(1), "command_kind": int64(511), "wire_variant": "authorization_envelope_v2", "protocol_version": "2.0", "family_id": "session-family", "family_incarnation": make([]byte, 16), "server_id": "server", "account_id": "account", "member_incarnation": strings.Repeat("a", 32), "profile_id": "profile", "profile_lifetime": strings.Repeat("b", 32), "device_id": "device", "installation_id": strings.Repeat("i", 32), "authority": "local", "role": "owner", "epoch": int64(1), "authority_generation": int64(1), "access_predecessor_generation": int64(3), "generation": int64(4), "access_predecessor_hash": usCredentialFixtureHash("session-synthetic-a"), "access_hash": usCredentialFixtureHash("session-synthetic-b"), "access_expires_at": "2026-11-07T14:15:00Z", "authorization_horizon": "2026-11-08T14:00:00Z", "request_id": "request", "controller_id": "controller", "controller_epoch": "controller-epoch", "key_id": "immutable-key", "access_nonce": make([]byte, 12), "access_body": make([]byte, 59), "aad_digest": make([]byte, 32)}
	insert := func() {
		t.Helper()
		args := make([]any, len(columns))
		for j, c := range columns {
			args[j] = values[c]
		}
		exec(`INSERT INTO security_session_rotation_results(`+strings.Join(columns, ",")+`) VALUES(`+strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")+`)`, args...)
	}
	insert()
	values["command_sequence"], values["command_kind"], values["wire_variant"], values["protocol_version"] = int64(2), int64(512), "envelope_v2", nil
	values["controller_id"], values["controller_epoch"] = nil, nil
	values["refresh_predecessor_generation"], values["refresh_predecessor_hash"], values["refresh_hash"] = int64(1), usCredentialFixtureHash("session-synthetic-c"), usCredentialFixtureHash("session-synthetic-d")
	values["refresh_nonce"], values["refresh_body"] = append([]byte{1}, make([]byte, 11)...), make([]byte, 59)
	insert()
	for _, q := range []string{
		`UPDATE security_session_rotation_results SET controller_id=NULL WHERE command_sequence=1`,
		`UPDATE security_session_rotation_results SET protocol_version=NULL WHERE command_sequence=1`,
		`UPDATE security_session_rotation_results SET refresh_body=zeroblob(59) WHERE command_sequence=1`,
		`UPDATE security_session_rotation_results SET refresh_nonce=NULL WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET refresh_nonce=access_nonce WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET refresh_predecessor_generation=4 WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET refresh_hash=access_hash WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET protocol_version='2.0' WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET wire_variant='authorization_envelope_v2' WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET generation=3 WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET access_body=zeroblob(58) WHERE command_sequence=2`,
		`UPDATE security_session_rotation_results SET command_kind=511 WHERE command_sequence=2`,
		`DELETE FROM security_command_receipts WHERE command_sequence=2`,
	} {
		refuse(q)
	}
	exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,current_generation,authorization_horizon,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES('session-family','server','account','profile','local','owner',1,1,'2026-11-08T14:00:00Z',?,?,zeroblob(16))`, strings.Repeat("a", 32), strings.Repeat("b", 32))
	exec(`INSERT INTO identity_refresh_receipts VALUES('previous','session-family','request',X'00','2026-11-08T14:00:00Z')`)
	refuse(`INSERT INTO identity_refresh_receipts VALUES('second','session-family','another',X'00','2026-11-08T14:00:00Z')`)
	var actual string
	if err := tx.conn.QueryRowContext(t.Context(), `DELETE FROM identity_refresh_receipts INDEXED BY sqlite_autoindex_identity_refresh_receipts_1 WHERE predecessor_hash='previous' AND family_id='session-family' RETURNING family_id`).Scan(&actual); err != nil || actual != "session-family" {
		t.Fatal("point predecessor retirement", err)
	}
	exec(`INSERT INTO identity_refresh_receipts VALUES('second','session-family','another',X'00','2026-11-08T14:00:00Z')`)
	var plan string
	var a, b, c int64
	if err := tx.conn.QueryRowContext(t.Context(), `EXPLAIN QUERY PLAN SELECT predecessor_hash FROM identity_refresh_receipts INDEXED BY identity_refresh_receipts_family WHERE family_id='session-family'`).Scan(&a, &b, &c, &plan); err != nil || !strings.Contains(plan, "SEARCH") || !strings.Contains(plan, "family_id=?") {
		t.Fatal("refresh family equality seek", err)
	}
	var unique int64
	if err := tx.conn.QueryRowContext(t.Context(), `SELECT "unique" FROM pragma_index_list('identity_refresh_receipts') WHERE name='identity_refresh_receipts_family'`).Scan(&unique); err != nil || unique != 1 {
		t.Fatal("refresh one-per-family native invariant", err)
	}
	var scope sql.NullString
	if err := tx.conn.QueryRowContext(t.Context(), `SELECT name FROM pragma_index_xinfo('identity_refresh_receipts_family') WHERE key=1 AND seqno=0`).Scan(&scope); err != nil || !scope.Valid || scope.String != "family_id" {
		t.Fatal("refresh actual key", err)
	}
	var count, bytes int64
	if err := tx.conn.QueryRowContext(t.Context(), `SELECT count(*),sum(variable_bytes) FROM security_session_rotation_results`).Scan(&count, &bytes); err != nil || count != 2 || bytes <= 0 {
		t.Fatal("native immutable results/byte debt", err)
	}
	t.Logf("session delta SHA256=%x; exact 511/512 variants, CHECK/FK and one-active-refresh point transition PASS", sha256.Sum256([]byte(usSchemaDraftSQL)))
}
