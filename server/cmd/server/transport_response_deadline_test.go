package main

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
)

// A socket write deadline cannot detect a peer that keeps reading HTTP/2
// frames but gives the response no DATA flow-control window. Exercise the real
// router/admission/gzip stack with the production 15-second response budget.
func TestHTTP2AdmissionJSONFlowStallReleasesAndRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const path = "/v1/networking/identity-proof"
	entered := make(chan struct{}, 1)
	returned := make(chan struct{}, 1)
	routes := httpapi.New(httpapi.Dependencies{RouteIdentity: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"proof": strings.Repeat("test response data ", 8192)})
	})})
	server := transportTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes.ServeHTTP(w, r)
		if r.URL.Path == path {
			returned <- struct{}{}
		}
	}), time.Second)
	address := strings.TrimPrefix(server.URL, "https://")
	client, err := tls.Dial("tcp", address, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}) // local httptest certificate
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(22 * time.Second))
	if _, err = io.WriteString(client, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// SETTINGS_INITIAL_WINDOW_SIZE=0: headers and control frames still flow,
	// while every response DATA frame must wait for permission from this peer.
	if err = writeTestHTTP2Frame(client, 4, 0, 0, []byte{0, 4, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	for {
		typ, flags, _, _, err := readTestHTTP2Frame(client)
		if err != nil {
			t.Fatal(err)
		}
		if typ == 4 && flags == 0 {
			if err = writeTestHTTP2Frame(client, 4, 1, 0, nil); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	// Unindexed literals deliberately avoid depending on an HPACK library.
	// Static POST, https, literal :path and :authority; accept-encoding:gzip.
	headers := append([]byte{0x83, 0x87, 0x04, byte(len(path))}, path...)
	headers = append(headers, 0x01, byte(len(address)))
	headers = append(headers, address...)
	headers = append(headers, 0x0f, 0x01, 0x04, 'g', 'z', 'i', 'p')
	if err = writeTestHTTP2Frame(client, 1, 5, 1, headers); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach the admitted JSON handler")
	}
	start := time.Now()
	// Continue reading control/HEADERS frames, so the socket remains writable.
	// Only the per-response deadline can reclaim this stalled stream.
	for {
		typ, _, stream, payload, err := readTestHTTP2Frame(client)
		if err != nil {
			t.Fatal(err)
		}
		if typ == 0 && stream == 1 {
			t.Fatal("response DATA bypassed the zero flow-control window")
		}
		if typ == 3 && stream == 1 {
			if len(payload) != 4 || binary.BigEndian.Uint32(payload) != 2 {
				t.Fatalf("stalled response reset = %x", payload)
			}
			break
		}
	}
	if elapsed := time.Since(start); elapsed < 14*time.Second || elapsed > 20*time.Second {
		t.Fatalf("production response deadline elapsed %s", elapsed)
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("response deadline did not release the admitted handler")
	}
	// Restore flow control and reuse this exact HTTP/2 connection. A deadline
	// must retire only the stalled request and must not poison subsequent work.
	client.SetDeadline(time.Now().Add(3 * time.Second))
	if err = writeTestHTTP2Frame(client, 4, 0, 0, []byte{0, 4, 0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	recoveryPath := "/health/live"
	headers = append([]byte{0x82, 0x87, 0x04, byte(len(recoveryPath))}, recoveryPath...)
	headers = append(headers, 0x01, byte(len(address)))
	headers = append(headers, address...)
	if err = writeTestHTTP2Frame(client, 1, 5, 3, headers); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for {
		typ, flags, stream, payload, err := readTestHTTP2Frame(client)
		if err != nil {
			t.Fatal(err)
		}
		if stream != 3 {
			continue
		}
		if typ == 3 {
			t.Fatalf("recovery request was reset: %x", payload)
		}
		if typ == 0 {
			body = append(body, payload...)
		}
		if (typ == 0 || typ == 1) && flags&1 != 0 {
			break
		}
	}
	var health struct {
		Alive bool `json:"alive"`
	}
	if err = json.Unmarshal(body, &health); err != nil || !health.Alive {
		t.Fatalf("same-connection recovery: body=%q error=%v", body, err)
	}
}
