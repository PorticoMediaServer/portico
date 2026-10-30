//go:build rcloneintegration && linux

package rcloneprogram

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

func TestLockedSMBMixedOperationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	program, digest := lockedServeProgram(t)
	image, job := os.Getenv("PORTICO_SAMBA_TEST_IMAGE"), os.Getenv("PORTICO_SAMBA_TEST_JOB")
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(image) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`).MatchString(job) {
		t.Fatal("exact disposable Samba image and job label are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	root, private := t.TempDir(), t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	modified := time.Unix(1700000000, 0).UTC()
	for _, name := range []string{"one.mkv", "two.mkv", "book.bin"} {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, payload, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{"HOME=" + private, "TMPDIR=" + os.Getenv("TMPDIR"), "GOMAXPROCS=1"}
	command := func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env, cmd.Stderr = environment, io.Discard
		childprocess.Configure(cmd)
		return cmd
	}
	label, err := command(ctx, "image", "inspect", "--format", `{{ index .Config.Labels "portico" }}`, image).Output()
	if err != nil || strings.TrimSpace(string(label)) != "remote-sources-r40" {
		t.Fatal("Samba fixture image is not owned", err)
	}
	label, err = command(ctx, "image", "inspect", "--format", `{{ index .Config.Labels "portico.job" }}`, image).Output()
	if err != nil || strings.TrimSpace(string(label)) != job {
		t.Fatal("Samba fixture image belongs to another job", err)
	}
	port := backendFixturePort(t)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	configuration, err := os.ReadFile("testdata/smb/smb.conf")
	if err != nil || len(configuration) > 4096 || strings.Count(string(configuration), "smb ports = 445") != 1 {
		t.Fatal("bounded fixture configuration", err)
	}
	configuration = []byte(strings.Replace(string(configuration), "smb ports = 445", "smb ports = "+strconv.Itoa(port), 1))
	configPath := filepath.Join(private, "smb.conf")
	if err = os.WriteFile(configPath, configuration, 0600); err != nil {
		t.Fatal(err)
	}
	startCommand := command(ctx, "run", "--detach", "--label", "portico=remote-sources-r40", "--label", "portico.job="+job, "--read-only", "--memory=256m", "--cpus=1", "--pids-limit=128", "--network=host", "--log-driver=local", "--log-opt=max-size=32k", "--log-opt=max-file=1", "--log-opt=compress=false", "--tmpfs", "/run", "--tmpfs", "/var/lib/samba", "--tmpfs", "/var/cache/samba", "--tmpfs", "/var/log/samba", "--mount", "type=bind,source="+root+",target=/media,readonly", "--mount", "type=bind,source="+configPath+",target=/etc/samba/smb.conf,readonly", image)
	startCommand.Stderr = nil // Output retains only bounded exec.ExitError diagnostics.
	raw, err := startCommand.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			t.Logf("owned Samba container start: %s", failure.Stderr[:min(len(failure.Stderr), 16<<10)])
		}
		t.Fatal("owned Samba fixture start", err)
	}
	container := strings.TrimSpace(string(raw))
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(container) {
		t.Fatal("Samba fixture returned no exact container ID")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := command(cleanup, "rm", "--force", container).Run(); err != nil {
			t.Error("owned Samba container did not retire", err)
		}
	})
	ready := time.Now().Add(10 * time.Second)
	for {
		connection, e := net.DialTimeout("tcp4", address, 200*time.Millisecond)
		if e == nil {
			connection.Close()
			break
		}
		if time.Now().After(ready) {
			// Only this guest fixture has ever reached the listener. Its bounded
			// startup diagnostics contain no owner account or retained auth.
			logsCommand := command(ctx, "logs", "--tail", "16", container)
			logsCommand.Stderr = nil
			logs, e := logsCommand.CombinedOutput()
			if e == nil && len(logs) <= 16<<10 {
				t.Logf("owned Samba fixture startup: %s", logs)
			}
			state, e := command(ctx, "inspect", "--format", "{{.State.Status}} exit={{.State.ExitCode}}", container).Output()
			if e == nil && len(state) <= 256 {
				t.Logf("owned Samba fixture state: %s", state)
			}
			t.Fatal("Samba listener readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	spec, lease, _ := serveProcessFixture(t, "unused")
	spec.SourceID, spec.Connections, spec.RequestsPerSecond = "smb-fixture", 2, 50
	spec.ApprovedProgramDigest, spec.MetadataPort = digest, backendFixturePort(t)
	spec.Remote = "media:media"
	spec.Configuration = []byte(fmt.Sprintf("[media]\ntype=smb\nhost=127.0.0.1\nport=%d\nuser=fixture-guest\n", port))
	defer clear(spec.Configuration)
	spec.Enforce = func(cmd *exec.Cmd) error {
		if cmd.Path != program || spec.Remote != "media:media" {
			return ErrServeProgram
		}
		return nil // Exact loopback fixture, never production egress/actor authority.
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
		t.Fatal("real direct SMB source", err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := process.Wait(wait); err != ErrServeStopped {
			t.Error("SMB source retirement", err)
		}
	})
	exerciseMixedSourceBudget(t, ctx, process, payload, modified)
	counts, peak := process.OperationCounts(), socketPeak.Load()
	t.Logf("controlled_smb_admission=%+v observed_upstream_socket_peak=%d", counts, peak)
	if peak < 1 {
		t.Fatal("SMB fixture observed no upstream socket")
	}
	// No socket ceiling is asserted: upstream SMB can retain idle pool sockets.
}
