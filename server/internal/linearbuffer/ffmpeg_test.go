package linearbuffer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"strings"
	"testing"
	"time"
)

// Synthetic muxer/clock contract test, not an alternate production execution
// path. Production FFmpeg remains behind decoder confinement and owned input.
func TestFFmpegPreservedMidProgrammeClock(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source := filepath.Join(t.TempDir(), "fixture.mp4")
	generate := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "12", "-c:v", "libx264", "-threads:v", "1", "-g", "96", "-keyint_min", "96", "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", source)
	if output, e := generate.CombinedOutput(); e != nil {
		t.Fatalf("generate fixture: %v: %s", e, output)
	}
	b := testBuffer(t, true)
	p := publisher(t, b, 1, b.options.OriginMS)
	p.producer.EndMS = p.producer.TimelineBaseMS + 10000
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/")
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, r)
		if rec.Code != http.StatusCreated {
			t.Logf("publisher rejected %s: %d %s %s", r.URL.Path, rec.Code, rec.Body.String(), raw)
			p.mu.Lock()
			t.Logf("last=%d pending=%v", p.last, p.pending)
			p.mu.Unlock()
		}
		for k, values := range rec.Header() {
			w.Header()[k] = values
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer server.Close()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-loglevel", "error", "-copyts", "-start_at_zero", "-ss", "5", "-i", source, "-t", "10", "-map", "0:v:0", "-map", "0:a:0", "-c", "copy", "-avoid_negative_ts", "disabled", "-muxdelay", "0", "-f", "hls", "-hls_time", "4", "-hls_list_size", "12", "-hls_flags", "independent_segments+program_date_time", "-hls_segment_options", "mpegts_copyts=1", "-method", "PUT", "-hls_segment_filename", server.URL+"/segment-%09d.ts", server.URL+"/index.m3u8")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("publish fixture: %v: %s", e, output)
	}
	if e := p.Finish(ctx); e != nil {
		t.Fatal("incomplete output", e)
	}
	w := b.Window()
	if !w.Ready || w.StartUS < 3900000 || w.StartUS > 4100000 || w.EndUS < 9900000 || w.EndUS > 10100000 {
		t.Fatalf("copyts lost selected source clock: %+v", w)
	}
	target := int64(5000000)
	if actual, e := b.Resolve(&target); e != nil || actual != target {
		t.Fatalf("mid-programme target: %d %v", actual, e)
	}
}

// Fragmented MP4 publication: an init segment, .m4s fragments dated from
// their tfdt clock, and a playlist that maps each producer's init.
func TestFFmpegFragmentedLiveOutput(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source := filepath.Join(t.TempDir(), "fixture.mkv")
	generate := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "12", "-c:v", "libx264", "-threads:v", "1", "-g", "50", "-keyint_min", "50", "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "flac", source)
	if output, e := generate.CombinedOutput(); e != nil {
		t.Fatalf("generate fixture: %v: %s", e, output)
	}
	b := testBuffer(t, false)
	p, e := b.Publisher(Producer{ID: 1, Fragmented: true}, func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Publisher(Producer{ID: 2}, func() error { return nil }); e != nil {
		t.Fatal("an empty buffer refused a producer", e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/")
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, r)
		if rec.Code != http.StatusCreated {
			t.Logf("publisher rejected %s: %d", r.URL.Path, rec.Code)
		}
		w.WriteHeader(rec.Code)
	}))
	defer server.Close()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-loglevel", "error", "-copyts", "-start_at_zero", "-readrate", "4", "-i", source, "-map", "0:v:0", "-map", "0:a:0", "-c", "copy", "-avoid_negative_ts", "disabled", "-muxdelay", "0", "-f", "hls", "-hls_time", "4", "-hls_list_size", "12", "-hls_flags", "independent_segments+program_date_time", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4", "-method", "PUT", "-hls_segment_filename", server.URL+"/segment-%09d.m4s", server.URL+"/index.m3u8")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("publish fixture: %v: %s", e, output)
	}
	if e := p.Finish(ctx); e != nil {
		t.Fatal("incomplete output", e)
	}
	w := b.Window()
	if !w.Ready || w.EndUS-w.StartUS < 11_000_000 || w.EndUS-w.StartUS > 13_000_000 {
		t.Fatalf("fragment clock: %+v", w)
	}
	playlist, e := b.Playlist()
	if e != nil || !strings.Contains(string(playlist), "#EXT-X-VERSION:7\n") || !strings.Contains(string(playlist), "#EXT-X-MAP:URI=\"i1.mp4\"\n") || !strings.Contains(string(playlist), "s1.m4s") || strings.Contains(string(playlist), ".ts") {
		t.Fatalf("%s %v", playlist, e)
	}
	init, e := b.Open("i1.mp4")
	if e != nil || init.ContentType != "video/mp4" || init.Size < 16 {
		t.Fatal(init, e)
	}
	init.Close()
	fragment, e := b.Open("s1.m4s")
	if e != nil || fragment.ContentType != "video/iso.segment" {
		t.Fatal(fragment, e)
	}
	fragment.Close()
	if _, e = b.Open("s1.ts"); e == nil {
		t.Fatal("a fragment was served under the MPEG-TS name")
	}
	// One playlist cannot switch to MPEG-TS once it holds fragments.
	if _, e = b.Publisher(Producer{ID: 3}, func() error { return nil }); e == nil {
		t.Fatal("an MPEG-TS producer joined a fragmented buffer")
	}
}
