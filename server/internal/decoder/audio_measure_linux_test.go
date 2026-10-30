//go:build linux

package decoder

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// NEW-29: the audio render measurement (audiofacts.Args through
// RunStreamingProbe, as subtitlevideo.MeasureAudio runs it) reads a real FLAC,
// AAC and MP3 file through the confined decoder and the private bridge.
func TestAudioMeasurementReadsRealFilesConfined(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	if ffprobe, err = filepath.EvalSymlinks(ffprobe); err != nil {
		t.Fatal(err)
	}
	libraries, err := ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Skipf("libraries: %v", err)
	}
	dir := t.TempDir()
	for _, format := range []struct{ name, codec string }{{"a.flac", "flac"}, {"a.m4a", "aac"}, {"a.mp3", "libmp3lame"}} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(dir, format.name)
			if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=441:sample_rate=44100:duration=5", "-c:a", format.codec, path).CombinedOutput(); err != nil {
				t.Skipf("encode %s: %v %s", format.name, err, out)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			v4, v6, res := runFixture(t)
			defer v6.Close()
			token := "/input/" + strings.Repeat("ab", 32)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != token {
					http.Error(w, "no", 404)
					return
				}
				http.ServeContent(w, r, format.name, time.Time{}, bytes.NewReader(raw))
			})}
			go func() { _ = server.Serve(v4) }()
			defer server.Close()
			url := "http://" + v4.Addr().String() + token
			var facts audiofacts.Facts
			var parsed error
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = RunStreamingProbe(ctx, &storage.Supervisor{Limit: 1}, "measure-"+format.name, ffprobe, url, audiofacts.Args(url), res, func(out io.Reader) error {
				facts, parsed = audiofacts.Parse(out)
				_, _ = io.Copy(io.Discard, out)
				return nil
			}, libraries...)
			if err != nil {
				t.Fatalf("confined measurement of %s failed: %v", format.name, err)
			}
			if parsed != nil {
				t.Fatalf("measurement output of %s unreadable: %v", format.name, parsed)
			}
			if facts.SampleRate != 44100 {
				t.Fatalf("%s facts: %+v", format.name, facts)
			}
		})
	}
}
