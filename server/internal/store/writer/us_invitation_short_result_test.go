package writer

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func shortResultFixture(t *testing.T) (CommandIdentity, RequestDigest, usInvitationShortSavedResult) {
	t.Helper()
	c := shortPrepare(t, shortFixture())
	p, r := c.request, c.request.original
	v := usInvitationShortSavedResult{kind: usInvitationShortIssue, version: 1, ownerID: r.scope.ownerID, issuerServer: r.scope.issuer.server, appliedMS: r.issuedMS + 1, invitationID: r.invitationID, invitationRevision: r.invitationRevision, codeHash: r.codeHash, role: r.role, invitationExpiryMS: r.invitationExpiryMS, serverID: r.serverID, publicKey: r.publicKey, keyIncarnation: r.keyIncarnation, resetGeneration: r.resetGeneration, signInKind: r.signInKind, kindRevision: r.kindRevision, issuedMS: r.issuedMS, expiresMS: p.expiresMS, lookupHash: p.lookupHash, bindingHash: p.bindingHash, wire: p.wire}
	return r.identity, c.digest, cloneUSInvitationShortResult(v)
}
func shortResultAAD(t *testing.T, i CommandIdentity, v *usInvitationShortSavedResult) {
	t.Helper()
	d, err := canonicalRequestDigest(usInvitationShortIssue, 1, i, v.manifest())
	if err != nil {
		t.Fatal(err)
	}
	v.wire.aad = append([]byte(nil), d[:]...)
}

func TestUSInvitationShortResultFieldsAndManifest(t *testing.T) {
	i, r, v := shortResultFixture(t)
	base, err := usInvitationShortResultDigest(i, r, v)
	if err != nil {
		t.Fatal(err)
	}
	changes := []func(*usInvitationShortSavedResult){
		func(v *usInvitationShortSavedResult) { v.ownerID += "2" }, func(v *usInvitationShortSavedResult) { v.issuerServer += "2" }, func(v *usInvitationShortSavedResult) { v.appliedMS++ }, func(v *usInvitationShortSavedResult) { v.invitationID += "2" }, func(v *usInvitationShortSavedResult) { v.invitationRevision++ }, func(v *usInvitationShortSavedResult) { v.codeHash = strings.Repeat("b", 64) }, func(v *usInvitationShortSavedResult) { v.role = "admin" }, func(v *usInvitationShortSavedResult) { v.invitationExpiryMS++ },
		func(v *usInvitationShortSavedResult) {
			v.publicKey[0]++
			h := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), v.publicKey...))
			v.serverID = "srv_" + base64.RawURLEncoding.EncodeToString(h[:])
		},
		func(v *usInvitationShortSavedResult) { v.keyIncarnation += "2" }, func(v *usInvitationShortSavedResult) { v.resetGeneration++ }, func(v *usInvitationShortSavedResult) { v.signInKind = "portico" }, func(v *usInvitationShortSavedResult) { v.kindRevision++ }, func(v *usInvitationShortSavedResult) { v.issuedMS++; v.expiresMS++ }, func(v *usInvitationShortSavedResult) { v.lookupHash = strings.Repeat("c", 64) }, func(v *usInvitationShortSavedResult) { v.bindingHash = strings.Repeat("d", 64) }, func(v *usInvitationShortSavedResult) { v.wire.keyID += "2" }, func(v *usInvitationShortSavedResult) { v.wire.nonce[0]++ }, func(v *usInvitationShortSavedResult) { v.wire.body[0]++ },
	}
	for n, change := range changes {
		q := cloneUSInvitationShortResult(v)
		change(&q)
		shortResultAAD(t, i, &q)
		d, err := usInvitationShortResultDigest(i, r, q)
		if err != nil || d == base {
			t.Fatal("saved field omitted", n, err)
		}
	}
	for _, change := range []func(*CommandIdentity){func(i *CommandIdentity) { i.Incarnation[0]++ }, func(i *CommandIdentity) { i.OperationID[0]++ }, func(i *CommandIdentity) { i.Sequence++ }} {
		j, q := i, cloneUSInvitationShortResult(v)
		change(&j)
		shortResultAAD(t, j, &q)
		d, err := usInvitationShortResultDigest(j, r, q)
		if err != nil || d == base {
			t.Fatal("original identity omitted", err)
		}
	}
	d, err := usInvitationShortResultDigest(i, RequestDigest{9}, v)
	if err != nil || d == base {
		t.Fatal("request digest omitted", err)
	}
	q := cloneUSInvitationShortResult(v)
	q.wire.aad[0] ^= 1
	if _, err := usInvitationShortResultDigest(i, r, q); err == nil {
		t.Fatal("foreign AAD accepted")
	}
}

func TestUSInvitationShortResultFormatAndNativeBytes(t *testing.T) {
	i, r, v := shortResultFixture(t)
	for _, verb := range []string{"%s", "%v", "%#v", "%+v", "%q", "%x"} {
		if out := fmt.Sprintf(verb, v); !strings.Contains(out, "redacted") {
			t.Fatal("saved evidence formatted", verb)
		}
	}
	clone := cloneUSInvitationShortResult(v)
	clone.publicKey[0]++
	clone.wire.body[0]++
	clone.wire.nonce[0]++
	clone.wire.aad[0]++
	if _, err := usInvitationShortResultDigest(i, r, v); err != nil {
		t.Fatal("result buffers alias", err)
	}
	w := usSchemaDraftFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,'us-owner','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','profile','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','cccccccccccccccccccccccccccccccc','family',0)`, v.ownerID, i.Incarnation[:], v.issuerServer); err != nil {
		t.Fatal(err)
	}
	digest, err := usInvitationShortResultDigest(i, r, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,?,?,?,?,553,1,'invitation_short_issue_v1',?,?,0)`, v.ownerID, i.Incarnation[:], i.Sequence, i.OperationID[:], r[:], v.appliedMS, digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_invitation_short_results(owner_id,command_incarnation,command_sequence,invitation_id,invitation_revision,invitation_code_hash,invitation_role,invitation_expires_ms,server_id,server_public_key,server_key_incarnation,server_reset_generation,sign_in_kind,kind_revision,issued_ms,expires_ms,lookup_hash,binding_hash,result_key_id,result_nonce,result_body,result_aad_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ownerID, i.Incarnation[:], i.Sequence, v.invitationID, v.invitationRevision, v.codeHash, v.role, v.invitationExpiryMS, v.serverID, v.publicKey, v.keyIncarnation, v.resetGeneration, v.signInKind, v.kindRevision, v.issuedMS, v.expiresMS, v.lookupHash, v.bindingHash, v.wire.keyID, v.wire.nonce, v.wire.body, v.wire.aad); err != nil {
		t.Fatal(err)
	}
	var header, child int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_command_receipts WHERE owner_id=?`, v.ownerID).Scan(&header); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM security_invitation_short_results WHERE owner_id=?`, v.ownerID).Scan(&child); err != nil {
		t.Fatal(err)
	}
	want, err := usInvitationShortResultVariableBytes(v)
	if err != nil || header+child != want {
		t.Fatal("native byte formula", header, child, want, err)
	}
}
