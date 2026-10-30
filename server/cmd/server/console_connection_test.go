package main

import (
	"context"
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

func TestConsoleRemainsReachableAtConnectionLimit(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "HTTP"
		if secure {
			name = "TLS"
		}
		t.Run(name, func(t *testing.T) {
			certServer := httptest.NewTLSServer(http.NotFoundHandler())
			cert := certServer.TLS.Certificates[0]
			roots := x509.NewCertPool()
			roots.AddCert(certServer.Certificate())
			certServer.Close()
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			cap := consoleConnections(raw, 1)
			listener, err := networking.NewDirectListenerWithTLSConfig(cap, &tls.Config{Certificates: []tls.Certificate{cert}})
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/media/hold" {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
					}
					return
				}
				w.WriteHeader(200)
			})
			server := &http.Server{Handler: cap.protectConsole(h), ConnContext: cap.context, ReadHeaderTimeout: time.Second}
			defer server.Close()
			go server.Serve(listener)
			scheme := "http"
			if secure {
				scheme = "https"
			}
			origin := scheme + "://" + raw.Addr().String()
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: false}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			result := make(chan error, 1)
			go func() {
				r, e := client.Get(origin + "/v1/media/hold")
				if e == nil {
					_, _ = io.Copy(io.Discard, r.Body)
					r.Body.Close()
				}
				result <- e
			}()
			defer close(release)
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("ordinary connection did not start")
			}
			response, err := client.Get(origin + "/v1/admin/connectivity/status")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("console=%d", response.StatusCode)
			}
			response, err = client.Get(origin + "/v1/media/another")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 503 {
				t.Fatalf("ordinary request exceeded cap: %d", response.StatusCode)
			}
			// Shutdown closes the blocked request after its context is cancelled.
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
	}
}
