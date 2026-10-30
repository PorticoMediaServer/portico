package trust

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestRootCertifiedRotationAndRejection(t *testing.T) {
	root, private, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	for _, id := range []string{"previous", "current"} {
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		cert, err := Certify(private, pub, id, "documents", now.Add(-time.Hour), now.Add(90*24*time.Hour), 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		env, err := Sign(key, cert, []byte("fixture"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = env.Verify(root, "documents", now); err != nil {
			t.Fatal(err)
		}
		if _, err = env.Verify(other, "documents", now); err == nil {
			t.Fatal("wrong root accepted")
		}
		if _, err = env.Verify(root, "wake", now); err == nil {
			t.Fatal("wrong purpose accepted")
		}
		if _, err = env.Verify(root, "documents", cert.NotAfter); err == nil {
			t.Fatal("expired certificate accepted")
		}
		cert.Revoked = []string{id}
		if _, err = env.Verify(root, "documents", now); err == nil {
			t.Fatal("tampered certificate accepted")
		}
		env.Certificate = nil
		if _, err = env.Verify(root, "documents", now); err == nil {
			t.Fatal("bare leaf signature accepted")
		}
	}
}
func TestCertificateLifetimeAndRevocation(t *testing.T) {
	root, private, _ := ed25519.GenerateKey(rand.Reader)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	if _, err := Certify(private, pub, "leaf", "documents", now, now.Add(MaximumCertificateLifetime+time.Hour), 1, nil); err == nil {
		t.Fatal("unbounded certificate")
	}
	if _, err := Certify(private, pub, "leaf", "documents", now, now.Add(DefaultCertificateLifetime), 1, nil); err != nil {
		t.Fatal("a one-year certificate was refused", err)
	}
	c, err := Certify(private, pub, "leaf", "documents", now.Add(-time.Hour), now.Add(time.Hour), 2, []string{"leaf"})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := Sign(key, c, []byte("fixture"))
	if _, err = e.Verify(root, "documents", now); err == nil {
		t.Fatal("revoked key accepted")
	}
}

func TestCertificateRevisionStaysWithinSharedIntegerRange(t *testing.T) {
	root, private, _ := ed25519.GenerateKey(rand.Reader)
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	for _, revision := range []uint64{MaximumCertificateRevision, MaximumCertificateRevision + 1, 1 << 63, ^uint64(0)} {
		wantValid := revision <= MaximumCertificateRevision
		cert, err := Certify(private, public, "leaf", "documents", now.Add(-time.Minute), now.Add(time.Hour), revision, nil)
		if (err == nil) != wantValid {
			t.Fatalf("issuance revision %d: %v", revision, err)
		}
		// Simulate a correctly root-signed artifact from an external ceremony.
		if cert == nil {
			cert = &Certificate{KeyID: "leaf", PublicKey: base64.RawURLEncoding.EncodeToString(public), Purpose: "documents", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Revision: revision, Revoked: []string{}}
		}
		cert.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, certificateBytes(*cert)))
		if _, err = cert.Verify(root, "documents", now); (err == nil) != wantValid {
			t.Fatalf("verification revision %d: %v", revision, err)
		}
	}
}
