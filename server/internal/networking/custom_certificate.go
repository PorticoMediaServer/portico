package networking

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// CustomCertificate serves an owner-supplied PEM certificate for a custom
// domain, in front of the Hosted certificate manager. PEM only: no PKCS#12,
// no passphrase; Portico keeps plain files.
//
// The source (paths and domain) is read on every handshake and status call.
// File content is reloaded when either file changes, checked
// at most once every 30 seconds, plus immediately when the source values
// change. On any failure the last good certificate keeps serving while it
// still matches the domain. Private keys are never logged or reported.
type CustomCertificate struct {
	mu     sync.Mutex
	source func(context.Context) (certPath, keyPath, domain string)
	now    func() time.Time

	certPath, keyPath, domain string
	cert                      *tls.Certificate

	certDigest [32]byte
	keyDigest  [32]byte
	certMtime  time.Time
	certSize   int64
	certOK     bool
	keyMtime   time.Time
	keySize    int64
	keyOK      bool
	lastCheck  time.Time

	state     string
	errorCode string
	notAfter  *time.Time
	issuer    string
}

// CustomCertificateStatus describes the custom certificate for the
// connectivity report. NotAfter is nil unless a certificate loaded.
type CustomCertificateStatus struct {
	State     string     `json:"state"`
	Serving   bool       `json:"serving"`
	ErrorCode string     `json:"errorCode,omitempty"`
	Domain    string     `json:"domain,omitempty"`
	NotAfter  *time.Time `json:"notAfter,omitempty"`
	Issuer    string     `json:"issuer,omitempty"`
}

var errCustomCertificate = errors.New("custom certificate unavailable")

// customCertificateReloadInterval bounds how often handshakes stat files.
const customCertificateReloadInterval = 30 * time.Second

// NewCustomCertificate returns an unconfigured custom certificate (state none).
func NewCustomCertificate() *CustomCertificate {
	return &CustomCertificate{state: "none", now: time.Now}
}

// SetSource installs the console-settings source. A nil source disables.
func (c *CustomCertificate) SetSource(source func(context.Context) (certPath, keyPath, domain string)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.source = source
}

func (c *CustomCertificate) nowOrDefault() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *CustomCertificate) currentSource(ctx context.Context) (certPath, keyPath, domain string) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return "", "", ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return source(ctx)
}

