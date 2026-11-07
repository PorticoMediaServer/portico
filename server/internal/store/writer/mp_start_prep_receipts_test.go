package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Test-only projection fixtures. The parent owns the exact F mapping and split
// header/result DDL; this exercises each proposed typed scalar SQL contract.
func mpNativeReceiptTables(n *mpDraftNative) {
	n.t.Helper()
	for _, d := range mpPrepReceiptDefinitions() {
		if d.Kind != stmt.Write || !strings.Contains(string(d.ID), "receipt_insert") {
			continue
		}
		sqlText := strings.TrimPrefix(d.SQL, "INSERT INTO ")
		open := strings.IndexByte(sqlText, '(')
		close := strings.IndexByte(sqlText, ')')
		table := sqlText[:open]
		cols := strings.Split(sqlText[open+1:close], ",")
		var defs []string
		param := 0
		for _, col := range cols {
			typ := "INTEGER"
			if col != "result_version" {
				p := d.Params[param]
				param++
				switch p.Type {
				case stmt.Text:
					typ = "TEXT"
				case stmt.Blob:
					typ = "BLOB"
				}
			}
			defs = append(defs, col+" "+typ)
		}
		owner := "operation_id"
		if strings.HasPrefix(table, "prepared_media_job") {
			owner = "job_id"
		}
		if strings.Contains(table, "birth_results") {
			owner = "birth_owner_id"
		}
		defs = append(defs, "PRIMARY KEY("+owner+",command_incarnation,command_sequence)")
		n.exec("CREATE TABLE " + table + "(" + strings.Join(defs, ",") + ")")
	}
	for _, table := range []string{"playback_start_preparation_birth_owners", "prepared_media_job_birth_owners"} {
		n.exec("CREATE TABLE " + table + "(id TEXT PRIMARY KEY,mp_command_incarnation BLOB,mp_command_last_sequence INTEGER,mp_command_acknowledged_sequence INTEGER,mp_receipt_rows INTEGER,mp_receipt_variable_bytes INTEGER)")
	}
}
func TestMPStartPrepNativeOriginalReceipt(t *testing.T) {
	n := mpNativeFixture(t)
	mpNativeReceiptTables(n)
	r, err := applyMPStartPrepBegin(t.Context(), n.purpose(MPStartPrepBegin), mpNativeStart())
	if err != nil {
		t.Fatal(err)
	}
	// Birth owner and later root commands have independent sequence/incarnations.
	identity := mpDraftIdentity()
	n.exec("INSERT INTO playback_start_preparation_birth_owners VALUES('birth',?,0,0,0,0)", identity.Incarnation[:])
	v := mpStartPrepReceipt{header: mpPrepReceiptHeader{kind: MPStartPrepBegin, identity: identity, request: RequestDigest{3}, appliedMS: 100}, result: r}
	v.header.resultDigest, err = mpStartPrepResultDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	debt, err := storeMPStartPrepBirthReceipt(t.Context(), n.purpose(MPStartPrepBegin), mpStartPrepBirthOwner{id: "birth", before: mpPrepOwnerBefore{incarnation: identity.Incarnation}}, v)
	if err != nil || debt <= 0 {
		t.Fatal(debt, err)
	}
	var last int64
	if err = n.conn.QueryRowContext(t.Context(), "SELECT mp_command_last_sequence FROM playback_start_preparations WHERE id='prep'").Scan(&last); err != nil || last != 0 {
		t.Fatal("birth consumed root sequence", last, err)
	}
	r, err = applyMPStartPrepClaim(t.Context(), n.purpose(MPStartPrepClaim), mpNativeStartFence(r), "execution")
	if err != nil {
		t.Fatal(err)
	}
	identity.Incarnation = r.rootIncarnation
	identity.OperationID = [16]byte{9}
	v = mpStartPrepReceipt{header: mpPrepReceiptHeader{kind: MPStartPrepClaim, identity: identity, request: RequestDigest{4}, appliedMS: 200}, result: r}
	v.header.resultDigest, err = mpStartPrepResultDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	debt, err = storeMPStartPrepReceipt(t.Context(), n.purpose(MPStartPrepClaim), mpPrepOwnerBefore{incarnation: identity.Incarnation}, v)
	if err != nil {
		t.Fatal(err)
	}
	var retained int64
	if err = n.conn.QueryRowContext(t.Context(), "SELECT mp_receipt_variable_bytes FROM playback_start_preparations WHERE id='prep'").Scan(&retained); err != nil || retained != debt {
		t.Fatal(retained, debt, err)
	}
	n.exec("UPDATE playback_start_preparation_lifecycle SET revision=revision+1,state='retired'; UPDATE inventory_objects SET revision='drifted'")
	got, err := loadMPStartPrepReceipt(t.Context(), n.purpose(MPStartPrepClaim), "prep", v.header)
	if err != nil || got != v {
		t.Fatal("reconstructed current instead of original", got, v, err)
	}
	n.exec("UPDATE playback_start_preparation_command_history SET result_session_id='tampered'")
	got, err = loadMPStartPrepReceipt(t.Context(), n.purpose(MPStartPrepClaim), "prep", v.header)
	if !errors.Is(err, ErrResolveRequired) || got != (mpStartPrepReceipt{}) {
		t.Fatal("tampered original disclosed", got, err)
	}
}
func TestMPTranscodeNativeOriginalReceipt(t *testing.T) {
	n := mpNativeFixture(t)
	mpNativeReceiptTables(n)
	r, err := applyMPTranscodeCreate(t.Context(), n.purpose(MPTranscodeCreate), mpNativeJob())
	if err != nil {
		t.Fatal(err)
	}
	identity := mpDraftIdentity()
	n.exec("INSERT INTO prepared_media_job_birth_owners VALUES('birth',?,0,0,0,0)", identity.Incarnation[:])
	v := mpTranscodeReceipt{header: mpPrepReceiptHeader{kind: MPTranscodeCreate, identity: identity, request: RequestDigest{3}, appliedMS: 100}, result: r}
	v.header.resultDigest, err = mpTranscodeResultDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storeMPTranscodeBirthReceipt(t.Context(), n.purpose(MPTranscodeCreate), mpTranscodeBirthOwner{id: "birth", before: mpPrepOwnerBefore{incarnation: identity.Incarnation}}, v); err != nil {
		t.Fatal(err)
	}
	r, err = applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 8, 5000, "encode")
	if err != nil {
		t.Fatal(err)
	}
	identity.Incarnation = r.rootIncarnation
	identity.OperationID = [16]byte{9}
	v = mpTranscodeReceipt{header: mpPrepReceiptHeader{kind: MPTranscodeClaim, identity: identity, request: RequestDigest{4}, appliedMS: 200}, result: r}
	v.header.resultDigest, err = mpTranscodeResultDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storeMPTranscodeReceipt(t.Context(), n.purpose(MPTranscodeClaim), mpPrepOwnerBefore{incarnation: identity.Incarnation}, v); err != nil {
		t.Fatal(err)
	}
	n.exec("UPDATE prepared_media_jobs SET generation=generation+1,job_lease_owner=9,job_lease_generation=job_lease_generation+1,revision=revision+1")
	got, err := loadMPTranscodeReceipt(t.Context(), n.purpose(MPTranscodeClaim), "job", v.header)
	if err != nil || got != v {
		t.Fatal(got, v, err)
	}
	wrong := v.header
	wrong.request = RequestDigest{5}
	got, err = loadMPTranscodeReceipt(t.Context(), n.purpose(MPTranscodeClaim), "job", wrong)
	if !errors.Is(err, ErrResolveRequired) || got != (mpTranscodeReceipt{}) {
		t.Fatal(got, err)
	}
}
