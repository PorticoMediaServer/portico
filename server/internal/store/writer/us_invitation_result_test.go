package writer

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func invitationResultFixture() (CommandIdentity, RequestDigest, usInvitationSavedResult) {
	i, _ := usInvitationPreparationFixture()
	v := usInvitationSavedResult{kind: usInvitationCreate, version: 1, count: 2, ownerID: "owner", issuerServer: "family-server", id: "invitation", serverID: "server", notes: sql.NullString{String: "private notes", Valid: true}, role: "member", state: "pending", createdMS: 1000, expiresMS: 1000 + usInvitationHourMS, revision: 1, access: usInvitationAccess{mode: "inherit", allowRemote: true, accessTokens: true}, codeHash: strings.Repeat("a", 64), libraries: []string{"one", "two"}, appliedMS: 1000, code: usInvitationProtectedCode{keyID: "key", nonce: bytes.Repeat([]byte{1}, 12), body: bytes.Repeat([]byte{2}, 59)}}
	v.code.manifest = usInvitationCodeAAD(i, v.ownerID, v.issuerServer, v.id, v.codeHash, v.code.keyID)
	return i, RequestDigest{1}, v
}

func TestUSInvitationResultBinding(t *testing.T) {
	i, r, v := invitationResultFixture()
	base, err := usInvitationResultDigest(i, r, v)
	if err != nil {
		t.Fatal(err)
	}
	changes := []func(*usInvitationSavedResult){
		func(v *usInvitationSavedResult) { v.ownerID = "other" }, func(v *usInvitationSavedResult) { v.issuerServer = "other-server" }, func(v *usInvitationSavedResult) { v.id = "other" }, func(v *usInvitationSavedResult) { v.serverID = "other" }, func(v *usInvitationSavedResult) { v.notes.String = "different" }, func(v *usInvitationSavedResult) { v.notes = sql.NullString{} }, func(v *usInvitationSavedResult) { v.notes = sql.NullString{Valid: true} }, func(v *usInvitationSavedResult) { v.role = "admin" }, func(v *usInvitationSavedResult) { v.createdMS++; v.appliedMS++; v.expiresMS++ }, func(v *usInvitationSavedResult) { v.expiresMS += usInvitationHourMS }, func(v *usInvitationSavedResult) { v.boundUsername = "person" }, func(v *usInvitationSavedResult) { v.access.mode = "unlimited" }, func(v *usInvitationSavedResult) { v.access.mode = "limit"; v.access.streams = 2 }, func(v *usInvitationSavedResult) { v.access.allowRemote = false }, func(v *usInvitationSavedResult) { v.access.accessTokens = false }, func(v *usInvitationSavedResult) { v.access.rating = "PG" }, func(v *usInvitationSavedResult) { v.codeHash = strings.Repeat("b", 64) }, func(v *usInvitationSavedResult) { v.libraries = []string{"two", "one"} }, func(v *usInvitationSavedResult) { v.libraries = []string{"one", "three"} }, func(v *usInvitationSavedResult) { v.libraries = []string{}; v.count = 0 }, func(v *usInvitationSavedResult) { v.code.keyID = "key2" }, func(v *usInvitationSavedResult) { v.code.nonce = bytes.Repeat([]byte{3}, 12) }, func(v *usInvitationSavedResult) { v.code.body = bytes.Repeat([]byte{3}, 59) },
	}
	for n, change := range changes {
		q := v
		change(&q)
		q.code.manifest = usInvitationCodeAAD(i, q.ownerID, q.issuerServer, q.id, q.codeHash, q.code.keyID)
		d, e := usInvitationResultDigest(i, r, q)
		if e != nil || d == base {
			t.Fatal("saved fact unbound", n, e)
		}
	}
	for _, change := range []func(*CommandIdentity){func(i *CommandIdentity) { i.Sequence++ }, func(i *CommandIdentity) { i.OperationID[0]++ }, func(i *CommandIdentity) { i.Incarnation[0]++ }} {
		j := i
		change(&j)
		q := v
		q.code.manifest = usInvitationCodeAAD(j, q.ownerID, q.issuerServer, q.id, q.codeHash, q.code.keyID)
		d, e := usInvitationResultDigest(j, r, q)
		if e != nil || d == base {
			t.Fatal("identity unbound", e)
		}
		if _, e = usInvitationResultDigest(j, r, v); e == nil {
			t.Fatal("foreign manifest accepted")
		}
	}
	d, e := usInvitationResultDigest(i, RequestDigest{2}, v)
	if e != nil || d == base {
		t.Fatal("request unbound", e)
	}
	a := v
	a.notes = sql.NullString{}
	b := a
	b.notes.Valid = true
	da, _ := usInvitationResultDigest(i, r, a)
	db, _ := usInvitationResultDigest(i, r, b)
	if da == db {
		t.Fatal("null collapsed into empty")
	}
	for _, value := range []any{v, &v} {
		for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%100.3s"} {
			s := fmt.Sprintf(format, value)
			if !strings.Contains(s, "redacted") || strings.Contains(s, "private notes") {
				t.Fatal("result formatting leaked")
			}
		}
	}
}

