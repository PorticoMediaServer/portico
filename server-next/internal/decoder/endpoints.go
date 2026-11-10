package decoder

import (
	"net"
	"os"
	"os/exec"
	"sync"
)

// EndpointReservation prevents a canceled bridge's listening port from being
// rebound before an owned decoder physically exits. The caller still owns the
// original listeners. A successful reservation must be closed by physical
// retirement, not merely by the request context being canceled.
type EndpointReservation struct {
	mu       sync.Mutex
	endpoint string
	files    []*os.File
	closed   bool
	claimed  bool
}

// ReserveEndpoints holds one loopback port on IPv4 and, where the host has
// IPv6 loopback, on IPv6 too. ipv6 is nil on a host without it.
func ReserveEndpoints(ipv4, ipv6 *net.TCPListener) (*EndpointReservation, error) {
	if ipv4 == nil {
		return nil, ErrInvalidConfiguration
	}
	a, ok := ipv4.Addr().(*net.TCPAddr)
	if !ok || !a.IP.Equal(net.IPv4(127, 0, 0, 1)) || a.Zone != "" || a.Port < 1 {
		return nil, ErrInvalidConfiguration
	}
	listeners := []*net.TCPListener{ipv4}
	if ipv6 != nil {
		b, ok := ipv6.Addr().(*net.TCPAddr)
		if !ok || !b.IP.Equal(net.IPv6loopback) || b.IP.To4() != nil || b.Zone != "" || a.Port != b.Port {
			return nil, ErrInvalidConfiguration
		}
		listeners = append(listeners, ipv6)
	}
	files := []*os.File{}
	for _, listener := range listeners {
		f, err := duplicateEndpoint(listener)
		if err != nil {
			for _, held := range files {
				held.Close()
			}
			return nil, err
		}
		if f != nil {
			files = append(files, f)
		}
	}
	return &EndpointReservation{endpoint: a.String(), files: files}, nil
}

func (r *EndpointReservation) matches(endpoint string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.endpoint == endpoint
}

func (r *EndpointReservation) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	if r.claimed {
		return ErrReservationInUse
	}
	r.closed = true
	var first error
	for _, f := range r.files {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	r.files = nil
	return first
}

func (r *EndpointReservation) claim(endpoint string, cmd *exec.Cmd) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.claimed || r.endpoint != endpoint || cmd == nil {
		return false
	}
	r.claimed = true
	// The confined child also pins these ports if the server process crashes.
	// Listening remains forbidden by the decoder sandbox.
	cmd.ExtraFiles = append(cmd.ExtraFiles, r.files...)
	configureInheritedFiles(cmd)
	return true
}
func (r *EndpointReservation) retire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, file := range r.files {
		file.Close()
	}
	r.files = nil
}
