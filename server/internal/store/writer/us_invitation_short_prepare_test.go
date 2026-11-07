package writer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func shortFixture() usInvitationShortRequest {
	i, s := usInvitationPreparationFixture()
	key := make([]byte, 32)
	h := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), key...))
	return usInvitationShortRequest{identity: i, scope: s, invitationID: "invite", codeHash: strings.Repeat("a", 64), state: "pending", role: "member", expectedRevision: 1, invitationRevision: 1, invitationExpiryMS: 900000, serverID: "srv_" + base64.RawURLEncoding.EncodeToString(h[:]), publicKey: key, keyIncarnation: "key1", signInKind: "local", kindRevision: 1, issuedMS: 1000}
}
func shortPrepare(t *testing.T, r usInvitationShortRequest) *usInvitationShortCommand {
	t.Helper()
	c, e := prepareUSInvitationShort(r, make([]byte, 10), make([]byte, 32), make([]byte, 32), make([]byte, 12), "display-key1")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestUSInvitationShortSourceAndProtection(t *testing.T) {
	r := shortFixture()
	c := shortPrepare(t, r)
	if c.request.lookupHash != "d64e83f539aa3159694401856ca8ae53e3370ece2588e0d1a1ab9d6d6bfd5a5b" || c.request.bindingHash != "a8e529b6e8a55a173fc408a55ab6747f3beea8452119902558bd1429f06365de" {
		t.Fatal("source tuple/domain/encoding mismatch")
	}
	b, _ := aes.NewCipher(make([]byte, 32))
	g, _ := cipher.NewGCM(b)
	w := c.request.wire
	plain, e := g.Open(nil, w.nonce, w.body, w.aad)
	if e != nil || string(plain) != "0000-0000-0000-0000" || len(w.body) != 35 {
		t.Fatal("display protection", e)
	}
	w.aad[0] ^= 1
	if _, e = g.Open(nil, w.nonce, w.body, w.aad); e == nil {
		t.Fatal("AAD unauthenticated")
	}
	w.aad[0] ^= 1
	r.publicKey[0] = 1
	if c.request.original.publicKey[0] != 0 {
		t.Fatal("public key alias")
	}
	for _, kind := range []string{"local", "portico"} {
		r = shortFixture()
		r.signInKind = kind
		if kind == "portico" {
			r.kindRevision = 1
		}
		r.invitationExpiryMS = 2000
		c = shortPrepare(t, r)
		if c.request.expiresMS != 2000 {
			t.Fatal("expiry cap")
		}
		if _, e = c.checkedDigest(); e != nil {
			t.Fatal(e)
		}
	}
	d, e := Describe(usInvitationShortIssue)
	if e != nil || d.Durability != Full {
		t.Fatal("553 metadata")
	}
}
func TestUSInvitationShortValidWitnessDigest(t *testing.T) {
	base := shortPrepare(t, shortFixture()).digest
	mutations := []func(*usInvitationShortRequest){
		func(r *usInvitationShortRequest) { r.identity.Incarnation[0]++ }, func(r *usInvitationShortRequest) { r.identity.OperationID[0]++ }, func(r *usInvitationShortRequest) { r.identity.Sequence++ },
		func(r *usInvitationShortRequest) { r.scope.ownerID += "x" }, func(r *usInvitationShortRequest) { r.scope.operationID += "x" },
		func(r *usInvitationShortRequest) { r.scope.issuer.server += "x" }, func(r *usInvitationShortRequest) { r.scope.issuer.account += "x" }, func(r *usInvitationShortRequest) { r.scope.issuer.profile += "x" }, func(r *usInvitationShortRequest) { r.scope.issuer.family += "x" },
		func(r *usInvitationShortRequest) { r.scope.issuer.memberIncarnation = strings.Repeat("d", 32) }, func(r *usInvitationShortRequest) { r.scope.issuer.profileLifetime = strings.Repeat("d", 32) }, func(r *usInvitationShortRequest) { r.scope.issuer.installation = strings.Repeat("d", 32) }, func(r *usInvitationShortRequest) { r.scope.issuer.tokenDigest = usCredentialFixtureHash("other") },
		func(r *usInvitationShortRequest) { r.scope.issuer.epoch++ }, func(r *usInvitationShortRequest) { r.scope.issuer.familyGeneration++ }, func(r *usInvitationShortRequest) { r.scope.issuer.authorityGeneration++ },
		func(r *usInvitationShortRequest) { r.invitationID += "x" },
		func(r *usInvitationShortRequest) { r.codeHash = strings.Repeat("b", 64) }, func(r *usInvitationShortRequest) { r.role = "admin" }, func(r *usInvitationShortRequest) { r.expectedRevision++; r.invitationRevision++ }, func(r *usInvitationShortRequest) { r.invitationExpiryMS++ },
		func(r *usInvitationShortRequest) {
			r.publicKey[0]++
			h := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), r.publicKey...))
			r.serverID = "srv_" + base64.RawURLEncoding.EncodeToString(h[:])
		},
		func(r *usInvitationShortRequest) { r.keyIncarnation += "x" }, func(r *usInvitationShortRequest) { r.resetGeneration++ }, func(r *usInvitationShortRequest) { r.signInKind = "portico"; r.kindRevision = 1 }, func(r *usInvitationShortRequest) { r.kindRevision++ }, func(r *usInvitationShortRequest) { r.issuedMS++ },
	}
	for n, m := range mutations {
		r := shortFixture()
		m(&r)
		if shortPrepare(t, r).digest == base {
			t.Fatalf("valid witness %d omitted", n)
		}
	}
}
func TestUSInvitationShortPayloadAndRedaction(t *testing.T) {
	changes := []func(*usInvitationShortPayload){func(p *usInvitationShortPayload) { p.expiresMS++ }, func(p *usInvitationShortPayload) { p.lookupHash = strings.Repeat("e", 64) }, func(p *usInvitationShortPayload) { p.bindingHash = strings.Repeat("f", 64) }, func(p *usInvitationShortPayload) { p.wire.keyID += "x" }, func(p *usInvitationShortPayload) { p.wire.nonce[0]++ }, func(p *usInvitationShortPayload) { p.wire.body[0]++ }, func(p *usInvitationShortPayload) { p.wire.aad[0]++ }}
	for _, m := range changes {
		c := shortPrepare(t, shortFixture())
		m(&c.request)
		if _, e := c.checkedDigest(); e == nil {
			t.Fatal("changed payload accepted")
		}
	}
	c := shortPrepare(t, shortFixture())
	for _, v := range []any{*c, c.request, c.request.original, c.request.wire} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if s := fmt.Sprintf(f, v); !strings.Contains(s, "redacted") {
				t.Fatal("unredacted carrier")
			}
		}
	}
	for _, m := range []func(*usInvitationShortRequest){func(r *usInvitationShortRequest) { r.state = "accepted" }, func(r *usInvitationShortRequest) { r.serverID = "" }, func(r *usInvitationShortRequest) { r.invitationRevision++ }, func(r *usInvitationShortRequest) { r.invitationExpiryMS = r.issuedMS }, func(r *usInvitationShortRequest) { r.signInKind = "hosted" }} {
		r := shortFixture()
		m(&r)
		if _, e := prepareUSInvitationShort(r, make([]byte, 10), make([]byte, 32), make([]byte, 32), make([]byte, 12), "key"); e == nil {
			t.Fatal("invalid original accepted")
		}
	}
}