func TestUSInvitationResultRejectsMalformedAndPurposeCrossing(t *testing.T) {
	i, r, v := invitationResultFixture()
	changes := []func(*usInvitationSavedResult){func(v *usInvitationSavedResult) { v.kind = USMemberLibraries }, func(v *usInvitationSavedResult) { v.version = 2 }, func(v *usInvitationSavedResult) { v.count++ }, func(v *usInvitationSavedResult) { v.count = -1 }, func(v *usInvitationSavedResult) { v.libraries = nil; v.count = 0 }, func(v *usInvitationSavedResult) { v.libraries = []string{"one", "one"} }, func(v *usInvitationSavedResult) { v.libraries = []string{""}; v.count = 1 }, func(v *usInvitationSavedResult) { v.state = "accepted" }, func(v *usInvitationSavedResult) { v.acceptedMS = sql.NullInt64{Int64: 2, Valid: true} }, func(v *usInvitationSavedResult) { v.accountID = sql.NullString{Valid: true} }, func(v *usInvitationSavedResult) { v.notes.Valid = false }, func(v *usInvitationSavedResult) { v.revision = 2 }, func(v *usInvitationSavedResult) { v.createdMS++ }, func(v *usInvitationSavedResult) { v.expiresMS = v.createdMS }, func(v *usInvitationSavedResult) { v.serverID = "bad server" }, func(v *usInvitationSavedResult) { v.notes.String = "\n" }, func(v *usInvitationSavedResult) { v.access.rating = "UNKNOWN" }, func(v *usInvitationSavedResult) { v.access.streams = 1 }, func(v *usInvitationSavedResult) { v.code.body = v.code.body[:58] }, func(v *usInvitationSavedResult) { v.code.manifest[0]++ }, func(v *usInvitationSavedResult) { v.kind = usInvitationEdit }, func(v *usInvitationSavedResult) { v.kind = usInvitationRevoke }}
	for n, change := range changes {
		q := v
		change(&q)
		if _, e := usInvitationResultDigest(i, r, q); e == nil {
			t.Fatal("malformed saved result accepted", n)
		}
	}
	for _, kind := range []CommandID{usInvitationEdit, usInvitationRevoke} {
		q := v
		q.kind = kind
		q.serverID = ""
		q.revision = 2
		q.code = usInvitationProtectedCode{}
		if kind == usInvitationRevoke {
			q.state = "revoked"
			q.appliedMS = q.expiresMS + 1
		}
		d, e := usInvitationResultDigest(i, r, q)
		if e != nil {
			t.Fatal(e)
		}
		changed := q
		changed.revision++
		next, e := usInvitationResultDigest(i, r, changed)
		if e != nil || next == d {
			t.Fatal("revision unbound", e)
		}
		changed = q
		changed.appliedMS++
		next, e = usInvitationResultDigest(i, r, changed)
		if e != nil || next == d {
			t.Fatal("applied time unbound", e)
		}
		for _, secret := range []usInvitationProtectedCode{v.code, {nonce: []byte{}}, {body: []byte{}}} {
			bad := q
			bad.code = secret
			if _, e := usInvitationResultDigest(i, r, bad); e == nil {
				t.Fatal("secret in existing DTO")
			}
		}
		q.serverID = "server"
		if _, e := usInvitationResultDigest(i, r, q); e == nil {
			t.Fatal("existing DTO server populated")
		}
	}
}

