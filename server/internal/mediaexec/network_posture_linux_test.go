//go:build linux

package mediaexec

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

func TestRequiredLoopbackRejectsFilesystemOnlySandbox(t *testing.T) {
	resetForTest(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sandboxProbe = func(string) (string, bool, error) { return "bubblewrap", true, nil }
	// A cached auto posture must not bypass a subsequently required setting.
	t.Setenv("PORTICO_DECODER_SANDBOX", "auto")
	if p := Decide(executable); !p.Sandboxed {
		t.Fatal(p)
	}
	t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	job := Job{Executable: executable, Loopback: "127.0.0.1:19502"}
	if _, err = Argv(job); !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("filesystem-only sandbox accepted in required mode: %v", err)
	}
	if _, err = linuxArgv("/usr/bin/bwrap", job, true); !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("platform builder accepted host network in required mode: %v", err)
	}
}

func TestOptionalLoopbackKeepsFeatureAndReportsNetworkSeparately(t *testing.T) {
	resetForTest(t)
	t.Setenv("PORTICO_DECODER_SANDBOX", "auto")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bwrap"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	// Only construct argv. This stand-in is never executed or represented as a
	// successful real sandbox probe.
	sandboxProbe = func(string) (string, bool, error) { return "bubblewrap", true, nil }
	args, err := Argv(Job{Executable: executable, Args: []string{"input"}, Loopback: "127.0.0.1:19502"})
	if err != nil || slices.Contains(args, "--unshare-net") || args[len(args)-1] != "input" {
		t.Fatalf("optional loopback behavior changed: %v %v", args, err)
	}
	args, err = Argv(Job{Executable: executable})
	if err != nil || !slices.Contains(args, "--unshare-net") {
		t.Fatalf("ordinary file job lost network isolation: %v %v", args, err)
	}
	found := false
	for _, status := range capabilityreport.All() {
		if status.Capability == capabilityreport.DecoderNetworkIsolation {
			found = true
			if status.Available || status.Code != "decoder_network_unconfined" {
				t.Fatalf("later isolated job hid the host-network path: %+v", status)
			}
		}
	}
	if !found {
		t.Fatal("network posture missing")
	}
}
