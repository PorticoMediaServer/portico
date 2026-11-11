// Package trust is how anything signed by Portico Services is checked. An offline root key
// certifies the working signing key for a year; servers and apps are built with the root's
// public half and check every document against it without calling anyone.
//
// The formats here are kept byte for byte from the first Portico Services, so the root that
// released servers and apps already hold stays valid: testdata holds documents the old code
// produced. The package uses only the standard library; the server copies this folder as it
// is.
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

// ErrTrust is the one answer for anything that does not check out. It does not say why:
// the caller has nothing to do with the reason but refuse.
var ErrTrust = errors.New("not signed by Portico Services")

// The purpose every document signing certificate carries.
const Documents = "documents"

// MaximumCertificateLifetime leaves room for the overlap at the yearly renewal.
const MaximumCertificateLifetime = 400 * 24 * time.Hour

// certificateLabel is signed in front of a certificate. The "v1" is part of the bytes the
// root has already signed.
const certificateLabel = "portico.hosted.signing-certificate.v1\x00"

var encoding = base64.RawURLEncoding.Strict()

// Certificate is the root's statement that a signing key may sign documents between two
// dates. Revision only ever rises, and Revoked lists every signing key withdrawn so far, so
// the newest certificate a server has seen is also its list of keys to refuse.
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

// Envelope is a signed document: the payload's exact bytes, the signature over them, and
// the certificate of the key that signed. Carrying the certificate in every document is
// how a renewed or withdrawn signing key reaches everyone with no call of its own.
type Envelope struct {
	Payload     string       `json:"payload"`
	Signature   string       `json:"signature"`
	KeyID       string       `json:"keyId"`
	Certificate *Certificate `json:"certificate,omitempty"`
}

// KeyID names a signing key by the first half of its digest.
func KeyID(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:16])
}

// ParseRoot reads the root public key from its PEM file.
func ParseRoot(raw []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(raw)
	if block == nil || len(rest) != 0 || block.Type != "PUBLIC KEY" {
		return nil, ErrTrust
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrTrust
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, ErrTrust
	}
	return key, nil
}

func (c Certificate) signedBytes() []byte {
	c.Signature = ""
	raw, _ := json.Marshal(c)
	return append([]byte(certificateLabel), raw...)
}

// Certify signs a certificate with the root. Services never calls it: the root's private
// half is not on any server. It is here for the key tool and for tests.
func Certify(root ed25519.PrivateKey, public ed25519.PublicKey, purpose string, notBefore, notAfter time.Time, revision uint64, revoked []string) (*Certificate, error) {
	if len(root) != ed25519.PrivateKeySize || len(public) != ed25519.PublicKeySize || purpose == "" || revision == 0 ||
		!notAfter.After(notBefore) || notAfter.Sub(notBefore) > MaximumCertificateLifetime {
		return nil, ErrTrust
	}
	if revoked == nil {
		revoked = []string{}
	}
	c := &Certificate{KeyID: KeyID(public), PublicKey: encoding.EncodeToString(public), Purpose: purpose,
		NotBefore: notBefore.UTC(), NotAfter: notAfter.UTC(), Revision: revision, Revoked: revoked}
	c.Signature = encoding.EncodeToString(ed25519.Sign(root, c.signedBytes()))
	return c, nil
}

// Verify checks the certificate against the root at the given moment and answers the
// signing key it certifies.
func (c *Certificate) Verify(root ed25519.PublicKey, purpose string, now time.Time) (ed25519.PublicKey, error) {
	if c == nil || len(root) != ed25519.PublicKeySize || c.KeyID == "" || len(c.KeyID) > 128 || len(c.Revoked) > 4096 ||
		c.Revision == 0 || c.Purpose != purpose || now.Before(c.NotBefore) || !now.Before(c.NotAfter) ||
		c.NotAfter.Sub(c.NotBefore) > MaximumCertificateLifetime {
		return nil, ErrTrust
	}
	signature, err := encoding.DecodeString(c.Signature)
	if err != nil || !ed25519.Verify(root, c.signedBytes(), signature) {
		return nil, ErrTrust
	}
	for _, id := range c.Revoked {
		if id == c.KeyID {
			return nil, ErrTrust
		}
	}
	key, err := encoding.DecodeString(c.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrTrust
	}
	return key, nil
}

// Sign wraps a payload in an envelope.
func Sign(key ed25519.PrivateKey, certificate *Certificate, payload []byte) (Envelope, error) {
	if len(key) != ed25519.PrivateKeySize || certificate == nil ||
		certificate.PublicKey != encoding.EncodeToString(key.Public().(ed25519.PublicKey)) {
		return Envelope{}, ErrTrust
	}
	return Envelope{
		Payload:     encoding.EncodeToString(payload),
		Signature:   encoding.EncodeToString(ed25519.Sign(key, payload)),
		KeyID:       certificate.KeyID,
		Certificate: certificate,
	}, nil
}

// Verify checks the envelope against the root and answers the payload's bytes. withdrawn
// is the Revoked list of the newest certificate the caller has seen (nil if it keeps
// none): an envelope signed by a key on it is refused even though its own, older
// certificate does not say so.
func (e Envelope) Verify(root ed25519.PublicKey, now time.Time, withdrawn []string) ([]byte, error) {
	key, err := e.Certificate.Verify(root, Documents, now)
	if err != nil || e.KeyID != e.Certificate.KeyID {
		return nil, ErrTrust
	}
	for _, id := range withdrawn {
		if id == e.KeyID {
			return nil, ErrTrust
		}
	}
	payload, err := encoding.DecodeString(e.Payload)
	if err != nil || len(payload) > 1<<20 {
		return nil, ErrTrust
	}
	signature, err := encoding.DecodeString(e.Signature)
	if err != nil || !ed25519.Verify(key, payload, signature) {
		return nil, ErrTrust
	}
	return payload, nil
}
