package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"testing"
)

func TestScopedIPv6IdentityProof(t *testing.T) {
	_, d := identityProofFixture(t)
	h := New(d)
	var pub []byte
	if err := d.DB.QueryRow("SELECT public_key FROM networking_claim_identity").Scan(&pub); err != nil {
		t.Fatal(err)
	}
	base := "http://[fe80::1234%25Ethernet_2]:32500"
	nonce := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	body, _ := json.Marshal(IdentityProofRequest{BaseURL: base, Nonce: nonce})
	for _, tc := range []struct{ name, host, origin string }{
		{"scoped host and origin", "[fe80::1234%Ethernet_2]:32500", base},
		{"native client omits scope in Host", "[fe80::1234]:32500", ""},
		{"wrong host", "[fe80::5678]:32500", ""},
		{"wrong port", "[fe80::1234]:32501", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", base+"/v1/networking/identity-proof", bytes.NewReader(body))
			r.Host = tc.host
			r.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if tc.name == "wrong host" || tc.name == "wrong port" {
				if w.Code != http.StatusBadRequest {
					t.Fatalf("wrong endpoint accepted: %d %s", w.Code, w.Body)
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("scoped proof rejected: %d %s", w.Code, w.Body)
			}
			var out IdentityProofResponse
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			payload, err := base64.RawURLEncoding.DecodeString(out.Payload)
			if err != nil {
				t.Fatal(err)
			}
			signature, err := base64.RawURLEncoding.DecodeString(out.Signature)
			if err != nil {
				t.Fatal(err)
			}
			if !ed25519.Verify(pub, payload, signature) {
				t.Fatal("proof signature did not match the pinned identity")
			}
			var proof networking.RouteIdentity
			if err := json.Unmarshal(payload, &proof); err != nil {
				t.Fatal(err)
			}
			if proof.BaseURL != base || proof.Nonce != nonce || proof.ServerID != d.Identity.ID() {
				t.Fatalf("proof changed its scope: %+v", proof)
			}
		})
	}
}
