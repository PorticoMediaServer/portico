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

func ReserveEndpoints(ipv4, ipv6 *net.TCPListener) (*EndpointReservation, error) {
	if ipv4 == nil || ipv6 == nil {
		return nil, ErrInvalidConfiguration
	}
	a, ok := ipv4.Addr().(*net.TCPAddr)
	if !ok {
		return nil, ErrInvalidConfiguration
	}
	b, ok := ipv6.Addr().(*net.TCPAddr)
	if !ok {
		return nil, ErrInvalidConfiguration
	}
	if !a.IP.Equal(net.IPv4(127, 0, 0, 1)) || a.Zone != "" || !b.IP.Equal(net.IPv6loopback) || b.IP.To4() != nil || b.Zone != "" || a.Port < 1 || a.Port != b.Port {
		return nil, ErrInvalidConfiguration
	}
	first, err := duplicateEndpoint(ipv4)
	if err != nil {
		return nil, err
	}
	second, err := duplicateEndpoint(ipv6)
	if err != nil {
		if first != nil {
			first.Close()
		}
		return nil, err
	}
	files := []*os.File{}
	for _, f := range []*os.File{first, second} {
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
