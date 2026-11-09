package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"testing"
)

func usCredentialResultTestIdentity() (CommandIdentity, RequestDigest) {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, RequestDigest{3}
}

func usCredentialResultFixture() usCredentialSavedResult {
	k := "key-id"
	h := sha256.Sum256([]byte("synthetic"))
	return usCredentialSavedResult{purpose: usAPICredentialIssue, owner: "owner", id: "credential", kind: "key", name: " raw\x00\xff display ", hint: "abcdef", state: "active", account: "account", memberIncarnation: strings.Repeat("a", 32), username: "username", profile: "profile", profileLifetime: strings.Repeat("b", 32), profileName: "profile name", family: "family", device: "device", secretHash: base64.RawURLEncoding.EncodeToString(h[:]), familyIncarnation: [16]byte{1}, scopes: []string{"admin", "read"}, createdMS: math.MaxInt64, expiresMS: math.MaxInt64, appliedMS: 1, secret: usCredentialResultSecret{keyID: &k, nonce: make([]byte, 12), body: make([]byte, 62), aadDigest: make([]byte, 32)}}
}

// The closed result codec: every field participates exactly once, including the
// sealed artifact, and the plaintext secret is not part of the model at all.
func TestUSCredentialResultExactDigest(t *testing.T) {
	v := usCredentialResultFixture()
	id, r := usCredentialResultTestIdentity()
	d, err := usCredentialResultDigest(id, r, v)
	if err != nil {
		t.Fatal(err)
	}
	for i, mutate := range []func(*usCredentialSavedResult){
		func(v *usCredentialSavedResult) { v.owner += "x" },
		func(v *usCredentialSavedResult) { v.id += "x" },
		func(v *usCredentialSavedResult) { v.kind = "token"; v.secret.body = make([]byte, 63) },
		func(v *usCredentialSavedResult) { v.name += "x" },
		func(v *usCredentialSavedResult) { v.hint = "ghijkl" },
		func(v *usCredentialSavedResult) { v.account += "x" },
		func(v *usCredentialSavedResult) { v.memberIncarnation = strings.Repeat("c", 32) },
		func(v *usCredentialSavedResult) { v.username += "x" },
		func(v *usCredentialSavedResult) { v.profile += "x" },
		func(v *usCredentialSavedResult) { v.profileLifetime = strings.Repeat("c", 32) },
		func(v *usCredentialSavedResult) { v.profileName += "x" },
		func(v *usCredentialSavedResult) { v.family += "x" },
		func(v *usCredentialSavedResult) { v.device += "x" },
		func(v *usCredentialSavedResult) {
			v.secretHash = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		},
		func(v *usCredentialSavedResult) { v.familyIncarnation[0]++ },
		func(v *usCredentialSavedResult) { v.scopes[0], v.scopes[1] = v.scopes[1], v.scopes[0] },
		func(v *usCredentialSavedResult) { v.scopes[0] = "library" },
		func(v *usCredentialSavedResult) { v.createdMS-- },
		func(v *usCredentialSavedResult) { v.expiresMS-- },
		func(v *usCredentialSavedResult) { v.appliedMS++ },
		func(v *usCredentialSavedResult) { *v.secret.keyID += "x" },
		func(v *usCredentialSavedResult) { v.secret.nonce[0]++ },
		func(v *usCredentialSavedResult) { v.secret.body[0]++ },
		func(v *usCredentialSavedResult) { v.secret.aadDigest[0]++ },
		func(v *usCredentialSavedResult) {
			v.purpose = usAPICredentialRevoke
			v.state = "revoked"
			v.secret = usCredentialResultSecret{}
		},
	} {
		x := cloneUSCredentialResult(v)
		mutate(&x)
		got, e := usCredentialResultDigest(id, r, x)
		if e != nil || got == d {
			t.Fatal(i, e, "mutation kept or broke the digest")
		}
	}
	for _, change := range []func(*CommandIdentity, *RequestDigest){
		func(i *CommandIdentity, r *RequestDigest) { i.Sequence++ },
		func(i *CommandIdentity, r *RequestDigest) { i.OperationID[0]++ },
		func(i *CommandIdentity, r *RequestDigest) { i.Incarnation[0]++ },
		func(i *CommandIdentity, r *RequestDigest) { r[0]++ },
	} {
		i, q := id, r
		change(&i, &q)
		got, e := usCredentialResultDigest(i, q, v)
		if e != nil || got == d {
			t.Fatal("original identity", e)
		}
	}
	for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%100.2s"} {
		for _, x := range []any{v, v.secret} {
			if strings.Contains(fmt.Sprintf(f, x), "key-id") {
				t.Fatal("redaction", f)
			}
		}
	}
	x := cloneUSCredentialResult(v)
	x.scopes[0] = "library"
	x.secret.nonce[0] = 1
	x.secret.body[0] = 1
	x.secret.aadDigest[0] = 1
	*x.secret.keyID = "other"
	if v.scopes[0] != "admin" || v.secret.nonce[0] != 0 || v.secret.body[0] != 0 || v.secret.aadDigest[0] != 0 || *v.secret.keyID != "key-id" {
		t.Fatal("alias")
	}
}

