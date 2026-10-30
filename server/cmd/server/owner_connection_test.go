package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestOwnerMediaUsesOrdinaryConnections(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cap := consoleConnections(raw, 1)
	cap.setOwnerRequest(func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer owner-session" })
	started, release := make(chan struct{}), make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/media/hold" {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Handler: cap.protectConsole(h), ConnContext: cap.context, ConnState: cap.state, ReadHeaderTimeout: time.Second}
	defer server.Close()
	go server.Serve(cap)
	defer close(release)
	tr := &http.Transport{DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	base := "http://" + raw.Addr().String()
	request := func(path string) (*http.Response, error) {
		q, err := http.NewRequest("GET", base+path, nil)
		if err != nil {
			return nil, err
		}
		q.Header.Set("Authorization", "Bearer owner-session")
		return client.Do(q)
	}
	go func() {
		resp, err := request("/v1/media/hold")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("owner media did not start")
	}
	resp, err := request("/v1/media/another")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("owner media bypassed ordinary ceiling: status=%d", resp.StatusCode)
	}
	for _, path := range []string{"/v1/admin/connectivity/status", "/v1/bootstrap", "/v1/preferences", "/v1/console/registry"} {
		resp, err := request(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("console read %s: status=%d", path, resp.StatusCode)
		}
	}
}
