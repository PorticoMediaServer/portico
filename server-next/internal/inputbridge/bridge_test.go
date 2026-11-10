package inputbridge

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
)

func TestAHostWithoutIPv6LoopbackStillServesTheSource(t *testing.T) {
	real := listenIPv6
	listenIPv6 = func(int) (*net.TCPListener, error) { return nil, syscall.EADDRNOTAVAIL }
	defer func() { listenIPv6 = real }()

	bridge, err := Open(context.Background(), bytes.NewReader([]byte("film")), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	response, err := http.Get(bridge.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if body, _ := io.ReadAll(response.Body); string(body) != "film" {
		t.Fatalf("served %q", body)
	}
}
