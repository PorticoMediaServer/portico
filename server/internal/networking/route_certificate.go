package networking

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"time"
)

// Native Android, iOS and tvOS hash the leaf certificate DER (not SPKI),
// using SHA-256 and unpadded base64url. Only server-owned TLS material belongs
// here: neither request headers nor the proof body can authorize a certificate.
func routeCertificateFingerprint(cert *tls.Certificate) string {
	if cert == nil || len(cert.Certificate) == 0 {
		return ""
	}
	digest := sha256.Sum256(cert.Certificate[0])
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (m *CertificateManager) routeCertificateFingerprints(ctx context.Context) []string {
	if m == nil || m.bootError != "" {
		return nil
	}
	var fingerprints []string
	// Use the same authority fence, durable active/previous generations and
	// material validation as GetCertificate. Include the renewal fallback too.
	if err := m.runner.Do(ctx, func(ctx context.Context) error {
		q, err := m.servingSnapshot(ctx)
		if err != nil {
			return err
		}
		for _, id := range []string{q.ActiveID, q.PreviousID} {
			if id == "" {
				continue
			}
			cert, err := m.cachedMaterial(ctx, q, id)
			if err != nil || !time.Now().Before(cert.Leaf.NotAfter) {
				continue
			}
			fingerprint := routeCertificateFingerprint(cert)
			if len(fingerprints) == 0 || fingerprints[0] != fingerprint {
				fingerprints = append(fingerprints, fingerprint)
			}
		}
		return nil
	}); err != nil {
		return nil
	}
	return fingerprints
}

func (c *CustomCertificate) routeCertificateFingerprint(ctx context.Context) string {
	if c == nil {
		return ""
	}
	certPath, keyPath, domain := c.currentSource(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if domain == "" || certPath == "" || keyPath == "" {
		return ""
	}
	// Refresh exactly as the listener does, including last-good material when
	// replacement files are invalid. Never hash a file that TLS would not serve.
	c.ensureLocked(certPath, keyPath, domain)
	if c.cert == nil || c.cert.Leaf == nil || c.cert.Leaf.VerifyHostname(domain) != nil || !c.nowOrDefault().Before(c.cert.Leaf.NotAfter) {
		return ""
	}
	return routeCertificateFingerprint(c.cert)
}
