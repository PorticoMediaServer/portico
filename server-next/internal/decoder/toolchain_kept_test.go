package decoder

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
)

// An answer kept for exactly this program and its libraries is used without asking the
// program again; one kept for other files is not.
func TestAKeptToolchainAnswerIsUsedOnlyForTheSameProgram(t *testing.T) {
	ffmpeg, err := exec.LookPath(decodertest.QualifiedFFmpeg(t))
	if err != nil {
		t.Fatal(err)
	}
	files := toolFiles(ffmpeg)
	if files == "" {
		t.Skip("this program's libraries cannot be listed here")
	}
	file := filepath.Join(t.TempDir(), "kept.json")
	keep := func(files string) {
		raw, _ := json.Marshal(keptToolchain{Files: files, Facts: ToolchainFacts{Identity: "kept", Version: "kept",
			Encoders: map[string]bool{"libx264": true}, Muxers: map[string]bool{"hls": true}}})
		if err := os.WriteFile(file, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep(files)
	if facts, err := ProbeToolchainKept(context.Background(), ffmpeg, file); err != nil || facts.Version != "kept" {
		t.Fatalf("the kept answer was not used: %q, %v", facts.Version, err)
	}
	// Another program's answer: asking is refused here by an ended context, which shows it was asked.
	keep("other files")
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if facts, err := ProbeToolchainKept(ended, ffmpeg, file); err == nil {
		t.Fatalf("an answer kept for other files was used: %q", facts.Version)
	}
}
