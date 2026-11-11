// Package tlsport lets the server's one port answer both plain HTTP and HTTPS, with the
// certificate the owner supplies for their own domain. The certificate and its key are PEM
// files in the Keys folder of the server's data folder, named in the remote access settings.
// A new certificate in those files (a renewal, say) is picked up within a minute, without a
// restart, and the owner is warned before one expires.
package tlsport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status is what the owner is shown about their certificate.
type Status struct {
	// State is "none" (no certificate is set), "ok", or "problem".
	State string `json:"state"`
	// Problem says in plain words what is wrong, when State is "problem".
	Problem string `json:"problem,omitempty"`
	// Names are the names the certificate being served is for, and ExpiresAt when it expires.
	Names     []string   `json:"names,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// ExpiresSoon is true within the owner's warning time before ExpiresAt.
	ExpiresSoon bool `json:"expiresSoon,omitempty"`
}

// Source names the certificate to serve: the domain it must be for and its two files in the
// Keys folder. An empty Source serves none.
type Source struct {
	Domain, CertificateFile, KeyFile string
	// WarnBefore is how long before expiry the owner is warned.
	WarnBefore time.Duration
}

// Store holds the certificate being served.
type Store struct {
	keys string
	log  *slog.Logger
	now  func() time.Time

	mu      sync.RWMutex
	source  Source
	cert    *tls.Certificate
	leaf    *x509.Certificate
	stamp   string // the files' sizes and times when last read
	problem string
	warned  time.Time
}

// NewStore keeps certificates read from the folder keys.
func NewStore(keys string, log *slog.Logger, now func() time.Time) *Store {
	return &Store{keys: keys, log: log, now: now}
}

// Use sets what to serve and reads it at once.
func (s *Store) Use(src Source) {
	s.mu.Lock()
	changed := src.Domain != s.source.Domain || src.CertificateFile != s.source.CertificateFile || src.KeyFile != s.source.KeyFile
	s.source = src
	if changed {
		// A certificate for another domain, or from other files, is not this one's fallback.
		s.cert, s.leaf, s.stamp, s.problem = nil, nil, "", ""
	}
	s.mu.Unlock()
	s.Check()
}

// stampOf is what changes when either file is replaced.
func stampOf(paths ...string) string {
	var out string
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			out += fmt.Sprintf("%d/%d;", info.Size(), info.ModTime().UnixNano())
		} else {
			out += "-;"
		}
	}
	return out
}

// Check reads the files again if they changed since they were last read. A certificate that
// cannot be used is reported, and the last good one goes on being served until it expires.
func (s *Store) Check() {
	s.mu.RLock()
	src := s.source
	s.mu.RUnlock()
	if src.CertificateFile == "" {
		s.mu.Lock()
		s.cert, s.leaf, s.stamp, s.problem = nil, nil, "", ""
		s.mu.Unlock()
		return
	}
	certPath, keyPath := filepath.Join(s.keys, src.CertificateFile), filepath.Join(s.keys, src.KeyFile)
	stamp := stampOf(certPath, keyPath)
	s.mu.RLock()
	same := stamp == s.stamp
	s.mu.RUnlock()
	if same {
		return
	}
	cert, leaf, problem := load(certPath, keyPath, src, s.now())
	s.mu.Lock()
	defer s.mu.Unlock()
	if src != s.source {
		return // the owner changed it while this was reading
	}
	s.stamp, s.problem = stamp, problem
	if problem == "" {
		s.cert, s.leaf = cert, leaf
		s.log.Info("serving the certificate for your own address", "domain", src.Domain, "expires", leaf.NotAfter)
	} else {
		s.log.Warn("the certificate for your own address cannot be used", "problem", problem)
	}
}

func load(certPath, keyPath string, src Source, now time.Time) (*tls.Certificate, *x509.Certificate, string) {
	for _, f := range []struct{ path, what string }{{certPath, "certificate"}, {keyPath, "key"}} {
		if _, err := os.Stat(f.path); errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Sprintf("The %s file %s is not in the Keys folder.", f.what, filepath.Base(f.path))
		} else if err != nil {
			return nil, nil, fmt.Sprintf("The %s file %s cannot be read.", f.what, filepath.Base(f.path))
		}
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, "The certificate and key files cannot be read as a certificate and its own key in PEM form."
	}
	leaf := cert.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return nil, nil, "The certificate file cannot be read as a certificate."
		}
	}
	switch {
	case now.After(leaf.NotAfter):
		return nil, nil, fmt.Sprintf("The certificate expired on %s.", leaf.NotAfter.UTC().Format("2 January 2006"))
	case now.Before(leaf.NotBefore):
		return nil, nil, fmt.Sprintf("The certificate is not valid until %s.", leaf.NotBefore.UTC().Format("2 January 2006"))
	case leaf.VerifyHostname(src.Domain) != nil:
		return nil, nil, fmt.Sprintf("The certificate is not for %s.", src.Domain)
	}
	return &cert, leaf, ""
}

// Status says what is being served and what is wrong.
func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.source.CertificateFile == "" {
		return Status{State: "none"}
	}
	out := Status{State: "ok", Problem: s.problem}
	if s.problem != "" {
		out.State = "problem"
	}
	if s.leaf != nil {
		expires := s.leaf.NotAfter.UTC()
		out.Names, out.ExpiresAt = s.leaf.DNSNames, &expires
		out.ExpiresSoon = s.now().Add(s.source.WarnBefore).After(expires)
	}
	return out
}

// GetCertificate is for tls.Config: the owner's certificate while it is valid, otherwise none,
// and the handshake fails while plain HTTP goes on working.
func (s *Store) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cert == nil || s.now().After(s.leaf.NotAfter) {
		return nil, errors.New("this server has no certificate to offer")
	}
	return s.cert, nil
}

// Run looks for a changed certificate every minute, and says in the log once a day while the
// one being served expires soon.
func (s *Store) Run(done <-chan struct{}) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		s.Check()
		status := s.Status()
		s.mu.Lock()
		due := status.ExpiresSoon && s.now().Sub(s.warned) > 24*time.Hour
		if due {
			s.warned = s.now()
		}
		s.mu.Unlock()
		if due {
			s.log.Warn("the certificate for your own address expires soon: put a renewed one in its files", "expires", status.ExpiresAt)
		}
	}
}