func TestUSCredentialResultPurposeEvidence(t *testing.T) {
	for kind, width := range map[string]int{"key": 62, "token": 63, "app_password": 64} {
		v := usCredentialResultFixture()
		v.kind = kind
		v.secret.body = make([]byte, width)
		if kind == "app_password" {
			v.hint = ""
		}
		if e := validateUSCredentialResult(v); e != nil {
			t.Fatal(e)
		}
		v.secret.body = make([]byte, width+1)
		if validateUSCredentialResult(v) == nil {
			t.Fatal("width")
		}
	}
	v := usCredentialResultFixture()
	v.purpose = usAPICredentialRevoke
	v.secret = usCredentialResultSecret{}
	v.state = "ended"
	v.endedReason = "member_deleted"
	v.revokedMS = math.MaxInt64
	v.lastUsedMS = math.MaxInt64
	if e := validateUSCredentialResult(v); e != nil {
		t.Fatal(e)
	}
	id, r := usCredentialResultTestIdentity()
	d, _ := usCredentialResultDigest(id, r, v)
	v.state = "revoked"
	v.endedReason = ""
	d2, e := usCredentialResultDigest(id, r, v)
	if e != nil || d == d2 {
		t.Fatal("captured state")
	}
	for _, m := range []func(*usCredentialSavedResult){
		func(v *usCredentialSavedResult) { s := ""; v.secret.keyID = &s },
		func(v *usCredentialSavedResult) { v.secret.nonce = []byte{} },
		func(v *usCredentialSavedResult) { v.secret.body = []byte{} },
		func(v *usCredentialSavedResult) { v.secret.aadDigest = []byte{} },
	} {
		x := cloneUSCredentialResult(v)
		m(&x)
		if validateUSCredentialResult(x) == nil {
			t.Fatal("NULL versus empty")
		}
	}
	if bytes.Equal([]byte(v.name), []byte(strings.TrimSpace(v.name))) {
		t.Fatal("fixture")
	}
}

