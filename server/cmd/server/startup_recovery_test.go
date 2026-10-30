package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStartupFailurePageStaysReachable(t *testing.T) {
	reporter := newStartupReporter()
	done := reporter.step("database", "Open the database")
	problem := errors.New("permission denied: /private/library?token=secret")
	done(problem)
	reporter.fail(problem)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() { serveStartupRecovery(ctx, listener, reporter); close(stopped) }()
	request, _ := http.NewRequest("GET", "http://"+listener.Addr().String()+"/", nil)
	request.Header.Set("Accept", "text/html")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || !strings.Contains(string(body), "permission") || !strings.Contains(string(body), "restart") || strings.Contains(string(body), "secret") {
		t.Fatalf("recovery=%d %s", response.StatusCode, body)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("recovery failed to stop")
	}
}

func TestRecoveryCopyMatchesStringCatalogue(t *testing.T) {
	source, err := os.ReadFile("../../../packages/i18n/src/catalog/en-US/settings.ts")
	if err != nil {
		t.Fatal(err)
	}
	for id, message := range startupCopy {
		key, _ := json.Marshal(id)
		value, _ := json.Marshal(message)
		if !strings.Contains(string(source), string(key)+": "+string(value)) {
			t.Errorf("recovery copy %s is missing or differs from the catalogue", id)
		}
	}
}
