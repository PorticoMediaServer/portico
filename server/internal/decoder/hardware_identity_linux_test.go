//go:build linux

package decoder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestHardwareCacheRefusesSameSizeSameTimeExecutableEdit(t *testing.T) {
	backend := supportedTestBackend(t)
	clearHardwareCircuit(backend)
	t.Cleanup(func() { clearHardwareCircuit(backend) })
	path := fakeFFmpeg(t)
	fail, calls := false, 0
	d := HardwareDetector{Run: func(context.Context, *exec.Cmd) error {
		calls++
		if fail {
			return errors.New("controlled changed tool cannot encode")
		}
		return nil
	}}
	before := d.Probe(context.Background(), path, backend, "controlled-runtime")
	if !before.Available {
		t.Fatal(before)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// EI_PAD is unused ELF header padding. The loader remains representable;
	// this changes bytes without changing the file's size or restored mtime.
	raw[8] ^= 1
	if err = os.WriteFile(path, raw, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	fail = true
	firstCalls := calls
	after := d.Probe(context.Background(), path, backend, "controlled-runtime")
	if after.Available || after.Identity == before.Identity || calls == firstCalls {
		t.Fatal("changed executable reused an earlier hardware verdict", before, after, calls)
	}
	if selected := d.SelectCached(path, string(backend), "controlled-runtime"); selected.Backend != BackendSoftware {
		t.Fatal("stale positive survived the current runtime selection", selected)
	}
}

func TestHardwareProbeRefusesReplacementDuringObservation(t *testing.T) {
	backend := supportedTestBackend(t)
	clearHardwareCircuit(backend)
	t.Cleanup(func() { clearHardwareCircuit(backend) })
	path := fakeFFmpeg(t)
	replaced := false
	d := HardwareDetector{Run: func(context.Context, *exec.Cmd) error {
		if !replaced {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path+".new", raw, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(path+".new", path); err != nil {
				t.Fatal(err)
			}
			replaced = true
		}
		return nil
	}}
	probe := d.Probe(context.Background(), path, backend, "controlled-runtime")
	if !replaced || probe.Available || probe.Identity != "" || probe.NativeFilters || probe.ScaleFilters || probe.ToneMapFilters || probe.DeinterlaceFilters || len(probe.OutputEncoders) != 0 || len(probe.DecodeCodecs) != 0 || len(d.cache) != 0 {
		t.Fatal("incomplete runtime observation published or cached hardware facts", probe, d.cache)
	}
}

func TestHardwareProbeRequiresNativeLoaderIdentity(t *testing.T) {
	path := t.TempDir() + "/ffmpeg"
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d := HardwareDetector{Run: func(context.Context, *exec.Cmd) error {
		t.Fatal("unbound wrapper reached hardware qualification")
		return nil
	}}
	if probe := d.Probe(context.Background(), path, supportedTestBackend(t), ""); probe.Available || probe.Identity != "" || len(d.cache) != 0 {
		t.Fatal(probe)
	}
}
