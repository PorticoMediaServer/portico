package certclient

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
)

// What is kept in the Keys folder, readable by the server alone:
//   - portico.pem: the installed certificate chain followed by its key, in one file so a
//     crash can never leave a certificate beside the wrong key;
//   - portico-pending.key: the key of the certificate being ordered, until it is issued; kept
//     so a restart asks for the same order again instead of starting a new one;
//   - portico-certificate.json: the name space, and when to ask next, so a restart does not
//     reset a wait to nothing.
const (
	certFile    = "portico.pem"
	pendingFile = "portico-pending.key"
	stateFile   = "portico-certificate.json"
)

type saved struct {
	NameSpace string    `json:"nameSpace"`
	NextTryAt time.Time `json:"nextTryAt"`
}

var (
	errNotPortico = errors.New("not a Portico address of this server")
	errChain      = errors.New("the certificate Portico Services handed back is not for this server's key and name")
)

// Name is the one name the certificate covers.
func Name(nameSpace string) string { return "*." + nameSpace + "." + Zone }

func (c *Client) path(name string) string { return filepath.Join(c.o.Keys, name) }

// load reads what was kept, and serves an installed certificate at once.
func (c *Client) load() {
	if raw, err := os.ReadFile(c.path(stateFile)); err == nil {
		json.Unmarshal(raw, &c.state)
	}
	raw, err := os.ReadFile(c.path(certFile))
	if err != nil {
		return
	}
	cert, err := tls.X509KeyPair(raw, raw)
	if err != nil {
		c.o.Log.Warn("the Portico certificate in the Keys folder cannot be read; a new one will be ordered", "file", certFile)
		return
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || len(leaf.DNSNames) != 1 || (c.state.NameSpace != "" && leaf.DNSNames[0] != Name(c.state.NameSpace)) {
		c.o.Log.Warn("the Portico certificate in the Keys folder is not for this server's name space; a new one will be ordered")
		return
	}
	c.cert, c.leaf = &cert, leaf
}

func (c *Client) save() {
	raw, _ := json.Marshal(c.state)
	if err := os.MkdirAll(c.o.Keys, 0o700); err != nil {
		c.o.Log.Warn("could not make the Keys folder", "error", err)
	}
	if err := atomicfile.Write(c.path(stateFile), raw, 0o600); err != nil {
		c.o.Log.Warn("could not keep the Portico certificate's progress", "error", err)
	}
}

// pendingKey is the key of the certificate being ordered: the one kept, or a new one.
func (c *Client) pendingKey() (*ecdsa.PrivateKey, error) {
	if raw, err := os.ReadFile(c.path(pendingFile)); err == nil {
		if block, _ := pem.Decode(raw); block != nil {
			if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
				if key, ok := parsed.(*ecdsa.PrivateKey); ok && key.Curve == elliptic.P256() {
					return key, nil
				}
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if err := os.MkdirAll(c.o.Keys, 0o700); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(c.path(pendingFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// request is the signing request for the name space with the pending key: exactly the one
// name, nothing else (Portico Services refuses anything more).
func request(key *ecdsa.PrivateKey, nameSpace string) ([]byte, error) {
	return x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{Name(nameSpace)}}, key)
}

// install checks a chain Portico Services handed back and serves it at once, without a
// restart. It must be for this name, for the pending key, valid now, and verify to the
// public roots.
func (c *Client) install(chain []byte, key *ecdsa.PrivateKey, now time.Time) error {
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	both := append(bytes.TrimSpace(chain), '\n')
	both = append(both, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})...)
	cert, err := tls.X509KeyPair(both, both)
	if err != nil {
		return errChain
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != Name(c.state.NameSpace) {
		return errChain
	}
	intermediates := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		if parsed, err := x509.ParseCertificate(der); err == nil {
			intermediates.AddCert(parsed)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.o.Roots, Intermediates: intermediates, CurrentTime: now,
		DNSName: "probe." + c.state.NameSpace + "." + Zone, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return errChain
	}
	if err := atomicfile.Write(c.path(certFile), both, 0o600); err != nil {
		return err
	}
	os.Remove(c.path(pendingFile))
	c.mu.Lock()
	c.cert, c.leaf = &cert, leaf
	c.mu.Unlock()
	return nil
}
