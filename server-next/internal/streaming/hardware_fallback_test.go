package streaming

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

func probeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	data := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "linux" {
		native, err := exec.LookPath("true")
		if err != nil {
			t.Fatal(err)
		}
		if data, err = os.ReadFile(native); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHardwareProbeRefusesScriptWithoutNativeLoaderIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("non-Linux identity keeps the legacy stat-only contract")
	}
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var runs int
	d := &decoder.HardwareDetector{Run: func(context.Context, *exec.Cmd) error {
		runs++
		return nil
	}}
	probe := d.Probe(context.Background(), path, decoder.BackendNVENC, "")
	if probe.Available || probe.Reason != decoder.ProbeEncodeFailed {
		t.Fatalf("script executable qualified hardware: %+v", probe)
	}
	if runs != 0 {
		t.Fatalf("unqualified executable reached the probe process %d times", runs)
	}
}
