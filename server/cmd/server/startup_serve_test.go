//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CS-15: the socket is served from bind onwards by one http.Server. A client
// that connects while the server is still starting gets the honest 503, and the
// same keep-alive connection is answered by the real handler once it is ready.
func TestStartingServerAnswersFromBindOnOneConnection(t *testing.T) {
	if os.Getenv("PORTICO_STARTING_HELPER") == "1" {
		t.Skip("parent only")
	}
	if testing.Short() {
		t.Skip("launches the server")
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestStartingServerHelper$", "-test.v")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PORTICO_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PORTICO_STARTING_HELPER=1", "PORTICO_DATA_DIR="+filepath.Join(root, "state"), "HOME="+root,
		fmt.Sprintf("PORTICO_BIND=127.0.0.1:%d", freePort(t)), "PORTICO_DISCOVERY=0")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("isolated starting server: %v\n%s", e, output)
	}
}

func TestStartingServerHelper(t *testing.T) {
	if os.Getenv("PORTICO_STARTING_HELPER") != "1" {
		t.Skip("isolated subprocess only")
	}
	base := "http://" + os.Getenv("PORTICO_BIND")
	transport := &http.Transport{MaxIdleConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	// An ordinary route keeps its HTTP/1.1 connection open; console routes ask
	// for a fresh one, which would hide whether the connection survived.
	ask := func() (status int, code string, reused bool, err error) {
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
		request, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), "GET", base+"/v1/items", nil)
		if err != nil {
			return 0, "", false, err
		}
		response, err := client.Do(request)
		if err != nil {
			return 0, "", false, err
		}
		defer response.Body.Close()
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		raw, _ := io.ReadAll(response.Body)
		_ = json.Unmarshal(raw, &body)
		return response.StatusCode, body.Error.Code, reused, nil
	}
	starting := make(chan error, 1)
	held := false
	startupPhaseHook = func(name string) {
		// The instance lock is the first phase after bind: nothing has opened the
		// database yet, so whatever answers here is the startup reporter.
		if name != "instance" || held {
			return
		}
		held = true
		status, code, _, err := ask()
		if err == nil && (status != http.StatusServiceUnavailable || code != "starting") {
			err = fmt.Errorf("during startup: status %d, code %q", status, code)
		}
		starting <- err
	}
	ready := make(chan struct{})
	serverReadyHook = func() { close(ready) }
	checked := make(chan error, 1)
	go func() {
		defer func() {
			if process, e := os.FindProcess(os.Getpid()); e == nil {
				_ = process.Signal(os.Interrupt)
			}
		}()
		select {
		case <-ready:
		case <-time.After(2 * time.Minute):
			checked <- fmt.Errorf("server did not finish startup")
			return
		}
		status, code, reused, err := ask()
		switch {
		case err != nil:
		case !reused:
			err = fmt.Errorf("the connection opened during startup was not kept")
		case status == http.StatusServiceUnavailable && code == "starting":
			err = fmt.Errorf("the real handler never replaced the startup reporter")
		}
		checked <- err
	}()
	if e := run(); e != nil {
		t.Fatal(e)
	}
	for _, result := range []chan error{starting, checked} {
		select {
		case e := <-result:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("startup observation did not finish")
		}
	}
}
