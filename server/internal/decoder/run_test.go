package decoder

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestDecoderProcessHelper(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "decoder-process-helper" {
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	if len(args) != 3 {
		os.Exit(2)
	}
	if args[1] == "hold" {
		// A request, not a bare connection: on Linux the endpoint inside the
		// sandbox is the helper's HTTP proxy, and only a request reaches the host
		// listener behind it. The answer does not matter; reaching it does.
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
		if response, err := client.Get("http://" + args[2] + "/hold"); err == nil {
			response.Body.Close()
		}
		// Held until the test cancels it; the sleep only bounds a leaked helper.
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if args[1] == "oversize" {
		os.Stdout.WriteString(strings.Repeat("x", MaxProbeOutputBytes+1))
		os.Exit(0)
	}
	os.Exit(4)
}

func TestDecoderRunOwnsReservationThroughRetirement(t *testing.T) {
	v4, v6, res := runFixture(t)
	defer v4.Close()
	defer v6.Close()
	defer res.Close()
	endpoint, ipv6 := v4.Addr().String(), v6.Addr().String()
	cmd, err := confinedCommand(testExecutable(t), []string{"-test.run=^TestDecoderProcessHelper$", "--", "decoder-process-helper", "hold", endpoint}, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := &storage.Supervisor{Limit: 1}
	result := make(chan error, 1)
	go func() {
		_, e := runOwned(ctx, supervisor, "fixture-retirement", cmd, endpoint, res)
		result <- e
	}()
	v4.SetDeadline(time.Now().Add(2 * time.Second))
	conn, err := v4.Accept()
	if err != nil {
		t.Fatal("helper did not start", err)
	}
	conn.Close()
	v4.Close()
	v6.Close()
	if err = res.Close(); !errors.Is(err, ErrReservationInUse) {
		t.Fatal("caller released live reservation", err)
	}
	for _, address := range []string{endpoint, ipv6} {
		listener, e := net.Listen("tcp", address)
		if e == nil {
			listener.Close()
			t.Fatal("endpoint rebound before child exit")
		}
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("physical child failed to retire")
	}
	if supervisor.Active() != 0 {
		t.Fatal("run returned before permit retirement")
	}
	for _, address := range []string{endpoint, ipv6} {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			t.Fatal("endpoint still retained", e)
		}
		listener.Close()
	}
}

func TestDecoderRunRejectsExcessOutputAndNoStart(t *testing.T) {
	for _, mode := range []string{"oversize", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			v4, v6, res := runFixture(t)
			defer v4.Close()
			defer v6.Close()
			defer res.Close()
			endpoint := v4.Addr().String()
			cmd, err := confinedCommand(testExecutable(t), []string{"-test.run=^TestDecoderProcessHelper$", "--", "decoder-process-helper", "oversize", endpoint}, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			supervisor := &storage.Supervisor{Limit: 1}
			_, err = runOwned(ctx, supervisor, "fixture-output", cmd, endpoint, res)
			want := ErrProbeOutput
			if mode == "canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if supervisor.Active() != 0 || res.matches(endpoint) {
				t.Fatal("failed invocation retained authority")
			}
		})
	}
}

func runFixture(t *testing.T) (*net.TCPListener, *net.TCPListener, *EndpointReservation) {
	t.Helper()
	v4, v6 := loopbackPair(t)
	res, err := ReserveEndpoints(v4, v6)
	if err != nil {
		v4.Close()
		v6.Close()
		t.Fatal(err)
	}
	return v4, v6, res
}

// loopbackPair listens on 127.0.0.1 and [::1] at one ephemeral port, as the
// gateway does. Never a fixed port: tests run in parallel with other packages
// and other checkouts on a shared machine.
func loopbackPair(t *testing.T) (*net.TCPListener, *net.TCPListener) {
	t.Helper()
	var last error
	for range 32 {
		v4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		v6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: v4.Addr().(*net.TCPAddr).Port})
		if err == nil {
			return v4, v6
		}
		// The port is free on IPv4 but taken on IPv6; try another.
		v4.Close()
		last = err
	}
	t.Fatal("no loopback port is free on both families", last)
	return nil, nil
}
func testExecutable(t *testing.T) string {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
