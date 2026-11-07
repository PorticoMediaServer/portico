package writer

import (
	_ "embed"
	"strings"
	"testing"
)

// Test-only UO-001 graph until INT adoption. This is not the canonical schema.
//
//go:embed testdata/uo-root-owner-draft.sql
var uoRootDraftSQL string

func uoRootFixture(t *testing.T) (*executor, string) {
	t.Helper()
	w := wc008NativeExecutor(t)
	var ddl strings.Builder
	for _, line := range strings.Split(uoRootDraftSQL, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			ddl.WriteString(line)
			ddl.WriteByte('\n')
		}
	}
	for _, sql := range strings.Split(ddl.String(), ";") {
		if strings.TrimSpace(sql) != "" {
			wc008NativeExec(t, w, sql)
		}
	}
	root := "abcdefghijklmnopqrstuv"
	wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,?)`, root, strings.Repeat("i", 32), "account", "profile", "account-birth", strings.Repeat("p", 32), "family", int64(1), "active")
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, root, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	return w, root
}

func TestUORootDraftNativeOwnershipAndPayloadBounds(t *testing.T) {
	w, root := uoRootFixture(t)
	insert := `INSERT INTO user_mutation_command_receipts(root_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,1,0,?,?,100)`
	inc := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	op := []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	digest := make([]byte, 32)
	if _, err := w.db.ExecContext(t.Context(), insert, root, make([]byte, 16), int64(1), op, int64(607), digest, digest, []byte{1}); err == nil {
		t.Fatal("wrong native owner incarnation accepted")
	}
	if _, err := w.db.ExecContext(t.Context(), insert, root, inc, int64(1), op, int64(607), digest, digest, make([]byte, 65537)); err == nil {
		t.Fatal("over-cap payload accepted")
	}
	wc008NativeExec(t, w, insert, root, inc, int64(1), op, int64(607), digest, digest, []byte{1, 2})
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_roots WHERE id=?`, root); err == nil {
		t.Fatal("root cascaded native owner/proof")
	}
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_command_owners WHERE root_id=?`, root); err == nil {
		t.Fatal("owner cascaded original proof")
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET last_command_sequence=2 WHERE root_id=?`, root); err == nil {
		t.Fatal("multiple unacknowledged commands accepted")
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET reclaim_command_sequence=1 WHERE root_id=?`, root); err == nil {
		t.Fatal("partial fixed cleanup result accepted")
	}
	var bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM user_mutation_command_receipts WHERE root_id=?`, root).Scan(&bytes); err != nil || bytes != 120 {
		t.Fatal(bytes, err)
	}
}
