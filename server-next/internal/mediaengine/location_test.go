package mediaengine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

func TestConversionLocationKeepsOldFoldersAndUsesTheExistingPlacement(t *testing.T) {
	owner := settings.Streaming.Get(nil)
	if owner.ConversionLocation != "separate" || delivery(owner).TranscodeLocation != "disk" {
		t.Fatal(owner, delivery(owner))
	}
	if err := json.Unmarshal([]byte(`{"conversionFolder":"/chosen"}`), &owner); err != nil {
		t.Fatal(err)
	}
	if owner.ConversionLocation != "folder" {
		t.Fatal("an earlier chosen folder was lost", owner)
	}
	for _, saved := range []struct{ raw, location, folder string }{
		{`{"conversionLocation":"disk","conversionFolder":"/chosen"}`, "folder", "/chosen"},
		{`{"conversionLocation":"disk","conversionFolder":""}`, "separate", ""},
		{`{"conversionLocation":"memory","conversionFolder":"/spill"}`, "memory", "/spill"},
	} {
		previous := settings.Streaming.Get(nil)
		if err := json.Unmarshal([]byte(saved.raw), &previous); err != nil {
			t.Fatal(err)
		}
		if previous.ConversionLocation != saved.location || previous.ConversionFolder != saved.folder {
			t.Fatal("an earlier choice moved", saved.raw, previous)
		}
	}
	owner.ConversionLocation = "memory"
	if delivery(owner).TranscodeLocation != "memory" {
		t.Fatal(delivery(owner))
	}
	root := t.TempDir()
	e := &Engine{ctx: context.Background(), folder: root, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, err := e.open(root, filepath.Join(root, "no-ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	e.current = p
	t.Cleanup(func() { p.streams.Shutdown(context.Background()); p.stop() })
	if !e.ConversionFolderReady(root) || e.ConversionFolderReady(filepath.Join(root, "unknown")) {
		t.Fatal("invented readiness")
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.open(filepath.Join(root, "file"), ""); err == nil {
		t.Fatal("a non-writable output was ready")
	}
}
