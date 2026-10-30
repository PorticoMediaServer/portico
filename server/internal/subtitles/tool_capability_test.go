package subtitles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredExtractionToolIsRetained(t *testing.T) {
	f := newSubtitleFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{binary, filepath.Join(t.TempDir(), "missing-ffmpeg")} {
		s, err := New(Options{DB: f.db, Directory: t.TempDir(), Storage: f.s.storage, HelperBinary: binary, Authorize: f.s.authorize, FFmpeg: tool})
		if err != nil {
			t.Fatal(err)
		}
		if (s.ffmpeg != "") != (tool == binary) {
			t.Fatalf("extraction capability for %q = %q", tool, s.ffmpeg)
		}
		s.Close()
	}
}
