//go:build darwin || linux

package decoder

import (
	"net"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestInheritedReservationPreservesListenerCancellationAndChildPinning(t *testing.T) {
	v4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	port := v4.Addr().(*net.TCPAddr).Port
	v6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer v6.Close()
	r, err := ReserveEndpoints(v4, v6)
	if err != nil {
		t.Fatal(err)
	}
	defer r.retire()
	cmd := exec.Command("/bin/cat")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if !r.claim(v4.Addr().String(), cmd) {
		t.Fatal("claim failed")
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	for _, listener := range []*net.TCPListener{v4, v6} {
		raw, err := listener.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var flags int
		var flagErr error
		if err = raw.Control(func(fd uintptr) { flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
			t.Fatal(err)
		}
		if flagErr != nil {
			t.Fatal(flagErr)
		}
		if flags&unix.O_NONBLOCK == 0 {
			t.Fatal("child inheritance changed the shared listener to blocking mode")
		}
		accepted := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if conn != nil {
				conn.Close()
			}
			accepted <- err
		}()
		closed := make(chan error, 1)
		go func() { closed <- listener.Close() }()
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Fatal("listener close hung")
		}
		select {
		case err := <-accepted:
			if err == nil {
				t.Fatal("accept unexpectedly succeeded")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("accept did not retire")
		}
	}
	// Simulate the parent releasing its copies while the child still owns them.
	r.retire()
	addresses := map[string]*net.TCPAddr{"tcp4": {IP: net.IPv4(127, 0, 0, 1), Port: port}, "tcp6": {IP: net.IPv6loopback, Port: port}}
	for network, addr := range addresses {
		l, err := net.ListenTCP(network, addr)
		if err == nil {
			l.Close()
			t.Fatalf("%s rebound while child still owns reservation", network)
		}
	}
	stdin.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	for network, addr := range addresses {
		l, err := net.ListenTCP(network, addr)
		if err != nil {
			t.Fatalf("%s stayed reserved after child retirement: %v", network, err)
		}
		l.Close()
	}
}
