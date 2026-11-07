package writer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestUSInvitationExactCodeProtection(t *testing.T) {
	i, scope := usInvitationPreparationFixture()
	code := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{21}, 32))
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	p, err := prepareUSInvitationCreate(i, scope, "invitation", hash, usInvitationCreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	key, nonce := bytes.Repeat([]byte{17}, 32), bytes.Repeat([]byte{18}, 12)
	protected, err := prepareUSInvitationCode(p, "key-1", key, nonce, code)
	if err != nil || len(protected.body) != 59 {
		t.Fatal("exact wire protection", err)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	plain, err := aead.Open(nil, protected.nonce, protected.body, protected.manifest[:])
	if err != nil || string(plain) != code {
		t.Fatal("exact issued wire not retained", err)
	}
	command, err := freezeUSInvitationIssuance(p, protected, usInvitationLinkBinding{mode: "portico", revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := canonicalRequestDigest(usInvitationCreate, 1, i, command.request)
	if err != nil || expected != command.digest {
		t.Fatal("incomplete issuance digest", err)
	}
	protected.body[0] ^= 1
	protected.nonce[0] ^= 1
	nonce[0] ^= 1
	key[0] ^= 1
	if command.request.code.body[0] == protected.body[0] || command.request.code.nonce[0] == protected.nonce[0] {
		t.Fatal("mutable protected evidence aliases frozen command")
	}
	// Every protected output witness is covered by the frozen request constructor.
	for _, change := range []func(*usInvitationProtectedCode){
		func(v *usInvitationProtectedCode) { v.body[0] ^= 1 },
		func(v *usInvitationProtectedCode) { v.nonce[0] ^= 1 },
	} {
		q := command.request.code
		q.body = bytes.Clone(q.body)
		q.nonce = bytes.Clone(q.nonce)
		change(&q)
		next, err := freezeUSInvitationIssuance(p, q, usInvitationLinkBinding{mode: "portico", revision: 1})
		if err != nil || next.digest == command.digest {
			t.Fatal("protected witness absent from full digest", err)
		}
	}
	for _, change := range []func(*usInvitationPrepared){
		func(q *usInvitationPrepared) { q.identity.Incarnation[0]++ },
		func(q *usInvitationPrepared) { q.identity.Sequence++ },
		func(q *usInvitationPrepared) { q.identity.OperationID[0]++ },
		func(q *usInvitationPrepared) { q.scope.ownerID = "another-owner" },
		func(q *usInvitationPrepared) { q.scope.issuer.server = "another-server" },
		func(q *usInvitationPrepared) { q.id = "another-invitation" },
		func(q *usInvitationPrepared) { q.codeHash = strings.Repeat("d", 64) },
	} {
		q := *p
		change(&q)
		aad := usInvitationCodeAAD(q.identity, q.scope.ownerID, q.scope.issuer.server, q.id, q.codeHash, "key-1")
		if _, err := aead.Open(nil, command.request.code.nonce, command.request.code.body, aad[:]); err == nil {
			t.Fatal("cross-manifest secret disclosure")
		}
		if _, err := freezeUSInvitationIssuance(&q, command.request.code, usInvitationLinkBinding{mode: "portico", revision: 1}); err == nil {
			t.Fatal("mismatched evidence manifest frozen")
		}
	}
	aad := usInvitationCodeAAD(i, scope.ownerID, scope.issuer.server, p.id, p.codeHash, "key-2")
	if _, err := aead.Open(nil, command.request.code.nonce, command.request.code.body, aad[:]); err == nil {
		t.Fatal("key identity not in AAD")
	}
	for _, v := range []any{command, command.request, command.request.code, &command.request.code} {
		for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%100.3s"} {
			text := fmt.Sprintf(format, v)
			if !strings.Contains(text, "redacted") || strings.Contains(text, code) || strings.Contains(text, hash) {
				t.Fatal("private issuance formatting")
			}
		}
	}
}

func TestUSInvitationCodePreparationRefusesMalformedWire(t *testing.T) {
	i, scope := usInvitationPreparationFixture()
	p, _ := prepareUSInvitationCreate(i, scope, "invite", strings.Repeat("a", 64), usInvitationCreateInput{})
	code := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	key, nonce := make([]byte, 32), make([]byte, 12)
	for _, attempt := range []struct {
		k, n []byte
		code string
	}{
		{key[:31], nonce, code}, {key, nonce[:11], code}, {key, nonce, code[:42]},
		{key, nonce, strings.Repeat("!", 43)}, {key, nonce, code},
	} {
		if _, err := prepareUSInvitationCode(p, "key", attempt.k, attempt.n, attempt.code); err == nil {
			t.Fatal("malformed or hash-mismatched wire accepted")
		}
	}
	if _, err := prepareUSInvitationCode(nil, "key", key, nonce, code); err == nil {
		t.Fatal("nil input")
	}
}