func TestUSInvitationShortCryptoInputsFrozen(t *testing.T) {
	r := shortFixture()
	entropy, key, protect, nonce := make([]byte, 10), make([]byte, 32), make([]byte, 32), make([]byte, 12)
	c, e := prepareUSInvitationShort(r, entropy, key, protect, nonce, "key")
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range [][]byte{entropy, key, protect, nonce, r.publicKey} {
		b[0]++
	}
	if _, e = c.checkedDigest(); e != nil {
		t.Fatal("input buffer alias", e)
	}
	base := shortPrepare(t, shortFixture()).digest
	for n := 0; n < 5; n++ {
		entropy, key, protect, nonce = make([]byte, 10), make([]byte, 32), make([]byte, 32), make([]byte, 12)
		id := "display-key1"
		switch n {
		case 0:
			entropy[0]++
		case 1:
			key[0]++
		case 2:
			protect[0]++
		case 3:
			nonce[0]++
		case 4:
			id = "display-key2"
		}
		c, e = prepareUSInvitationShort(shortFixture(), entropy, key, protect, nonce, id)
		if e != nil || c.digest == base {
			t.Fatalf("crypto input %d not bound: %v", n, e)
		}
	}
}

func TestUSInvitationShortAADReconstruction(t *testing.T) {
	c := shortPrepare(t, shortFixture())
	p := c.request
	r := p.original
	// Rebuild solely from root's retained 553 columns and header/owner/issuerServer.
	m := usInvitationShortAAD{purpose: "portico-us-invitation-short-display-aad-v1", ownerID: r.scope.ownerID, issuerServer: r.scope.issuer.server, invitationID: r.invitationID, revision: r.invitationRevision, codeHash: r.codeHash, role: r.role, invitationExpiryMS: r.invitationExpiryMS, serverID: r.serverID, publicKey: r.publicKey, keyIncarnation: r.keyIncarnation, resetGeneration: r.resetGeneration, signInKind: r.signInKind, kindRevision: r.kindRevision, issuedMS: r.issuedMS, expiresMS: p.expiresMS, lookupHash: p.lookupHash, bindingHash: p.bindingHash, keyID: p.wire.keyID}
	d, e := canonicalRequestDigest(usInvitationShortIssue, 1, r.identity, m)
	if e != nil || string(d[:]) != string(p.wire.aad) {
		t.Fatal("AAD reconstruction", e)
	}
	r.scope.issuer.account += "x"
	other := shortPrepare(t, r)
	if string(other.request.wire.aad) != string(p.wire.aad) || other.digest == c.digest {
		t.Fatal("unsaved issuer witness entered AAD or escaped request digest")
	}
	for _, f := range []string{"%v", "%+v", "%#v", "%s", "%x"} {
		if !strings.Contains(fmt.Sprintf(f, m), "redacted") {
			t.Fatal("AAD formatting")
		}
	}
}

func TestUSInvitationShortKindZero(t *testing.T) {
	r := shortFixture()
	shortPrepare(t, r)
	r.kindRevision = 0
	if _, e := prepareUSInvitationShort(r, make([]byte, 10), make([]byte, 32), make([]byte, 32), make([]byte, 12), "key"); e == nil {
		t.Fatal("local revision zero accepted")
	}
	r.signInKind = "portico"
	shortPrepare(t, r)
}
