package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestCopyTimelineProbeRejectsSecondaryFileDemuxers(t *testing.T) {
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("actual ffprobe unavailable")
	}
	t.Setenv("PORTICO_DECODER_SANDBOX", "off")
	dir := t.TempDir()
	wav, err := os.ReadFile("../decoder/testdata/music-formats-r29/source.wav")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "other.wav")
	if err = os.WriteFile(path, wav, 0600); err != nil {
		t.Fatal(err)
	}
	input := &assets.PlaybackInput{Argument: path}
	if _, err = runProbe(context.Background(), probe, input, "-show_entries", "format=format_name", "-of", "json"); err != nil {
		t.Fatalf("ordinary media rejected: %v", err)
	}
	wrapper := filepath.Join(dir, "disguised.mkv")
	if err = os.WriteFile(wrapper, []byte("ffconcat version 1.0\nfile 'other.wav'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input.Argument = wrapper
	for _, options := range [][]string{nil, {"-f", "concat"}} {
		input.Options = options
		if _, err = runProbe(context.Background(), probe, input, "-show_entries", "format=format_name", "-of", "json"); err == nil {
			t.Fatalf("secondary-file wrapper accepted with input options %v", options)
		}
	}
}
