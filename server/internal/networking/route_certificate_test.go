package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRouteCertificateFingerprintUsesLeafDER(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := pem.Decode(raw)
	digest := sha256.Sum256(leaf.Bytes)
	want := base64.RawURLEncoding.EncodeToString(digest[:])
	// An intermediate must never contribute to the native leaf fingerprint.
	cert.Certificate = append(cert.Certificate, []byte("intermediate"))
	if got := routeCertificateFingerprint(&cert); got != want || len(got) != 43 || strings.ContainsAny(got, "+/=") {
		t.Fatalf("fingerprint %q, want %q", got, want)
	}
}

func TestRouteCertificateCustomSourceTracksWhatTLSCanServe(t *testing.T) {
	ctx := context.Background()
	certPath, keyPath := customCertFixture(t, "media.example.com")
	custom := customWithStaticSource(certPath, keyPath, "media.example.com")
	first := custom.routeCertificateFingerprint(ctx)
	cert, err := custom.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if err != nil || first == "" || first != routeCertificateFingerprint(cert) {
		t.Fatal("binding differs from listener material", err)
	}
	// Broken replacement files retain the same last-good serving certificate.
	if err := os.WriteFile(certPath, []byte("broken replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	custom.lastCheck = time.Time{}
	if got := custom.routeCertificateFingerprint(ctx); got != first {
		t.Fatal("binding lost last-good certificate")
	}
	nextCert, nextKey := customCertFixture(t, "media.example.com")
	custom.SetSource(func(context.Context) (string, string, string) { return nextCert, nextKey, "media.example.com" })
	next := custom.routeCertificateFingerprint(ctx)
	if next == "" || next == first {
		t.Fatal("binding did not follow replacement TLS material")
	}
	custom.SetSource(func(context.Context) (string, string, string) { return nextCert, nextKey, "other.example.com" })
	if got := custom.routeCertificateFingerprint(ctx); got != "" {
		t.Fatal("authorized a certificate the listener cannot serve for its configured domain")
	}
	custom.SetSource(nil)
	if got := custom.routeCertificateFingerprint(ctx); got != "" {
		t.Fatal("disabled certificate retained binding")
	}
}

func TestRouteCertificateManagedActiveAndRenewalFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, manager, ctx := certificateFixture(t)
	if got := manager.routeCertificateFingerprints(ctx); len(got) != 0 {
		t.Fatal("unconfigured manager authorized material")
	}
	installSyntheticCertificate(t, manager, ctx)
	q, err := manager.servingSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := manager.GetCertificate(&tls.ClientHelloInfo{ServerName: "current." + fixtureCertificateNamespace + ".portico.direct"})
	if err != nil {
		t.Fatal(err)
	}
	first := routeCertificateFingerprint(cert)
	if got := manager.routeCertificateFingerprints(ctx); !reflect.DeepEqual(got, []string{first}) {
		t.Fatalf("binding %v, want active certificate", got)
	}
	// Stage the durable renewal state with distinct active and previous leafs,
	// retaining the same protected key/CSR as a real renewal.
	mat, err := manager.material(ctx, q.Scope, q.ActiveID)
	if err != nil {
		t.Fatal(err)
	}
	renewal := syntheticCertificate(t, manager, mat.CSR)
	const nextID = "renewed-certificate"
	_, err = f.db.Exec(`INSERT INTO networking_certificates(request_id,scope_id,key_nonce,key_cipher,csr,order_id,state,chain,not_before,not_after,renew_at,created_at)
	 SELECT ?,scope_id,key_nonce,key_cipher,csr,order_id,state,?,not_before,not_after,renew_at,created_at FROM networking_certificates WHERE request_id=?`, nextID, renewal, q.ActiveID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE networking_certificate_state SET previous_id=active_id,active_id=? WHERE scope_id=?`, nextID, q.Scope.ID); err != nil {
		t.Fatal(err)
	}
	leaf, _ := pem.Decode(renewal)
	digest := sha256.Sum256(leaf.Bytes)
	next := base64.RawURLEncoding.EncodeToString(digest[:])
	if got := manager.routeCertificateFingerprints(ctx); !reflect.DeepEqual(got, []string{next, first}) {
		t.Fatalf("binding %v, want active and previous", got)
	}
	// Cached material must not outlive the serving authority fence.
	if _, err = f.db.Exec(`UPDATE fixture_server_authority SET allowed=0`); err != nil {
		t.Fatal(err)
	}
	if got := manager.routeCertificateFingerprints(ctx); len(got) != 0 {
		t.Fatal("revoked authority retained TLS binding")
	}
}

func TestRouteProofCertificateBindingAndCompatibility(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newRouteIdentityFixture(t)
	certPath, keyPath := customCertFixture(t, "media.example.com")
	custom := customWithStaticSource(certPath, keyPath, "media.example.com")
	f.handler.custom = custom
	want := custom.routeCertificateFingerprint(context.Background())
	nonce := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		name, accept, host, extra string
		custom                    *CustomCertificate
		status                    int
		version                   string
		bound                     bool
	}{
		{"bound", "application/json; portico-route-proof=2", "media.example.com", "", custom, 200, "2", true},
		{"legacy client", "application/json", "media.example.com", "", custom, 200, "1", false},
		{"proxy", "application/json; portico-route-proof=2", "media.example.com", "", nil, 200, "2", false},
		{"wrong Host", "application/json; portico-route-proof=2", "relay.example.com", "", custom, 400, "", false},
		{"client cannot supply binding", "application/json; portico-route-proof=2", "media.example.com", `,"tlsCertificateFingerprints":["` + strings.Repeat("A", 43) + `"]`, custom, 400, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.handler.custom = tc.custom
			r := httptest.NewRequest(http.MethodPost, "https://media.example.com/v1/networking/identity-proof", bytes.NewBufferString(`{"baseUrl":"https://media.example.com","nonce":"`+nonce+`"`+tc.extra+`}`))
			r.Host = tc.host
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", tc.accept)
			r.Header.Set("X-Forwarded-TLS-Certificate-Fingerprint", strings.Repeat("A", 43))
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if tc.status != 200 {
				return
			}
			var signed struct{ Payload, Signature string }
			if err := json.Unmarshal(w.Body.Bytes(), &signed); err != nil {
				t.Fatal(err)
			}
			raw, err := base64.RawURLEncoding.DecodeString(signed.Payload)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := base64.RawURLEncoding.DecodeString(signed.Signature)
			if err != nil || !ed25519.Verify(f.key.Public().(ed25519.PublicKey), raw, sig) {
				t.Fatal("proof signature invalid", err)
			}
			var proof RouteIdentity
			if err := json.Unmarshal(raw, &proof); err != nil {
				t.Fatal(err)
			}
			if proof.Version != tc.version || proof.Nonce != nonce || proof.BaseURL != "https://media.example.com" {
				t.Fatal("proof changed identity challenge or version")
			}
			if tc.bound {
				if !reflect.DeepEqual(proof.TLSCertificateFingerprints, []string{want}) {
					t.Fatal("signed binding does not name configured certificate")
				}
			} else if bytes.Contains(raw, []byte("tlsCertificateFingerprints")) {
				t.Fatal("unbound proof must omit binding")
			}
		})
	}
}
