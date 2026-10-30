package networking

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// customCertFixture generates a self-signed certificate for domain and writes
// the PEM pair into a temp dir, returning both paths.
func customCertFixture(t *testing.T, domain string) (certPath, keyPath string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, e := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certOut, e := os.Create(certPath)
	if e != nil {
		t.Fatal(e)
	}
	if e := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); e != nil {
		t.Fatal(e)
	}
	if e := certOut.Close(); e != nil {
		t.Fatal(e)
	}
	raw, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	keyOut, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: raw}); e != nil {
		t.Fatal(e)
	}
	if e := keyOut.Close(); e != nil {
		t.Fatal(e)
	}
	return certPath, keyPath
}

func customWithStaticSource(certPath, keyPath, domain string) *CustomCertificate {
	c := NewCustomCertificate()
	c.SetSource(func(context.Context) (string, string, string) { return certPath, keyPath, domain })
	return c
}

func TestCustomCertificateServesMatchingName(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	c := customWithStaticSource(certPath, keyPath, "media.example.com")
	got, e := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || got == nil || got.Leaf == nil {
		t.Fatalf("matching name not served: %v %v", got, e)
	}
	if got.Leaf.VerifyHostname("media.example.com") != nil {
		t.Fatal("served leaf does not cover the domain")
	}
	// Case-insensitive server names match like TLS does.
	got, e = c.Get(&tls.ClientHelloInfo{ServerName: "MEDIA.EXAMPLE.COM"})
	if e != nil || got == nil {
		t.Fatalf("case-insensitive match not served: %v", e)
	}
	status := c.Status(context.Background())
	if status.State != "ready" || status.Domain != "media.example.com" || status.NotAfter == nil || status.Issuer == "" {
		t.Fatalf("status: %+v", status)
	}
}

func TestCustomCertificateOtherNameFallsThrough(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	c := customWithStaticSource(certPath, keyPath, "media.example.com")
	got, e := c.Get(&tls.ClientHelloInfo{ServerName: "other.example.com"})
	if e != nil || got != nil {
		t.Fatalf("another name must fall through to the manager: %v %v", got, e)
	}
	if got, e := c.Get(nil); e != nil || got != nil {
		t.Fatalf("a missing hello must fall through: %v %v", got, e)
	}
}

func TestCustomCertificateNameMismatchRejected(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	c := customWithStaticSource(certPath, keyPath, "other.example.com")
	if got, _ := c.Get(&tls.ClientHelloInfo{ServerName: "other.example.com"}); got != nil {
		t.Fatal("a leaf that does not cover the domain must not serve")
	}
	status := c.Status(context.Background())
	if status.State != "error" || status.ErrorCode != "custom_certificate_name_mismatch" {
		t.Fatalf("status: %+v", status)
	}
}

