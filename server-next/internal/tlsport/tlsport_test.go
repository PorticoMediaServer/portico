package tlsport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert writes a self-signed certificate for name, valid until notAfter, and returns it.
func writeCert(t *testing.T, dir, name string, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(filepath.Join(dir, "certificate.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(filepath.Join(dir, "certificate.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	cert, _ := x509.ParseCertificate(der)
	return cert
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// The owner's certificate is served on the same port as plain HTTP. A renewed one in the same
// files is served without a restart; a broken or wrong one is reported in plain words while
// the last good one goes on being served; and the owner is warned before expiry.
func TestOwnCertificateOnTheOnePort(t *testing.T) {
	keys := t.TempDir()
	store := NewStore(keys, quiet, time.Now)
	src := Source{Domain: "media.example.com", CertificateFile: "certificate.pem", KeyFile: "certificate.key", WarnBefore: 14 * 24 * time.Hour}
	store.Use(src)
	if st := store.Status(); st.State != "problem" || st.Problem != "The certificate file certificate.pem is not in the Keys folder." {
		t.Fatalf("with no files the status is %+v", st)
	}
	first := writeCert(t, keys, "media.example.com", time.Now().Add(90*24*time.Hour))
	store.Check()
	if st := store.Status(); st.State != "ok" || st.ExpiresSoon || len(st.Names) != 1 {
		t.Fatalf("with a good certificate the status is %+v", st)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			io.WriteString(w, "secure")
		} else {
			io.WriteString(w, "plain")
		}
	}), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(Listen(ln, &tls.Config{GetCertificate: store.GetCertificate, MinVersion: tls.VersionTLS12}))
	t.Cleanup(func() { srv.Close() })
	addr := ln.Addr().String()

	get := func(scheme string, trust *x509.Certificate) (string, error) {
		pool := x509.NewCertPool()
		if trust != nil {
			pool.AddCert(trust)
		}
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "media.example.com"}, DisableKeepAlives: true}}
		resp, err := client.Get(scheme + "://" + addr + "/")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil
	}
	if body, err := get("http", nil); err != nil || body != "plain" {
		t.Fatalf("plain HTTP on the port: %q %v", body, err)
	}
	if body, err := get("https", first); err != nil || body != "secure" {
		t.Fatalf("HTTPS on the same port: %q %v", body, err)
	}

	// A renewal that expires within the warning time is picked up and warned about.
	time.Sleep(10 * time.Millisecond)
	renewed := writeCert(t, keys, "media.example.com", time.Now().Add(10*24*time.Hour))
	store.Check()
	if st := store.Status(); st.State != "ok" || !st.ExpiresSoon {
		t.Fatalf("after a renewal the status is %+v", st)
	}
	if body, err := get("https", renewed); err != nil || body != "secure" {
		t.Fatalf("the renewed certificate is not served: %q %v", body, err)
	}

	// A certificate for another name is refused, and the last good one stays.
	time.Sleep(10 * time.Millisecond)
	writeCert(t, keys, "other.example.com", time.Now().Add(90*24*time.Hour))
	store.Check()
	if st := store.Status(); st.State != "problem" || st.Problem != "The certificate is not for media.example.com." {
		t.Fatalf("with a certificate for another name the status is %+v", st)
	}
	if body, err := get("https", renewed); err != nil || body != "secure" {
		t.Fatalf("the last good certificate is no longer served: %q %v", body, err)
	}

	// Turning the own address off serves no certificate, and plain HTTP goes on.
	store.Use(Source{})
	if _, err := get("https", renewed); err == nil {
		t.Fatal("HTTPS still answered with no certificate set")
	}
	if body, err := get("http", nil); err != nil || body != "plain" {
		t.Fatalf("plain HTTP after HTTPS was turned off: %q %v", body, err)
	}
}
