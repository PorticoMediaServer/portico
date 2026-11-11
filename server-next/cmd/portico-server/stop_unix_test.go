//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// With an app open, which holds the change feed for as long as it runs, the real server still
// stops at once when asked, and has the time to finish its work: put jobs back, save where
// everyone had got to, add up today's totals. It used to wait ten seconds for the feed to
// end by itself and then do that work with no time left.
func TestTheServerStopsAtOnceWithAnAppOpen(t *testing.T) {
	binary, data := filepath.Join(t.TempDir(), "portico-server"), t.TempDir()
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	var said bytes.Buffer
	server := exec.Command(binary, "-data", data, "-listen", address)
	server.Stderr = &said
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Process.Kill()

	call := func(path, token string, body, into any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		request, _ := http.NewRequest("POST", "http://"+address+path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		reply, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer reply.Body.Close()
		if reply.StatusCode/100 != 2 {
			t.Fatalf("%s: %s", path, reply.Status)
		}
		if err := json.NewDecoder(reply.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	// The owner sets the server up with the code it wrote in its data folder, and picks a profile.
	var code []byte
	for began := time.Now(); ; time.Sleep(50 * time.Millisecond) {
		reply, err := http.Get("http://" + address + "/v1/system")
		if err == nil {
			reply.Body.Close()
			if code, err = os.ReadFile(filepath.Join(data, "setup-code.txt")); err == nil {
				break
			}
		}
		if time.Since(began) > 30*time.Second {
			t.Fatalf("the server never served: %v\n%s", err, said.String())
		}
	}
	password := make([]byte, 12)
	rand.Read(password)
	var signedIn struct {
		Session  struct{ AccessToken string }
		Profiles []struct{ ID string }
	}
	call("/v1/setup/owner", "", map[string]any{"setupCode": strings.TrimSpace(string(code)), "username": "owner", "password": hex.EncodeToString(password),
		"device": map[string]string{"installationId": fmt.Sprintf("install-%027d", 1), "name": "test"}}, &signedIn)
	var selected struct{ AccessToken string }
	call("/v1/profiles/"+signedIn.Profiles[0].ID+"/select", signedIn.Session.AccessToken, struct{}{}, &selected)

	// The app's change feed, held open.
	request, _ := http.NewRequest("GET", "http://"+address+"/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+selected.AccessToken)
	feed, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Body.Close()
	if line, err := bufio.NewReader(feed.Body).ReadString('\n'); err != nil || feed.StatusCode != http.StatusOK {
		t.Fatalf("the feed did not open: %s %q %v", feed.Status, line, err)
	}

	began := time.Now()
	server.Process.Signal(syscall.SIGTERM)
	stopped := make(chan error, 1)
	go func() { stopped <- server.Wait() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("the server stopped with %v\n%s", err, said.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the server was still stopping after 5 seconds with a feed open\n%s", said.String())
	}
	// Only a stop whose work finished in its time says this.
	if !strings.Contains(said.String(), "stopped cleanly") {
		t.Fatalf("the server stopped in %s without finishing its work\n%s", time.Since(began), said.String())
	}
}