// The Go charge must equal the exact sum of the three SQL-generated
// variable_bytes columns for every purpose, kind and scope cohort.
func TestUSCredentialResultVariableBytesMatchNativeStorage(t *testing.T) {
	w := wc008NativeExecutor(t)
	c, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	defer c.ExecContext(t.Context(), "PRAGMA foreign_keys=ON")
	seq := 0
	for _, purpose := range []CommandID{usAPICredentialIssue, usAPICredentialRevoke} {
		for kind, width := range map[string]int{"key": 62, "token": 63, "app_password": 64} {
			seq++
			v := usCredentialResultFixture()
			v.purpose = purpose
			v.kind = kind
			v.secret.body = make([]byte, width)
			if kind == "app_password" {
				v.hint = ""
			}
			if purpose == usAPICredentialRevoke {
				v.secret = usCredentialResultSecret{}
				v.state = "expired"
				v.lastUsedMS = math.MaxInt64
				v.revokedMS = math.MaxInt64
			}
			codec, _ := usCredentialResultCodec(purpose)
			var header, child int64
			err = c.QueryRowContext(t.Context(), `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,zeroblob(16),?,randomblob(16),zeroblob(32),?,1,?,0,zeroblob(32),0) RETURNING variable_bytes`, v.owner, seq, purpose, codec).Scan(&header)
			if err != nil {
				t.Fatal(err)
			}
			var key any
			if v.secret.keyID != nil {
				key = *v.secret.keyID
			}
			err = c.QueryRowContext(t.Context(), `INSERT INTO security_credential_results(owner_id,command_incarnation,command_sequence,command_kind,credential_id,kind,name,scope_count,hint,created_ms,expires_ms,last_used_ms,revoked_ms,state,ended_reason,account_id,member_incarnation,username,profile_id,profile_lifetime,profile_name,family_id,family_incarnation,device_id,secret_hash,secret_key_id,secret_nonce,secret_body,secret_aad_digest) VALUES(?,zeroblob(16),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, v.owner, seq, purpose, v.id, v.kind, v.name, len(v.scopes), v.hint, v.createdMS, v.expiresMS, v.lastUsedMS, v.revokedMS, v.state, v.endedReason, v.account, v.memberIncarnation, v.username, v.profile, v.profileLifetime, v.profileName, v.family, v.familyIncarnation[:], v.device, v.secretHash, key, v.secret.nonce, v.secret.body, v.secret.aadDigest).Scan(&child)
			if err != nil {
				t.Fatal(err)
			}
			native := header + child
			for i, s := range v.scopes {
				var n int64
				err = c.QueryRowContext(t.Context(), `INSERT INTO security_credential_result_scopes(owner_id,command_incarnation,command_sequence,ordinal,scope) VALUES(?,zeroblob(16),?,?,?) RETURNING variable_bytes`, v.owner, seq, i, s).Scan(&n)
				if err != nil {
					t.Fatal(err)
				}
				native += n
			}
			got, err := usCredentialResultVariableBytes(v)
			if err != nil || got != native {
				t.Fatal(kind, purpose, got, native, err)
			}
		}
	}
}

func TestUSCredentialResultRejectsMalformedEvidence(t *testing.T) {
	for i, m := range []func(*usCredentialSavedResult){
		func(v *usCredentialSavedResult) { v.purpose = 0 },
		func(v *usCredentialSavedResult) { v.kind = "other" },
		func(v *usCredentialSavedResult) { v.secret.keyID = nil },
		func(v *usCredentialSavedResult) { s := ""; v.secret.keyID = &s },
		func(v *usCredentialSavedResult) { v.secret.nonce = make([]byte, 11) },
		func(v *usCredentialSavedResult) { v.secret.aadDigest = make([]byte, 31) },
		func(v *usCredentialSavedResult) { v.secretHash = strings.Repeat("_", 43) },
		func(v *usCredentialSavedResult) { v.scopes = []string{"read", "read"} },
		func(v *usCredentialSavedResult) { v.scopes = []string{"unknown"} },
		func(v *usCredentialSavedResult) { v.lastUsedMS = 1 },
		func(v *usCredentialSavedResult) { v.revokedMS = 1 },
		func(v *usCredentialSavedResult) { v.state = "revoked" },
		func(v *usCredentialSavedResult) { v.endedReason = "unavailable" },
	} {
		v := usCredentialResultFixture()
		m(&v)
		if validateUSCredentialResult(v) == nil {
			t.Fatal(i)
		}
	}
}
