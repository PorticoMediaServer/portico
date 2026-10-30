package assets

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLocalProbesRejectSecondaryFileDemuxers(t *testing.T) {
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("actual ffprobe unavailable")
	}
	// Exercise the format boundary even when an owner has disabled the sandbox.
	t.Setenv("PORTICO_DECODER_SANDBOX", "off")
	dir := t.TempDir()
	wav, err := os.ReadFile("../decoder/testdata/music-formats-r29/source.wav")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "other.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	p := Probe{Binary: probe}
	checks := map[string]func(string) error{
		"scan": func(path string) error {
			_, err := p.InspectScan(context.Background(), path)
			return err
		},
		"inspect": func(path string) error {
			_, err := p.Inspect(context.Background(), path)
			return err
		},
		"analysis": func(path string) error {
			_, err := p.analysisOutput(context.Background(), path, 1<<20, "-show_entries", "format=format_name", "-of", "json")
			return err
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(filepath.Join(dir, "other.wav")); err != nil {
				t.Fatalf("ordinary media rejected: %v", err)
			}
			for _, filename := range []string{"wrapper.ffconcat", "disguised.mkv"} {
				path := filepath.Join(dir, filename)
				if err := os.WriteFile(path, []byte("ffconcat version 1.0\nfile 'other.wav'\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := check(path); err == nil {
					t.Fatalf("secondary-file wrapper accepted: %s", filename)
				}
			}
		})
	}
}
