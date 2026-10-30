package main

import (
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"
)

func TestStartupWithoutInstanceLockPreservesTempAndCache(t *testing.T) {
	l := storagepaths.At(filepath.Join(t.TempDir(), "data"))
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.Temp, l.Cache} {
		if err := os.WriteFile(filepath.Join(dir, "active-work"), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RequestCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	if err := resetStartupWork(l, false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.Temp, l.Cache} {
		if raw, err := os.ReadFile(filepath.Join(dir, "active-work")); err != nil || string(raw) != "preserve" {
			t.Fatal("unguarded startup deleted work", dir, err)
		}
	}
	if err := resetStartupWork(l, true); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.Temp, l.Cache} {
		if _, err := os.Stat(filepath.Join(dir, "active-work")); !os.IsNotExist(err) {
			t.Fatal("lock-owning startup retained work", dir, err)
		}
	}
}