func TestCustomCertificateReloadsAfterChange(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	current := time.Now()
	c := customWithStaticSource(certPath, keyPath, "media.example.com")
	c.now = func() time.Time { return current }
	first, e := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || first == nil {
		t.Fatal(e)
	}
	// Replace the files without advancing the clock: the 30 s check has not
	// passed, so the old certificate keeps serving and no reload happens.
	secondPath, secondKey := customCertFixture(t, "media.example.com")
	secondCert, e := os.ReadFile(secondPath)
	if e != nil {
		t.Fatal(e)
	}
	secondPrivate, e := os.ReadFile(secondKey)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(certPath, secondCert, 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(keyPath, secondPrivate, 0600); e != nil {
		t.Fatal(e)
	}
	// Force a new mtime so the change is visible once the interval passes.
	future := current.Add(time.Hour)
	if e := os.Chtimes(certPath, future, future); e != nil {
		t.Fatal(e)
	}
	if e := os.Chtimes(keyPath, future, future); e != nil {
		t.Fatal(e)
	}
	still, e := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || still == nil {
		t.Fatal(e)
	}
	if string(still.Certificate[0]) != string(first.Certificate[0]) {
		t.Fatal("reloaded before the 30 s check passed")
	}
	// Past the interval the changed files reload.
	current = current.Add(31 * time.Second)
	reloaded, e := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || reloaded == nil {
		t.Fatal(e)
	}
	if string(reloaded.Certificate[0]) == string(first.Certificate[0]) {
		t.Fatal("did not reload after the files changed")
	}
	if status := c.Status(context.Background()); status.State != "ready" {
		t.Fatalf("status: %+v", status)
	}
}

func TestCustomCertificateKeepsLastGoodOnBrokenFile(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	current := time.Now()
	c := customWithStaticSource(certPath, keyPath, "media.example.com")
	c.now = func() time.Time { return current }
	first, e := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || first == nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(certPath, []byte("not a certificate"), 0644); e != nil {
		t.Fatal(e)
	}
	current = current.Add(31 * time.Second)
	kept, e := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || kept == nil {
		t.Fatalf("the last good certificate must keep serving: %v", e)
	}
	if string(kept.Certificate[0]) != string(first.Certificate[0]) {
		t.Fatal("did not keep the last good certificate")
	}
	status := c.Status(context.Background())
	if status.State != "error" || status.ErrorCode == "" {
		t.Fatalf("status: %+v", status)
	}
	if !strings.HasPrefix(status.ErrorCode, "custom_certificate_") {
		t.Fatalf("error code: %q", status.ErrorCode)
	}
}

func TestDirectListenerServesCustomWithoutManager(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	custom := customWithStaticSource(certPath, keyPath, "media.example.com")
	raw := &listenerFixture{incoming: make(chan net.Conn, 1), done: make(chan struct{})}
	_ = raw
	l, e := NewDirectListenerWithCustom(raw, nil, custom)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	got, e := l.config.GetCertificate(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if e != nil || got == nil {
		t.Fatalf("custom not served without a manager: %v", e)
	}
	if _, e := l.config.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example.com"}); e == nil {
		t.Fatal("another name behaves as before (no certificate)")
	}
	if l.config.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS minimum moved: %v", l.config.MinVersion)
	}
	if l.config.VerifyConnection == nil {
		t.Fatal("custom certificate needs a resumption validity check")
	}
	if err := l.config.VerifyConnection(tls.ConnectionState{DidResume: true, ServerName: "media.example.com"}); err != nil {
		t.Fatal(err)
	}
	custom.now = func() time.Time { return got.Leaf.NotAfter.Add(time.Second) }
	if err := l.config.VerifyConnection(tls.ConnectionState{DidResume: true, ServerName: "media.example.com"}); err == nil {
		t.Fatal("expired custom certificate resumed")
	}
}

func TestCustomCertificateStatusExpiresWithoutFileChange(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	c := customWithStaticSource(certPath, keyPath, "media.example.com")
	first, err := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return first.Leaf.NotAfter.Add(time.Second) }
	if status := c.Status(context.Background()); status.State != "error" || status.ErrorCode != "custom_certificate_expired" {
		t.Fatalf("%+v", status)
	}
	if _, err := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"}); err == nil {
		t.Fatal("expired certificate served")
	}
}

func TestCustomCertificateDetectsSameSizeAndTimestampReplacement(t *testing.T) {
	certPath, keyPath := customCertFixture(t, "media.example.com")
	now := time.Now()
	c := customWithStaticSource(certPath, keyPath, "media.example.com")
	c.now = func() time.Time { return now }
	first, err := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(certPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt PEM while preserving its length and mtime. The last good pair
	// continues serving, but status must report the replacement failure.
	for i := range raw {
		if raw[i] == 'M' {
			raw[i] = '!'
			break
		}
	}
	if err = os.WriteFile(certPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(certPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	status := c.Status(context.Background())
	if status.State != "error" || !status.Serving {
		t.Fatalf("replacement not detected: %+v", status)
	}
	got, err := c.Get(&tls.ClientHelloInfo{ServerName: "media.example.com"})
	if err != nil || !bytes.Equal(first.Certificate[0], got.Certificate[0]) {
		t.Fatal("last valid pair was lost")
	}
}
