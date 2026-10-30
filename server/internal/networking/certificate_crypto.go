package networking

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"strings"
	"time"
)

func validCertificateNamespace(ns string) bool {
	if !strings.HasPrefix(ns, "ptc-") || len(ns) != 24 {
		return false
	}
	raw, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(ns[4:]))
	return e == nil && len(raw) == 12 && "ptc-"+strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)) == ns
}
func scopeForCertificate(ctx context.Context, v Intent, environment string) (certificateScope, error) {
	lease, e := claimAuthority(ctx)
	if e != nil {
		return certificateScope{}, e
	}
	raw, e := aad(v)
	if e != nil {
		return certificateScope{}, e
	}
	// Stable across process restarts, different after root reset/restore. This
	// fingerprint is only an optimistic UI/row fence; it grants no authority.
	h := sha256.New()
	h.Write([]byte("portico/tls-scope/v1\x00" + environment + "\x00" + lease.Incarnation() + "\x00"))
	h.Write(raw)
	return certificateScope{ID: hex.EncodeToString(h.Sum(nil)), Incarnation: lease.Incarnation(), Intent: v}, nil
}
func certificateAAD(s certificateScope, request string) []byte {
	raw, _ := json.Marshal(struct{ Kind, Scope, Request string }{"portico/tls-private-key/v1", s.ID, request})
	return raw
}
func (m *CertificateManager) newMaterial(s certificateScope, namespace string) (certificateMaterial, error) {
	if m.tlsKey == nil || !validCertificateNamespace(namespace) {
		return certificateMaterial{}, errCertificateMaterial
	}
	id := make([]byte, 24)
	if _, e := rand.Read(id); e != nil {
		return certificateMaterial{}, e
	}
	name := "*." + namespace + "." + m.directDomain
	csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{}, DNSNames: []string{name}}, m.tlsKey)
	if e != nil {
		return certificateMaterial{}, e
	}
	// The private key lives in the state folder's PEM file; the row keeps the
	// CSR and, once issued, the chain. Empty (not null) key fields mark the
	// keyless format.
	return certificateMaterial{RequestID: base64.RawURLEncoding.EncodeToString(id), ScopeID: s.ID, CSR: csr, State: "pending", Nonce: []byte{}, Cipher: []byte{}}, nil
}
func (m *CertificateManager) privateKey(s certificateScope, mat certificateMaterial) (*ecdsa.PrivateKey, error) {
	if m.tlsKey == nil || mat.ScopeID != s.ID {
		return nil, errCertificateMaterial
	}
	return m.tlsKey, nil
}
func exactWildcardSAN(extensions []pkix.Extension, name string) bool {
	count := 0
	expected, e := asn1.Marshal([]asn1.RawValue{{Class: 2, Tag: 2, Bytes: []byte(name)}})
	if e != nil {
		return false
	}
	for _, ext := range extensions {
		if ext.Id.Equal(asn1.ObjectIdentifier{2, 5, 29, 17}) {
			count++
			if !bytes.Equal(ext.Value, expected) {
				return false
			}
		}
	}
	return count == 1
}
func (m *CertificateManager) validateMaterial(s certificateScope, namespace string, mat certificateMaterial, chain []byte, now time.Time) (*tls.Certificate, error) {
	if !validCertificateNamespace(namespace) || len(mat.CSR) > 8192 || len(chain) == 0 || len(chain) > 64<<10 {
		return nil, errCertificateMaterial
	}
	key, e := m.privateKey(s, mat)
	if e != nil {
		return nil, e
	}
	csr, e := x509.ParseCertificateRequest(mat.CSR)
	if e != nil || csr.CheckSignature() != nil {
		return nil, errCertificateMaterial
	}
	want := "*." + namespace + "." + m.directDomain
	pub, e := x509.MarshalPKIXPublicKey(key.Public())
	if e != nil || !bytes.Equal(pub, csr.RawSubjectPublicKeyInfo) || len(csr.Extensions) != 1 || !exactWildcardSAN(csr.Extensions, want) {
		return nil, errCertificateMaterial
	}
	var certs []*x509.Certificate
	var ders [][]byte
	for len(bytes.TrimSpace(chain)) > 0 {
		chain = bytes.TrimSpace(chain)
		if !bytes.HasPrefix(chain, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errCertificateMaterial
		}
		block, rest := pem.Decode(chain)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(certs) >= 8 {
			return nil, errCertificateMaterial
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return nil, errCertificateMaterial
		}
		certs = append(certs, cert)
		ders = append(ders, block.Bytes)
		chain = rest
	}
	if len(certs) == 0 {
		return nil, errCertificateMaterial
	}
	leaf := certs[0]
	if leaf.IsCA || !bytes.Equal(leaf.RawSubjectPublicKeyInfo, pub) || !exactWildcardSAN(leaf.Extensions, want) || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errCertificateMaterial
	}
	pool := x509.NewCertPool()
	for _, c := range certs[1:] {
		pool.AddCert(c)
	}
	if _, e = leaf.Verify(x509.VerifyOptions{Roots: m.roots, Intermediates: pool, CurrentTime: now, DNSName: "probe." + namespace + "." + m.directDomain, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
		return nil, errCertificateMaterial
	}
	return &tls.Certificate{Certificate: ders, PrivateKey: key, Leaf: leaf}, nil
}
func certificateRenewAt(request string, before, after time.Time) time.Time {
	h := sha256.Sum256([]byte(request))
	// Renew somewhere in 58–76% of the real lifetime, fixed per order and re-drawn for the next
	// one. The mean (67%) is essentially unchanged, so the certificate authority sees the same
	// number of orders a year, but a cohort issued together (a launch, a forced fleet re-issue) returns
	// spread over 18% of the lifetime (16 days of a 90-day certificate) instead of 6%, and
	// disperses further every cycle. A quarter of the lifetime always remains as margin.
	// Hosted refuses a renewal before 55% (certificates.go), so the lower bound stays above it.
	fraction := 0.58 + float64(uint16(h[0])<<8|uint16(h[1]))/65535*0.18
	return before.Add(time.Duration(float64(after.Sub(before)) * fraction))
}

// Handshake signatures recheck live local authority too. Session tickets are
// disabled by the listener: resumption cannot bypass this gate after unclaim.
type authorityTLSSigner struct {
	manager *CertificateManager
	scope   certificateScope
	request string
	key     *ecdsa.PrivateKey
}

func (s authorityTLSSigner) Public() crypto.PublicKey { return s.key.Public() }
func (s authorityTLSSigner) Sign(random io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var signature []byte
	e := s.manager.runner.Do(ctx, func(ctx context.Context) error {
		return s.manager.withServingMaterial(ctx, s.scope, s.request, func() error { var e error; signature, e = s.key.Sign(random, digest, opts); return e })
	})
	if e != nil {
		return nil, e
	}
	return signature, nil
}