// Get returns the loaded certificate when the handshake's server name matches
// the configured domain, and nil, nil otherwise (fall through to the manager).
// A matching name with no usable certificate is an error, so the handshake
// fails rather than falling through to a manager that cannot serve it either.
func (c *CustomCertificate) Get(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c == nil {
		return nil, nil
	}
	var helloName string
	var helloCtx context.Context
	if hello != nil {
		helloName = hello.ServerName
		helloCtx = hello.Context()
	}
	certPath, keyPath, domain := c.currentSource(helloCtx)
	if domain == "" || certPath == "" || keyPath == "" {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.domain != "" || c.certPath != "" || c.keyPath != "" {
			c.resetLocked()
		}
		return nil, nil
	}
	if !strings.EqualFold(helloName, domain) {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked(certPath, keyPath, domain)
	if c.cert != nil && c.cert.Leaf != nil && c.cert.Leaf.VerifyHostname(domain) == nil && c.nowOrDefault().Before(c.cert.Leaf.NotAfter) {
		return c.cert, nil
	}
	return nil, errCustomCertificate
}

// Status refreshes the cached state (throttled like Get) and reports it.
func (c *CustomCertificate) Status(ctx context.Context) CustomCertificateStatus {
	if c == nil {
		return CustomCertificateStatus{State: "none"}
	}
	certPath, keyPath, domain := c.currentSource(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if domain == "" || certPath == "" || keyPath == "" {
		if c.domain != "" || c.certPath != "" || c.keyPath != "" {
			c.resetLocked()
		}
		return c.statusLocked()
	}
	c.ensureLocked(certPath, keyPath, domain)
	if c.cert != nil && c.cert.Leaf != nil && !c.nowOrDefault().Before(c.cert.Leaf.NotAfter) {
		c.state, c.errorCode = "error", "custom_certificate_expired"
	}
	return c.statusLocked()
}

// Serves reports whether the cached configuration serves a name, without file
// I/O. It lets the resumed-session fence skip claim checks for custom names.
func (c *CustomCertificate) Serves(name string) bool {
	if c == nil || name == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.domain != "" && strings.EqualFold(name, c.domain)
}

func (c *CustomCertificate) resetLocked() {
	c.certPath, c.keyPath, c.domain = "", "", ""
	c.cert = nil
	c.certOK, c.keyOK = false, false
	c.state, c.errorCode = "none", ""
	c.notAfter, c.issuer = nil, ""
}

func (c *CustomCertificate) statusLocked() CustomCertificateStatus {
	out := CustomCertificateStatus{Serving: c.cert != nil && c.cert.Leaf != nil && c.nowOrDefault().Before(c.cert.Leaf.NotAfter), State: c.state, ErrorCode: c.errorCode, Domain: c.domain, Issuer: c.issuer}
	if out.State == "" {
		out.State = "none"
	}
	if c.notAfter != nil {
		cp := *c.notAfter
		out.NotAfter = &cp
	}
	return out
}

// ensureLocked reloads immediately when the source values changed, otherwise
// stats files at most once per interval and reloads when they changed.
func (c *CustomCertificate) ensureLocked(certPath, keyPath, domain string) {
	if certPath != c.certPath || keyPath != c.keyPath || !strings.EqualFold(domain, c.domain) {
		c.reloadLocked(certPath, keyPath, domain)
		return
	}
	now := c.nowOrDefault()
	if now.Sub(c.lastCheck) < customCertificateReloadInterval {
		if c.state == "" {
			c.state = "none"
		}
		return
	}
	c.lastCheck = now
	certMtime, certSize, certDigest, certOK := statIdentity(certPath)
	keyMtime, keySize, keyDigest, keyOK := statIdentity(keyPath)
	if certDigest == c.certDigest && keyDigest == c.keyDigest && certOK == c.certOK && keyOK == c.keyOK && certMtime.Equal(c.certMtime) && certSize == c.certSize && keyMtime.Equal(c.keyMtime) && keySize == c.keySize {
		return
	}
	c.reloadLocked(certPath, keyPath, domain)
}

const maxCertificatePEMBytes = 1 << 20

func certificatePEM(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCertificatePEMBytes {
		return nil, errors.New("certificate file must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxCertificatePEMBytes+1))
	if err != nil || len(raw) > maxCertificatePEMBytes {
		clear(raw)
		return nil, errors.New("certificate file exceeds its bound")
	}
	return raw, nil
}
func statIdentity(path string) (time.Time, int64, [32]byte, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, 0, [32]byte{}, false
	}
	raw, err := certificatePEM(path)
	if err != nil {
		return fi.ModTime(), fi.Size(), [32]byte{}, false
	}
	defer clear(raw)
	return fi.ModTime(), fi.Size(), sha256.Sum256(raw), true
}

// reloadLocked loads the files and records state. On failure the last good
// certificate keeps serving while it still covers the new domain.
func (c *CustomCertificate) reloadLocked(certPath, keyPath, domain string) {
	now := c.nowOrDefault()
	certMtime, certSize, certDigest, certOK := statIdentity(certPath)
	keyMtime, keySize, keyDigest, keyOK := statIdentity(keyPath)
	// Keep the pre-load fingerprint: a replacement during the load must trigger
	// another check, never make an older loaded pair look like the new files.
	cert, leaf, code, e := loadCustomCertificate(certPath, keyPath, domain, now)
	previous := c.cert
	c.certPath, c.keyPath, c.domain = certPath, keyPath, domain
	c.certMtime, c.certSize, c.certOK = certMtime, certSize, certOK
	c.certDigest, c.keyDigest = certDigest, keyDigest
	c.keyMtime, c.keySize, c.keyOK = keyMtime, keySize, keyOK
	c.lastCheck = now
	if e != nil {
		c.state = "error"
		c.errorCode = code
		if previous != nil && previous.Leaf != nil && previous.Leaf.VerifyHostname(domain) == nil && now.Before(previous.Leaf.NotAfter) {
			c.cert = previous
			when := previous.Leaf.NotAfter
			c.notAfter = &when
			c.issuer = issuerName(previous.Leaf)
		} else {
			c.cert = nil
			c.notAfter = nil
			c.issuer = ""
		}
		return
	}
	c.cert = cert
	c.state = "ready"
	c.errorCode = ""
	when := leaf.NotAfter
	c.notAfter = &when
	c.issuer = issuerName(leaf)
}

func issuerName(leaf *x509.Certificate) string {
	if leaf == nil {
		return ""
	}
	if leaf.Issuer.CommonName != "" {
		return leaf.Issuer.CommonName
	}
	return leaf.Issuer.String()
}

// loadCustomCertificate loads a PEM pair and rejects it when the leaf does
// not cover the domain or is expired.
func loadCustomCertificate(certPath, keyPath, domain string, now time.Time) (*tls.Certificate, *x509.Certificate, string, error) {
	certPEM, certErr := certificatePEM(certPath)
	if certErr != nil {
		return nil, nil, "custom_certificate_unreadable", certErr
	}
	defer clear(certPEM)
	keyPEM, keyErr := certificatePEM(keyPath)
	if keyErr != nil {
		return nil, nil, "custom_certificate_unreadable", keyErr
	}
	defer clear(keyPEM)
	pair, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		if _, s := os.Stat(certPath); s != nil {
			return nil, nil, "custom_certificate_unreadable", e
		}
		if _, s := os.Stat(keyPath); s != nil {
			return nil, nil, "custom_certificate_unreadable", e
		}
		return nil, nil, "custom_certificate_invalid", e
	}
	if len(pair.Certificate) == 0 {
		return nil, nil, "custom_certificate_invalid", errors.New("custom certificate is empty")
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return nil, nil, "custom_certificate_invalid", e
	}
	if !now.Before(leaf.NotAfter) {
		return nil, leaf, "custom_certificate_expired", errors.New("custom certificate is expired")
	}
	if now.Before(leaf.NotBefore) {
		return nil, leaf, "custom_certificate_invalid", errors.New("custom certificate is not yet valid")
	}
	if e := leaf.VerifyHostname(domain); e != nil {
		return nil, leaf, "custom_certificate_name_mismatch", e
	}
	pair.Leaf = leaf
	out := pair
	return &out, leaf, "", nil
}
