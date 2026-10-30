// Package trust defines Hosted's offline-root certificate contract. It has no
// dependencies on Hosted runtime or storage and is shared with user servers.
package trust

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"time"
)

const Origin = "https://web.getportico.tv"

// A signing certificate lasts a year (Justin, 22 Sep: one renewal a year, with
// an alert 30 days ahead); the limit leaves room for the renewal overlap.
const DefaultCertificateLifetime = 365 * 24 * time.Hour
const MaximumCertificateLifetime = 400 * 24 * time.Hour

// MaximumCertificateRevision is the exact JSON integer range shared by clients
// and also fits the signed integer used by SQLite.
const MaximumCertificateRevision uint64 = 9007199254740991

// OfficialRoot is populated only by the release build script, from the owner's
// public PEM file. A release binary with no valid official root refuses to boot.
var OfficialRoot string
var ErrTrust = errors.New("untrusted Hosted signing certificate")

type Certificate struct {
	KeyID     string    `json:"keyId"`
	PublicKey string    `json:"publicKey"`
	Purpose   string    `json:"purpose"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	Revision  uint64    `json:"revision"`
	Revoked   []string  `json:"revoked"`
	Signature string    `json:"signature"`
}
type Envelope struct {
	Payload     string       `json:"payload"`
	Signature   string       `json:"signature"`
	KeyID       string       `json:"keyId"`
	Certificate *Certificate `json:"certificate,omitempty"`
}

func KeyID(key ed25519.PublicKey) string { h := sha256.Sum256(key); return hex.EncodeToString(h[:16]) }
func DefaultRoot() (ed25519.PublicKey, error) {
	raw := OfficialRoot
	if raw == "" && Development {
		raw = DevelopmentRoot
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrTrust
	}
	if IsDevelopmentRoot(key) && !Development {
		return nil, ErrTrust
	}
	return ed25519.PublicKey(key), nil
}
func IsDevelopmentRoot(key ed25519.PublicKey) bool {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:]) == "a96af130ee5a59294c90fcb8883dc7c6c3878365d970f4a6e67fa7ed2fc7ef4c"
}
func init() {
	if Release {
		if _, err := DefaultRoot(); err != nil {
			panic("release requires the official Hosted root public key at build time")
		}
	}
}
func ParsePublicPEM(raw []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(raw)
	if block == nil || len(rest) != 0 || block.Type != "PUBLIC KEY" {
		return nil, ErrTrust
	}
	value, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrTrust
	}
	key, ok := value.(ed25519.PublicKey)
	if !ok {
		return nil, ErrTrust
	}
	return key, nil
}
func certificateBytes(c Certificate) []byte {
	c.Signature = ""
	raw, _ := json.Marshal(c)
	return append([]byte("portico.hosted.signing-certificate.v1\x00"), raw...)
}
func Certify(root ed25519.PrivateKey, public ed25519.PublicKey, id, purpose string, before, after time.Time, revision uint64, revoked []string) (*Certificate, error) {
	if len(root) != 64 || len(public) != 32 || id == "" || (purpose != "documents" && purpose != "wake") || !after.After(before) || after.Sub(before) > MaximumCertificateLifetime || revision == 0 || revision > MaximumCertificateRevision {
		return nil, ErrTrust
	}
	if revoked == nil {
		revoked = []string{}
	}
	c := &Certificate{KeyID: id, PublicKey: base64.RawURLEncoding.EncodeToString(public), Purpose: purpose, NotBefore: before.UTC(), NotAfter: after.UTC(), Revision: revision, Revoked: revoked}
	c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(root, certificateBytes(*c)))
	return c, nil
}
func (c *Certificate) Verify(root ed25519.PublicKey, purpose string, now time.Time) (ed25519.PublicKey, error) {
	if c == nil || len(root) != 32 || c.KeyID == "" || len(c.KeyID) > 128 || len(c.Revoked) > 4096 || c.Revision == 0 || c.Revision > MaximumCertificateRevision || c.Purpose != purpose || now.Before(c.NotBefore) || !now.Before(c.NotAfter) || !c.NotAfter.After(c.NotBefore) || c.NotAfter.Sub(c.NotBefore) > MaximumCertificateLifetime {
		return nil, ErrTrust
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(c.Signature)
	if err != nil || !ed25519.Verify(root, certificateBytes(*c), signature) {
		return nil, ErrTrust
	}
	for _, id := range c.Revoked {
		if id == c.KeyID {
			return nil, ErrTrust
		}
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(c.PublicKey)
	if err != nil || len(key) != 32 {
		return nil, ErrTrust
	}
	return key, nil
}
func (e Envelope) Verify(root ed25519.PublicKey, purpose string, now time.Time) ([]byte, error) {
	key, err := e.Certificate.Verify(root, purpose, now)
	if err != nil || e.KeyID != e.Certificate.KeyID {
		return nil, ErrTrust
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(e.Payload)
	if err != nil || len(raw) > 1<<20 {
		return nil, ErrTrust
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(e.Signature)
	if err != nil || !ed25519.Verify(key, raw, signature) {
		return nil, ErrTrust
	}
	return raw, nil
}
func Sign(key ed25519.PrivateKey, certificate *Certificate, raw []byte) (Envelope, error) {
	if len(key) != 64 || certificate == nil || certificate.PublicKey != base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) {
		return Envelope{}, ErrTrust
	}
	return Envelope{base64.RawURLEncoding.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw)), certificate.KeyID, certificate}, nil
}
