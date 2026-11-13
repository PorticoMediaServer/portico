package api

import (
	"net"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
)

// RemoteManagedCertificate identifies the managed certificate without its key, chain or paths.
// The own-address certificate is separately identified by RemoteAccessStatus.Certificate.
type RemoteManagedCertificate struct {
	State         string     `json:"state"`
	Name          string     `json:"name,omitempty"`
	Ready         bool       `json:"ready"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	Problem       string     `json:"problem,omitempty"`
}

// RemoteListenerFacts is the actual shared HTTP/HTTPS socket, including a loopback listener.
// It is not the public forwarded port, or an interface inferred from the request's Host.
type RemoteListenerFacts struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// ListeningOn is called only after Listen succeeds, before the listener answers requests.
func (s *Server) ListeningOn(addr net.Addr) {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		s.access.mu.Lock()
		s.access.listener = &RemoteListenerFacts{Address: tcp.AddrPort().Addr().Unmap().String(), Port: tcp.Port}
		s.access.mu.Unlock()
	}
}

func (s *Server) listenerFacts() *RemoteListenerFacts {
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	if s.access.listener == nil {
		return nil
	}
	copy := *s.access.listener
	return &copy
}

func (s *Server) managedCertificate() *RemoteManagedCertificate {
	if s.portico == nil {
		return nil
	}
	st := s.portico.Status()
	out := &RemoteManagedCertificate{State: st.State, Ready: st.CertificateReady && s.porticoZone() != "",
		ExpiresAt: st.ExpiresAt, LastAttemptAt: st.LastAttemptAt, Problem: st.LastProblem}
	if st.NameSpace != "" {
		out.Name = certclient.Name(st.NameSpace)
	}
	return out
}