func TestUSInvitationResultNativeVariableBytes(t *testing.T) {
	w := usSchemaDraftFixture(t)
	i, r, v := invitationResultFixture()
	d, err := usInvitationResultDigest(i, r, v)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := w.db.ExecContext(t.Context(), query, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,created_ms) VALUES(?,?,'system',?,0)`, v.ownerID, i.Incarnation[:], v.issuerServer)
	exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body) VALUES(?,?,?,?,?,550,1,'invitation_create_v1',?,?,?,?,?,?)`, v.ownerID, i.Incarnation[:], i.Sequence, i.OperationID[:], r[:], v.appliedMS, d[:], v.count, v.code.keyID, v.code.nonce, v.code.body)
	exec(`INSERT INTO security_invitation_results(owner_id,command_incarnation,command_sequence,invitation_id,server_id,notes,role,state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ownerID, i.Incarnation[:], i.Sequence, v.id, v.serverID, v.notes, v.role, v.state, v.createdMS, v.expiresMS, v.acceptedMS, v.accountID, v.revision, v.boundUsername, v.access.mode, v.access.streams, v.access.allowRemote, v.access.accessTokens, v.access.rating, v.codeHash)
	for ordinal, id := range v.libraries {
		exec(`INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES(?,?,?,'invitation',?,?)`, v.ownerID, i.Incarnation[:], i.Sequence, ordinal, id)
	}
	var actual int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT sum(variable_bytes) FROM security_command_receipts)+(SELECT sum(variable_bytes) FROM security_invitation_results)+(SELECT sum(variable_bytes) FROM security_result_libraries)`).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	want, err := usInvitationResultVariableBytes(v)
	if err != nil || actual != want {
		t.Fatal("native variable byte mismatch", actual, want, err)
	}
}

// The family Viewer server ID and protected-key link ID are different source
// namespaces. The result must not use the public link ID as issuance AAD.
func TestUSInvitationResultKeepsOriginalServerDistinctFromLink(t *testing.T) {
	i, r, v := invitationResultFixture()
	v.serverID = "srv_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := usInvitationResultDigest(i, r, v); err != nil {
		t.Fatal("distinct original and link server IDs refused", err)
	}
	v.serverID = "" // current Hosted invitation DTO omits the local-link expectation
	if _, err := usInvitationResultDigest(i, r, v); err != nil {
		t.Fatal("source's empty link DTO refused", err)
	}
	v.issuerServer = "another-original-server"
	if _, err := usInvitationResultDigest(i, r, v); err == nil {
		t.Fatal("foreign original-server manifest accepted")
	}
}

func TestUSInvitationResultPreservesSourceTimesAcrossClockCorrection(t *testing.T) {
	i, r, v := invitationResultFixture()
	v.kind = usInvitationEdit
	v.revision = 2
	v.serverID = ""
	v.code = usInvitationProtectedCode{}
	v.createdMS = 10000
	v.appliedMS = 2000
	v.expiresMS = 5000
	if _, err := usInvitationResultDigest(i, r, v); err != nil {
		t.Fatal("legal edit expiry was compared to the old creation clock", err)
	}
	v.kind = usInvitationRevoke
	v.state = "revoked"
	v.appliedMS = 1500
	if _, err := usInvitationResultDigest(i, r, v); err != nil {
		t.Fatal("saved revoke creation time was recomputed", err)
	}
}
