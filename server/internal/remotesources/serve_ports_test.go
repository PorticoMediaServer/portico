package remotesources

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
)

// Reuse the lowest free port immediately, as the OS may after a listener closes.
// Keeping both listeners alive is the only way to obtain two distinct ports.
type reusableServePorts struct {
	live          map[int]bool
	calls, failAt int
}

func (p *reusableServePorts) listen(network, address string) (net.Listener, error) {
	p.calls++
	if p.calls == p.failAt {
		return nil, errServePortFixture
	}
	if network != "tcp4" || address != "127.0.0.1:0" {
		return nil, errors.New("serve listener must be ephemeral IPv4 loopback")
	}
	for port := 12345; port < 12347; port++ {
		if !p.live[port] {
			p.live[port] = true
			return &reusableServeListener{ports: p, port: port}, nil
		}
	}
	return nil, errors.New("no fixture ports available")
}

var errServePortFixture = errors.New("serve port fixture refused listen")

type reusableServeListener struct {
	ports *reusableServePorts
	port  int
}

func (l *reusableServeListener) Accept() (net.Conn, error) { return nil, errors.New("unused") }
func (l *reusableServeListener) Close() error              { delete(l.ports.live, l.port); return nil }
func (l *reusableServeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: l.port}
}

func TestServePortPairAcceptsImmediatePortReuseAtTheCommandBoundary(t *testing.T) {
	ports := &reusableServePorts{live: map[int]bool{}}
	port, metadata, err := privateServePorts(ports.listen)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports.live) != 0 {
		t.Fatal("port reservations survived allocation", ports.live)
	}
	private, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := rcloneprogram.ServeCommandSpec{
		SourceID: "fixture", SourceGeneration: "1", PrivateDirectory: private,
		Configuration: []byte("[native]\ntype=sftp\nhost=192.168.42.7\nport=2222\n"), Remote: "native:/media",
		Port: port, MetadataPort: metadata, Username: "fixture", Password: "fixture-only",
		Connections: 1, RequestsPerSecond: 1, Enforce: func(*exec.Cmd) error { return nil },
	}
	program := rcloneprogram.Program{Path: "/unused/rclone", Included: true}
	prepared, err := rcloneprogram.PrepareServe(context.Background(), program, spec)
	if err != nil {
		t.Fatalf("allocated ports rejected at serve boundary: %v (data=%d metadata=%d)", err, port, metadata)
	}
	t.Cleanup(func() {
		if err := prepared.Loaded(); err != nil {
			t.Error(err)
		}
	})
	// The command boundary must continue refusing an actually invalid pair.
	spec.MetadataPort = spec.Port
	if _, err := rcloneprogram.PrepareServe(context.Background(), program, spec); !errors.Is(err, rcloneprogram.ErrServeConfiguration) {
		t.Fatal("duplicate serve ports accepted", err)
	}
}

func TestServePortPairClosesReservationsWhenEitherListenFails(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		ports := &reusableServePorts{live: map[int]bool{}, failAt: failAt}
		port, metadata, err := privateServePorts(ports.listen)
		if !errors.Is(err, errServePortFixture) || port != 0 || metadata != 0 {
			t.Fatalf("listen %d: pair=%d,%d err=%v", failAt, port, metadata, err)
		}
		if len(ports.live) != 0 {
			t.Fatalf("listen %d leaked a reservation: %v", failAt, ports.live)
		}
	}
}
