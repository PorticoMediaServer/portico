package api

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// An app sends its sign-in only to an address that signs a fresh question with the key the
// app remembered. The proof must verify, name the id that comes from that key, and be the
// same server after a restart.
func TestIdentityProof(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	var system System
	call(t, s, "GET", "/v1/system", "", nil, &system)
	nonce := strings.Repeat("n", 43)
	var proof IdentityProof
	if w := call(t, s, "POST", "/v1/system/identity-proof", "", IdentityQuestion{BaseURL: "http://portico.test", Nonce: nonce}, &proof); w.Code != 200 {
		t.Fatalf("the proof answered %d: %s", w.Code, w.Body)
	}
	dec := base64.RawURLEncoding.DecodeString
	payload, _ := dec(proof.Payload)
	signature, _ := dec(proof.Signature)
	var said struct{ Kind, BaseURL, Nonce, ServerID, PublicKey, Fingerprint string }
	if err := json.Unmarshal(payload, &said); err != nil {
		t.Fatal(err)
	}
	public, _ := dec(said.PublicKey)
	if len(public) != ed25519.PublicKeySize || !ed25519.Verify(public, payload, signature) {
		t.Fatal("the proof is not signed by the key it names")
	}
	id := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), public...))
	if want := "srv_" + base64.RawURLEncoding.EncodeToString(id[:]); said.ServerID != want || system.ID != want {
		t.Errorf("the server id is %q and the proof says %q; the key gives %q", system.ID, said.ServerID, want)
	}
	if said.Kind != "portico.route-proof" || said.BaseURL != "http://portico.test" || said.Nonce != nonce {
		t.Errorf("the proof does not answer the question asked: %+v", said)
	}
	if w := call(t, s, "POST", "/v1/system/identity-proof", "", IdentityQuestion{BaseURL: "http://portico.test", Nonce: "short"}, nil); w.Code != 400 {
		t.Errorf("a short nonce answered %d", w.Code)
	}
	var again System
	call(t, openTestServer(t, path), "GET", "/v1/system", "", nil, &again)
	if again.ID != system.ID {
		t.Errorf("the server id changed across a restart: %q then %q", system.ID, again.ID)
	}
}
