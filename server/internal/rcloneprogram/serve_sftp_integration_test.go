//go:build rcloneintegration && linux

package rcloneprogram

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

// A throwaway direct SFTP origin, not a relay, native replacement or real
// account. The origin's SSH key/auth exist only in the runner's test scratch.
// This qualifies protocol/resource custody, not production actor/egress grants.
func TestLockedSFTPMixedOperationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	program, digest := lockedServeProgram(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	root, private := t.TempDir(), t.TempDir()
	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	modified := time.Unix(1700000000, 0).UTC()
	for _, name := range []string{"one.mkv", "two.mkv", "book.bin"} {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, payload, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(private, "fixture-host-key")
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	clear(encoded)
	clear(key)
	serverKey, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	port := backendFixturePort(t)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	authorized := filepath.Join(private, "fixture-authorized-keys")
	if err = os.WriteFile(authorized, nil, 0600); err != nil {
		t.Fatal(err)
	}
	environment := []string{"HOME=" + private, "TMPDIR=" + os.Getenv("TMPDIR"), "GOMAXPROCS=1"}
	server := exec.CommandContext(ctx, program, "serve", "sftp", root, "--addr", address, "--key", keyFile, "--authorized-keys", authorized, "--user=fixture", "--pass=fixture-only", "--read-only", "--dir-cache-time=0s", "--vfs-cache-mode=off", "--buffer-size=0", "--log-level=ERROR")
	server.Env, server.Stdout, server.Stderr = environment, io.Discard, io.Discard
	childprocess.Configure(server)
	if err = server.Start(); err != nil {
		t.Fatal("owned SFTP fixture start", err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Wait(); close(serverDone) }()
	t.Cleanup(func() {
		childprocess.Kill(server)
		select {
		case <-serverDone:
		case <-time.After(3 * time.Second):
			t.Error("owned SFTP origin did not physically exit")
		}
	})
	ready := time.Now().Add(10 * time.Second)
	for {
		client, e := ssh.Dial("tcp", address, &ssh.ClientConfig{User: "fixture", Auth: []ssh.AuthMethod{ssh.Password("fixture-only")}, HostKeyCallback: ssh.FixedHostKey(serverKey), Timeout: 200 * time.Millisecond})
		if e == nil {
			client.Close()
			break
		}
		select {
		case <-serverDone:
			t.Fatal("SFTP fixture exited before authenticated readiness")
		default:
		}
		if time.Now().After(ready) {
			t.Fatal("SFTP authenticated readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	known := filepath.Join(private, "fixture-known-hosts")
	if err = os.WriteFile(known, []byte(knownhosts.Line([]string{address}, serverKey)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Use the same locked tool for the fixture's password encoding. No source
	// owner auth or environment is read, logged or transferred to the runner.
	obscure := exec.CommandContext(ctx, program, "obscure", "fixture-only")
	obscure.Env, obscure.Stderr = environment, io.Discard
	childprocess.Configure(obscure)
	password, err := obscure.Output()
	if err != nil || len(password) > 512 {
		t.Fatal("fixture password encoding", err)
	}
	defer clear(password)
	spec, lease, _ := serveProcessFixture(t, "unused")
	spec.SourceID, spec.Connections, spec.RequestsPerSecond = "sftp-fixture", 2, 50
	spec.ApprovedProgramDigest, spec.MetadataPort = digest, backendFixturePort(t)
	spec.Remote = "media:/"
	spec.Configuration = []byte(fmt.Sprintf("[media]\ntype=sftp\nhost=127.0.0.1\nport=%d\nuser=fixture\npass=%s\nknown_hosts_file=%s\nshell_type=none\ndisable_hashcheck=true\n", port, strings.TrimSpace(string(password)), known))
	defer clear(spec.Configuration)
	spec.Enforce = func(cmd *exec.Cmd) error {
		if cmd.Path != program || spec.Remote != "media:/" {
			return ErrServeProgram
		}
		return nil // Fixture-only loopback, never production egress permission.
	}
	var socketPeak atomic.Int32
	watch, stopWatch := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			n := int32(backendFixtureSockets(port))
			for prior := socketPeak.Load(); n > prior && !socketPeak.CompareAndSwap(prior, n); prior = socketPeak.Load() {
			}
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { stopWatch(); <-watchDone }()
	process, err := StartServe(ctx, Program{Path: program}, spec, lease)
	if err != nil {
		t.Fatal("real direct SFTP source", err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := process.Wait(wait); err != ErrServeStopped {
			t.Error("SFTP source retirement", err)
		}
	})
	exerciseMixedSourceBudget(t, ctx, process, payload, modified)
	counts := process.OperationCounts()
	peak := socketPeak.Load()
	t.Logf("controlled_sftp_admission=%+v observed_upstream_socket_peak=%d", counts, peak)
	if counts.Capacity != 2 || counts.Active != 0 || counts.Peak != 2 || peak < 1 || peak > 2 {
		t.Fatal("SFTP operation/pool ceiling", counts, peak)
	}
}

func backendFixturePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// Linux's sampled established client-side sockets are an observed fact, not a
// generic transport/socket guarantee. No private endpoint or auth is returned.
func backendFixtureSockets(port int) int {
	file, err := os.Open("/proc/net/tcp")
	if err != nil {
		return 0
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return 0
	}
	suffix, count := fmt.Sprintf(":%04X", port), 0
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 3 && fields[3] == "01" && strings.HasSuffix(fields[2], suffix) {
			count++
		}
	}
	return count
}
