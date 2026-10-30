//go:build rcloneintegration && linux

package rcloneprogram

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

// A native DAV origin served by the same locked upstream release. This is a
// controlled protocol/resource test, not production egress or actor authority.
func TestLockedDAVMixedOperationBudget(t *testing.T) {
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
	port := backendFixturePort(t)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	environment := []string{"HOME=" + private, "TMPDIR=" + os.Getenv("TMPDIR"), "GOMAXPROCS=1", "RCLONE_USER=fixture", "RCLONE_PASS=fixture-only"}
	origin := exec.CommandContext(ctx, program, "serve", "webdav", root, "--addr", address, "--read-only", "--dir-cache-time=0s", "--vfs-cache-mode=off", "--buffer-size=0", "--log-level=ERROR")
	origin.Env, origin.Stdout, origin.Stderr = environment, io.Discard, io.Discard
	childprocess.Configure(origin)
	if err := origin.Start(); err != nil {
		t.Fatal("owned DAV fixture start", err)
	}
	originDone := make(chan error, 1)
	go func() { originDone <- origin.Wait(); close(originDone) }()
	t.Cleanup(func() {
		childprocess.Kill(origin)
		select {
		case <-originDone:
		case <-time.After(3 * time.Second):
			t.Error("owned DAV origin did not physically exit")
		}
	})
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 200 * time.Millisecond}
	defer client.CloseIdleConnections()
	ready := time.Now().Add(10 * time.Second)
	for {
		request, err := http.NewRequestWithContext(ctx, "PROPFIND", "http://"+address+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.SetBasicAuth("fixture", "fixture-only")
		request.Header.Set("Depth", "0")
		response, err := client.Do(request)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			if response.StatusCode == http.StatusMultiStatus {
				break
			}
		}
		select {
		case <-originDone:
			t.Fatal("DAV fixture exited before authenticated readiness")
		default:
		}
		if time.Now().After(ready) {
			t.Fatal("DAV authenticated readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	client.CloseIdleConnections()
	obscure := exec.CommandContext(ctx, program, "obscure", "fixture-only")
	obscure.Env, obscure.Stderr = environment, io.Discard
	childprocess.Configure(obscure)
	password, err := obscure.Output()
	if err != nil || len(password) > 512 {
		t.Fatal("fixture password encoding", err)
	}
	defer clear(password)
	spec, lease, _ := serveProcessFixture(t, "unused")
	spec.SourceID, spec.Connections, spec.RequestsPerSecond = "dav-fixture", 2, 50
	spec.ApprovedProgramDigest, spec.MetadataPort = digest, backendFixturePort(t)
	spec.Remote = "media:/"
	spec.Configuration = []byte(fmt.Sprintf("[media]\ntype=webdav\nurl=http://%s/\nvendor=other\nuser=fixture\npass=%s\n", address, strings.TrimSpace(string(password))))
	defer clear(spec.Configuration)
	spec.Enforce = func(cmd *exec.Cmd) error {
		if cmd.Path != program || spec.Remote != "media:/" {
			return ErrServeProgram
		}
		return nil // This exact loopback fixture supplies no production grant.
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
		t.Fatal("real DAV source", err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := process.Wait(wait); err != ErrServeStopped {
			t.Error("DAV source retirement", err)
		}
	})
	exerciseMixedSourceBudget(t, ctx, process, payload, modified)
	counts, peak := process.OperationCounts(), socketPeak.Load()
	t.Logf("controlled_dav_admission=%+v observed_upstream_socket_peak=%d", counts, peak)
	if peak < 1 {
		t.Fatal("DAV fixture observed no upstream socket")
	}
	// HTTP's idle sockets and rclone's hidden retry/pacer are recorded as facts;
	// the Connections promise is the shared admitted-operation count above.
}
