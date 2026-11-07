package writer

import (
	"crypto/sha256"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// Raw schema/protocol controls on the existing private FULL native frame. These
// are not an accepting cleanup command, a governor profile or hold measurement.
func usFinalSchemaTurn(t *testing.T, w *executor) (*transaction, *testCustody) {
	t.Helper()
	now := time.Now().Add(time.Hour)
	w.now = func() time.Time { return now }
	custody := &testCustody{}
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), USMemberLibraries, recipe, custody)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	if tx.settings.Synchronous != 2 || !tx.policyVerified {
		t.Fatal("control is not physically FULL")
	}
	return tx, custody
}
func usControlOwner(t *testing.T, tx *transaction) {
	t.Helper()
	_, err := tx.conn.ExecContext(t.Context(), `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,created_ms) VALUES('control-owner',?,'system','server',0)`, make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
}
func TestUSFinalPINReceiptChecks(t *testing.T) {
	w := usSchemaDraftFixture(t)
	tx, _ := usFinalSchemaTurn(t, w)
	usControlOwner(t, tx)
	const insert = `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,result_pin_revision,result_pin_required,result_changed,result_count) VALUES('control-owner',?,?,?,?,?,1,?,0,?,'account','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','profile','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',2,2,?,?,0)`
	for index, test := range []struct {
		name              string
		kind              int64
		codec             string
		required, changed int64
		valid             bool
	}{
		{"set_true", 532, "pin_set_v1", 1, 1, true},
		{"clear_false", 533, "pin_clear_v1", 0, 1, true},
		{"set_false_refused", 532, "pin_set_v1", 0, 1, false},
		{"clear_true_refused", 533, "pin_clear_v1", 1, 1, false},
		{"set_unchanged_refused", 532, "pin_set_v1", 1, 0, false},
		{"clear_unchanged_refused", 533, "pin_clear_v1", 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation := make([]byte, 16)
			operation[0] = byte(index + 1)
			_, err := tx.conn.ExecContext(t.Context(), insert, make([]byte, 16), int64(index+1), operation, make([]byte, 32), test.kind, test.codec, make([]byte, 32), test.required, test.changed)
			if test.valid && err != nil {
				t.Fatal("valid concrete PIN output refused", err)
			}
			if !test.valid && (err == nil || !strings.Contains(err.Error(), "CHECK constraint failed")) {
				t.Fatal("exact PIN CHECK did not refuse", err)
			}
		})
	}
	var count int64
	if err := tx.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM security_command_receipts`).Scan(&count); err != nil || count != 2 {
		t.Fatal("CHECK refusal made effects", count, err)
	}
	t.Logf("final delta sha256=%x; native positive set/clear and four isolated CHECK falsifiers PASS", sha256.Sum256([]byte(usSchemaDraftSQL)))
}

func TestUSReceiptCleanupIsChildFirstAndKeepsDebt(t *testing.T) {
	w := usSchemaDraftFixture(t)
	tx, custody := usFinalSchemaTurn(t, w)
	usControlOwner(t, tx)
	exec := func(query string, args ...any) sql.Result {
		t.Helper()
		r, err := tx.conn.ExecContext(t.Context(), query, args...)
		if err != nil {
			t.Fatal("native protocol control", err)
		}
		return r
	}
	scalar := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := tx.conn.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
			t.Fatal("native scalar control", err)
		}
		return n
	}
	refuses := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.conn.ExecContext(t.Context(), query, args...); err == nil {
			t.Fatal("native constraint unexpectedly accepted")
		}
	}
	inc, digest := make([]byte, 16), make([]byte, 32)
	op1, op2 := make([]byte, 16), make([]byte, 16)
	op1[0], op2[0] = 1, 2
	exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,target_account_id,target_member_incarnation,result_revision,policy_inherits,result_changed,result_count) VALUES('control-owner',?,1,?,?,540,1,'member_libraries_v1',0,?,'account','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',2,0,1,2)`, inc, op1, digest, digest)
	for ordinal, id := range []string{"movies", "music"} {
		exec(`INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES('control-owner',?,1,'policy',?,?)`, inc, int64(ordinal), id)
	}
	header1 := scalar(`SELECT variable_bytes FROM security_command_receipts WHERE owner_id='control-owner' AND command_sequence=1`)
	bytes1 := header1 + scalar(`SELECT sum(variable_bytes) FROM security_result_libraries WHERE owner_id='control-owner' AND command_sequence=1`)
	exec(`UPDATE security_command_owners SET last_sequence=1,retained_count=1,retained_variable_bytes=? WHERE id='control-owner'`, bytes1)
	exec(`UPDATE security_command_owners SET acknowledged_sequence=1 WHERE id='control-owner'`)
	if scalar(`SELECT retained_variable_bytes FROM security_command_owners WHERE id='control-owner'`) != bytes1 || custody.returns != 0 {
		t.Fatal("ACK refunded live debt")
	}
	exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES('control-owner',?,2,?,?,552,1,'invitation_revoke_v1',0,?,1)`, inc, op2, digest, digest)
	exec(`INSERT INTO security_invitation_results(owner_id,command_incarnation,command_sequence,invitation_id,server_id,role,state,created_ms,expires_ms,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash) VALUES('control-owner',?,2,'invitation','','member','revoked',0,1000,2,'','inherit',0,1,1,'',?)`, inc, strings.Repeat("a", 64))
	exec(`INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES('control-owner',?,2,'invitation',0,'movies')`, inc)
	header2 := scalar(`SELECT variable_bytes FROM security_command_receipts WHERE owner_id='control-owner' AND command_sequence=2`)
	facts2 := scalar(`SELECT variable_bytes FROM security_invitation_results WHERE owner_id='control-owner' AND command_sequence=2`)
	library2 := scalar(`SELECT variable_bytes FROM security_result_libraries WHERE owner_id='control-owner' AND command_sequence=2`)
	bytes2 := header2 + facts2 + library2
	exec(`UPDATE security_command_owners SET last_sequence=2,retained_count=2,retained_variable_bytes=? WHERE id='control-owner'`, bytes1+bytes2)
	// No source/receipt cascade may recursively erase an unvisited cohort.
	refuses(`DELETE FROM security_command_owners WHERE id='control-owner'`)
	refuses(`DELETE FROM security_command_receipts WHERE owner_id='control-owner' AND command_sequence=1`)
	refuses(`UPDATE security_command_owners SET reclaimed_sequence=2 WHERE id='control-owner'`)
	refuses(`UPDATE security_command_owners SET retained_variable_bytes=-1 WHERE id='control-owner'`)
	first := scalar(`DELETE FROM security_result_libraries WHERE owner_id='control-owner' AND command_sequence=1 AND cohort='policy' AND ordinal=0 RETURNING variable_bytes`)
	exec(`UPDATE security_command_owners SET retained_variable_bytes=retained_variable_bytes-? WHERE id='control-owner' AND retained_count=2 AND reclaimed_sequence=0`, first)
	if scalar(`SELECT retained_count FROM security_command_owners WHERE id='control-owner'`) != 2 || scalar(`SELECT retained_variable_bytes FROM security_command_owners WHERE id='control-owner'`) != bytes1+bytes2-first || custody.returns != 0 {
		t.Fatal("partial drain lost header/debt or retired custody early")
	}
	physical, err := tx.commit()
	if err != nil || physical.state != committed || custody.returns != 1 {
		t.Fatal("partial drain did not retire through physical terminal", physical, err)
	}

	tx, custody = usFinalSchemaTurn(t, w)
	// The current native ACK floor is read in the same exclusive frame. An
	// unacknowledged receipt cannot lose even one child; no DELETE is fan-out.
	ack := scalar(`SELECT acknowledged_sequence FROM security_command_owners WHERE id='control-owner'`)
	r := exec(`DELETE FROM security_result_libraries WHERE owner_id='control-owner' AND command_sequence=2 AND cohort='invitation' AND ordinal=0 AND 2<=?`, ack)
	if n, err := r.RowsAffected(); err != nil || n != 0 {
		t.Fatal("unacknowledged cohort drained", n, err)
	}
	lastChild := scalar(`DELETE FROM security_result_libraries WHERE owner_id='control-owner' AND command_sequence=1 AND cohort='policy' AND ordinal=1 RETURNING variable_bytes`)
	deletedHeader := scalar(`DELETE FROM security_command_receipts WHERE owner_id='control-owner' AND command_sequence=1 RETURNING variable_bytes`)
	if deletedHeader != header1 || first+lastChild+deletedHeader != bytes1 {
		t.Fatal("native physical charge mismatch")
	}
	exec(`UPDATE security_command_owners SET retained_count=1,retained_variable_bytes=retained_variable_bytes-?,reclaimed_sequence=1 WHERE id='control-owner' AND reclaimed_sequence=0 AND acknowledged_sequence=1`, lastChild+deletedHeader)
	if scalar(`SELECT retained_variable_bytes FROM security_command_owners WHERE id='control-owner'`) != bytes2 {
		t.Fatal("unacknowledged receipt debt changed")
	}
	exec(`UPDATE security_command_owners SET acknowledged_sequence=2 WHERE id='control-owner'`)
	refuses(`DELETE FROM security_command_receipts WHERE owner_id='control-owner' AND command_sequence=2`)
	refuses(`UPDATE security_command_owners SET retained_count=0 WHERE id='control-owner'`)
	if scalar(`DELETE FROM security_invitation_results WHERE owner_id='control-owner' AND command_sequence=2 RETURNING variable_bytes`) != facts2 {
		t.Fatal("typed snapshot charge mismatch")
	}
	if scalar(`DELETE FROM security_result_libraries WHERE owner_id='control-owner' AND command_sequence=2 AND cohort='invitation' AND ordinal=0 RETURNING variable_bytes`) != library2 {
		t.Fatal("invitation library charge mismatch")
	}
	if scalar(`DELETE FROM security_command_receipts WHERE owner_id='control-owner' AND command_sequence=2 RETURNING variable_bytes`) != header2 {
		t.Fatal("header charge mismatch")
	}
	exec(`UPDATE security_command_owners SET retained_count=0,retained_variable_bytes=0,reclaimed_sequence=2 WHERE id='control-owner' AND acknowledged_sequence=2 AND reclaimed_sequence=1 AND retained_count=1 AND retained_variable_bytes=?`, bytes2)
	if scalar(`SELECT count(*) FROM security_command_owners WHERE id='control-owner' AND last_sequence=2 AND acknowledged_sequence=2 AND reclaimed_sequence=2 AND retained_count=0 AND retained_variable_bytes=0`) != 1 || custody.returns != 0 {
		t.Fatal("retained floor/tombstone/debt boundary lost")
	}
	physical, err = tx.commit()
	if err != nil || physical.state != committed || custody.returns != 1 {
		t.Fatal(physical, err)
	}
	t.Logf("final delta sha256=%x; two physical FULL frames; child-first partial/final drain, ACK debt and retained owner floor PASS", sha256.Sum256([]byte(usSchemaDraftSQL)))
}
