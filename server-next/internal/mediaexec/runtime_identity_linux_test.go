//go:build linux

package mediaexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeIdentityRevalidatesSameSizeLibraryEdit(t *testing.T) {
	tool := copyCacheELF(t)
	extra := filepath.Join(t.TempDir(), "explicit-runtime-input")
	if err := os.WriteFile(extra, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	configured.Lock()
	prior := configured.libraries
	configured.libraries = []string{extra}
	configured.Unlock()
	t.Cleanup(func() { configured.Lock(); configured.libraries = prior; configured.Unlock() })
	before, complete, err := RuntimeIdentity(context.Background(), tool)
	if err != nil || !complete {
		t.Fatal(before, complete, err)
	}
	info, err := os.Stat(extra)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(extra, []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(extra, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, complete, err := RuntimeIdentity(context.Background(), tool)
	if err != nil || !complete || before == after {
		t.Fatal("runtime library generation was reused", before, after, complete, err)
	}
}

func TestRuntimeIdentityScriptsDoNotGrantCacheableClosure(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	identity, complete, err := RuntimeIdentity(context.Background(), tool)
	if err != nil || complete || identity == "" {
		t.Fatal(identity, complete, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	identity, complete, err = RuntimeIdentity(ctx, tool)
	if !errors.Is(err, context.Canceled) || complete || identity != "" {
		t.Fatal(identity, complete, err)
	}
}
