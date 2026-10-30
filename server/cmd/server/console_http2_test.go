package main

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"testing"
	"time"
)

func TestConsoleHTTP2FirstRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := httptest.NewTLSServer(http.NotFoundHandler())
	cert := f.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(f.Certificate())
	f.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cap := consoleConnections(raw, 1)
	direct, err := networking.NewDirectListenerWithTLSConfig(cap, &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: cap.protectConsole(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })), ConnContext: cap.context, ConnState: cap.state, ReadHeaderTimeout: time.Second, HTTP2: serverHTTP2Policy()}
	defer server.Close()
	go server.Serve(direct)
	tr := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	resp, err := client.Get("https://" + raw.Addr().String() + "/v1/admin/connectivity/status")
	if err != nil {
		t.Fatalf("fresh HTTP/2 console request fails even below capacity: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.ProtoMajor != 2 {
		t.Fatalf("status=%d proto=%s", resp.StatusCode, resp.Proto)
	}
	// The first response leaves a reserve connection idle, not an ordinary one.
	deadline := time.Now().Add(reserveIdleTimeout + time.Second)
	for cap.held.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cap.held.Load() != 0 {
		t.Fatal("idle HTTP/2 reserve connection was not reclaimed")
	}
}
