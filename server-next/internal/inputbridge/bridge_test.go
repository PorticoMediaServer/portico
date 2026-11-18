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

// A title in parts: the list at the bridge's address names each part's address with its
// length, each part is served there, and no other address beneath it serves anything.
func TestABridgeServesATitlesPartsAndNothingElse(t *testing.T) {
	bridge, err := OpenParts(context.Background(), []Part{{bytes.NewReader([]byte("one")), 3, 1.5}, {bytes.NewReader([]byte("two")), 3, 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	get := func(url string) (int, string) {
		response, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	want := "ffconcat version 1.0\nfile '" + bridge.PartURL(0) + "'\nduration 1.500000\nfile '" + bridge.PartURL(1) + "'\nduration 2.000000\n"
	if code, body := get(bridge.URL()); code != 200 || body != want {
		t.Fatalf("the list answered %d %q", code, body)
	}
	if code, body := get(bridge.PartURL(1)); code != 200 || body != "two" {
		t.Fatalf("part 1 answered %d %q", code, body)
	}
	for _, other := range []string{"/2", "/01", "/-1", "/", "x", "/0/"} {
		if code, _ := get(bridge.URL() + other); code != http.StatusNotFound {
			t.Errorf("%s answered %d", other, code)
		}
	}
}
