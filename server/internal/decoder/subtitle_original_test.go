package decoder

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSubtitleFontExtractionPreservesAttachmentBytes(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg is unavailable")
	}
	ffmpeg, _ = filepath.Abs(ffmpeg)
	t.Setenv("PORTICO_DECODER_SANDBOX", "off")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	font := []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4}
	fontPath := filepath.Join(dir, "font.ttf")
	if e = os.WriteFile(fontPath, font, 0600); e != nil {
		t.Fatal(e)
	}
	media := filepath.Join(dir, "fonts.mkv")
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=size=16x16:duration=0.2", "-c:v", "mpeg4", "-attach", fontPath, "-metadata:s:t", "mimetype=application/x-truetype-font", "-metadata:s:t", "filename=font.ttf", media)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	a, b, res := runFixture(t)
	defer a.Close()
	defer b.Close()
	defer res.Close()
	path := "/input/" + strings.Repeat("a", 64)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, media)
	})}
	defer server.Close()
	go server.Serve(a)
	got, e := RunSubtitleFontExtract(ctx, storage.New("").Supervisor, "font-fixture", ffmpeg, "http://"+a.Addr().String()+path, res, 1)
	if e != nil || !bytes.Equal(got, font) {
		t.Fatalf("font extraction: %v bytes=%d", e, len(got))
	}
}
func TestEmbeddedPGSExtractionReturnsSUPPackets(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg is unavailable")
	}
	ffmpeg, _ = filepath.Abs(ffmpeg)
	t.Setenv("PORTICO_DECODER_SANDBOX", "off")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	supPath := filepath.Join(dir, "source.sup")
	if e = os.WriteFile(supPath, pgsFixture(), 0600); e != nil {
		t.Fatal(e)
	}
	media := filepath.Join(dir, "subtitles.mkv")
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=size=16x16:duration=8", "-i", supPath, "-map", "0:v:0", "-map", "1:s:0", "-c:v", "mpeg4", "-c:s", "copy", media)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	a, b, res := runFixture(t)
	defer a.Close()
	defer b.Close()
	defer res.Close()
	path := "/input/" + strings.Repeat("b", 64)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, media)
	})}
	defer server.Close()
	go server.Serve(a)
	got, e := RunSubtitleExtract(ctx, storage.New("").Supervisor, "pgs-fixture", ffmpeg, "http://"+a.Addr().String()+path, res, 1, "pgs")
	if e != nil || len(got) < 13 || string(got[:2]) != "PG" {
		t.Fatalf("PGS extraction: %v bytes=%d", e, len(got))
	}
}
