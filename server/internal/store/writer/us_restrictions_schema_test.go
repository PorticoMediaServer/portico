package writer

import (
	"strings"
	"testing"
)

func TestUSRestrictionResultSchemaAndOrderedLabels(t *testing.T) {
	w, _, input := usPolicyReceiptFixture(t)
	key := []any{input.request.ownerID, input.identity.Incarnation[:], input.identity.Sequence}
	const header = `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`
	h := append(append([]any(nil), key...), input.identity.OperationID[:], input.digest[:], int64(542), int64(1), "profile_restrictions_v1", int64(1000), make([]byte, 32), int64(2))
	for _, change := range []func([]any){
		func(v []any) { v[5] = int64(543) },
		func(v []any) { v[7] = "member_libraries_v1" },
		func(v []any) { v[10] = int64(65) },
	} {
		v := append([]any(nil), h...)
		change(v)
		if _, err := w.db.ExecContext(t.Context(), header, v...); err == nil {
			t.Fatal("open purpose/codec/count accepted")
		}
	}
	if _, err := w.db.ExecContext(t.Context(), header, h...); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE security_command_receipts SET result_body=X'' WHERE owner_id=?`, key[0]); err == nil {
		t.Fatal("generic body accepted for restrictions")
	}
	const result = `INSERT INTO security_restriction_results(owner_id,command_incarnation,command_sequence,command_kind,account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	r := append(append([]any(nil), key...), int64(542), "target", strings.Repeat("e", 32), "target-profile", strings.Repeat("f", 32), int64(2), "", "", int64(-1), int64(1), int64(1), int64(1), int64(0), int64(1))
	for _, change := range []func([]any){
		func(v []any) { v[3] = int64(540) },
		func(v []any) { v[8] = int64(1) },
		func(v []any) { v[11] = int64(-2) },
		func(v []any) { v[12] = nil },
		func(v []any) { v[15] = int64(2) },
		func(v []any) { v[6] = nil },
	} {
		v := append([]any(nil), r...)
		change(v)
		if _, err := w.db.ExecContext(t.Context(), result, v...); err == nil {
			t.Fatal("invalid typed result accepted")
		}
	}
	if _, err := w.db.ExecContext(t.Context(), result, r...); err != nil {
		t.Fatal(err)
	}
	// Two different accepted invalid-byte inputs may JSON-decode to equal
	// replacement labels. Both positions are retained, without UNIQUE(label).
	const label = `INSERT INTO security_restriction_result_labels(owner_id,command_incarnation,command_sequence,ordinal,label) VALUES(?,?,?,?,?)`
	for _, bad := range []struct {
		ordinal int64
		label   string
	}{{-1, "x"}, {64, "x"}, {0, ""}, {0, strings.Repeat("界", 121)}} {
		v := append(append([]any(nil), key...), bad.ordinal, bad.label)
		if _, err := w.db.ExecContext(t.Context(), label, v...); err == nil {
			t.Fatal("invalid label encoding/ordinal accepted")
		}
	}
	repaired := strings.Repeat("\ufffd", 120)
	for ordinal := range 2 {
		v := append(append([]any(nil), key...), int64(ordinal), repaired)
		if _, err := w.db.ExecContext(t.Context(), label, v...); err != nil {
			t.Fatal("legal repaired duplicate rejected", err)
		}
	}
	var typedBytes, labelBytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_restriction_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...).Scan(&typedBytes); err != nil {
		t.Fatal(err)
	}
	want := len(input.request.ownerID) + 16 + len("target") + 32 + len("target-profile") + 32
	if typedBytes != int64(want) {
		t.Fatal("typed actual bytes", typedBytes, want)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_restriction_result_labels WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=0`, key...).Scan(&labelBytes); err != nil || labelBytes != int64(len(input.request.ownerID)+16+360) {
		t.Fatal("actual UTF-8 bytes", labelBytes, err)
	}
}

func TestUSRestrictionReceiptCleanupRetainsOwnerDebt(t *testing.T) {
	w, _, input := usPolicyReceiptFixture(t)
	key := []any{input.request.ownerID, input.identity.Incarnation[:], input.identity.Sequence}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,?,?,zeroblob(16),zeroblob(32),542,1,'profile_restrictions_v1',1000,zeroblob(32),1)`, key...); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_restriction_results(owner_id,command_incarnation,command_sequence,account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together) VALUES(?,?,?,'target',?,'target-profile',?,2,'','',-1,1,1,1,0,1)`, append(append([]any(nil), key...), strings.Repeat("e", 32), strings.Repeat("f", 32))...); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_restriction_result_labels(owner_id,command_incarnation,command_sequence,ordinal,label) VALUES(?,?,?,0,'label')`, key...); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=1,retained_variable_bytes=1000 WHERE id=?`, key[0]); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"security_command_receipts", "security_restriction_results"} {
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM `+table+` WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...); err == nil {
			t.Fatal("nonrecursive parent deletion accepted", table)
		}
	}
	for _, query := range []string{
		`DELETE FROM security_restriction_result_labels WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=0`,
		`DELETE FROM security_restriction_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
		`DELETE FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
	} {
		result, err := w.db.ExecContext(t.Context(), query, key...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	var count, bytes, floor int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT retained_count,retained_variable_bytes,reclaimed_sequence FROM security_command_owners WHERE id=?`, key[0]).Scan(&count, &bytes, &floor); err != nil || count != 1 || bytes != 1000 || floor != 0 {
		t.Fatal("physical cleanup silently refunded debt", count, bytes, floor, err)
	}
}
