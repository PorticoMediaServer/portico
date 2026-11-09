package decoder

import (
	"net"
	"strconv"
	"testing"
)

func TestDecoderEndpointReservationRetainsBothFamilies(t *testing.T) {
	v4, v6 := loopbackPair(t)
	defer v4.Close()
	defer v6.Close()
	endpoint, ipv6 := v4.Addr().String(), v6.Addr().String()
	other := net.JoinHostPort("127.0.0.1", strconv.Itoa(v4.Addr().(*net.TCPAddr).Port%65535+1))
	reservation, err := ReserveEndpoints(v4, v6)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	if !reservation.matches(endpoint) || reservation.matches(other) {
		t.Fatal("reservation authority mismatch")
	}
	v4.Close()
	v6.Close()
	for _, network := range []string{"tcp4", "tcp6"} {
		address := endpoint
		if network == "tcp6" {
			address = ipv6
		}
		listener, e := net.Listen(network, address)
		if e == nil {
			listener.Close()
			t.Fatalf("%s rebound before reservation retirement", network)
		}
	}
	if err = reservation.Close(); err != nil {
		t.Fatal(err)
	}
	if reservation.matches(endpoint) {
		t.Fatal("closed reservation authorizes launch")
	}
	for _, network := range []string{"tcp4", "tcp6"} {
		address := endpoint
		if network == "tcp6" {
			address = ipv6
		}
		listener, e := net.Listen(network, address)
		if e != nil {
			t.Fatalf("%s did not release after retirement: %v", network, e)
		}
		listener.Close()
	}
}
