package linearinput_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/linearbuffer"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Exercise the real rolling HLS origin -> shared gateway -> owned probe ->
// owned copy/mux -> private publisher path beyond the configured startup
// buffer. A finite three-second probe cannot establish this continuation.
func TestOwnedRollingLiveCaptureAdvancesPastStartupBuffer(t *testing.T) {
	t.Run("mpegts", func(t *testing.T) { rollingLiveCapture(t, false, 0, "") })
	t.Run("fragmented-mp4", func(t *testing.T) { rollingLiveCapture(t, true, 0, "") })
	t.Run("pts-wrap", func(t *testing.T) { rollingLiveCapture(t, false, 95430, "") })
}

// This opt-in reproduction consumes the campaign's approved public fixture;
// normal test runs never depend on an external server.
func TestOwnedPublicLiveCaptureAdvancesPastStartupBuffer(t *testing.T) {
	if os.Getenv("PORTICO_M0_PUBLIC_LIVE_REPRO") != "1" {
		t.Skip("campaign public fixture reproduction not requested")
	}
	rollingLiveCapture(t, false, 0, "https://demo.getportico.tv/m0-fixtures/live/demo-cinema/index.m3u8")
}

func rollingLiveCapture(t *testing.T, fragmented bool, sourceClockSeconds int, publicURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ffmpeg, _ = filepath.EvalSymlinks(ffmpeg)
	ffprobe, _ = filepath.EvalSymlinks(ffprobe)
	version, err := exec.CommandContext(ctx, ffmpeg, "-version").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(strings.SplitN(string(version), "\n", 2)[0])
	// The production media owner selects the execution posture. Do not skip a
	// valid baseline host or force a required sandbox off to run this test.
	if err := decoder.RequiredConfinement(ffmpeg); err != nil {
		t.Fatal(err)
	}
	t.Logf("production decoder sandboxed=%t", mediaexec.Decide(ffmpeg).Sandboxed)
	libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	inputURL := publicURL
	approvalURL := inputURL
	if inputURL == "" {
		pattern := filepath.Join(dir, "pattern.mp4")
		generate := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "6", "-c:v", "libx264", "-threads:v", "1", "-g", "50", "-keyint_min", "50", "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", pattern)
		if output, err := generate.CombinedOutput(); err != nil {
			t.Fatalf("generate procedural source: %v: %s", err, output)
		}
		originArgs := []string{"-nostdin", "-v", "error", "-re", "-stream_loop", "-1", "-i", pattern, "-c", "copy"}
		if sourceClockSeconds != 0 {
			// A long-running MPEG-TS source wraps its 33-bit 90kHz clock. Start
			// near that real boundary without waiting twenty-six hours.
			originArgs = append(originArgs, "-output_ts_offset", strconv.Itoa(sourceClockSeconds))
		}
		originArgs = append(originArgs, "-f", "hls", "-hls_time", "2", "-hls_list_size", "12", "-hls_delete_threshold", "4", "-hls_flags", "delete_segments+omit_endlist+temp_file", "-hls_segment_filename", filepath.Join(dir, "segment-%09d.ts"), filepath.Join(dir, "index.m3u8"))
		origin := exec.CommandContext(ctx, ffmpeg, originArgs...)

		if err = origin.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = origin.Process.Kill(); _ = origin.Wait() }()
		for {
			data, _ := os.ReadFile(filepath.Join(dir, "index.m3u8"))
			if strings.Count(string(data), "#EXTINF:") >= 3 {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("procedural origin did not publish", ctx.Err())
			case <-ticker.C:
			}
		}
		server := httptest.NewServer(http.FileServer(http.Dir(dir)))
		defer server.Close()
		inputURL = server.URL + "/index.m3u8"
		// This procedural origin owns the manifest and its sibling segments.
		// Keep its directory approval separate from the exact manifest input.
		approvalURL = server.URL + "/"
	}
	approval, err := (remotemedia.Policy{}).Approve(ctx, approvalURL)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := linearinput.Open(ctx, livechannels.Input{SourceID: inputURL, ChannelID: "procedural", Locator: inputURL, Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}, RetryWindowSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	supervisor := storage.New("").Supervisor
	reservation, err := gateway.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decoder.LinearProbe(ctx, supervisor, "rolling-live-probe", ffprobe, gateway.URL(), true, reservation, nil, libraries...); err != nil {
		t.Fatal("confined source probe", err)
	}
	buffer, err := linearbuffer.New(linearbuffer.Options{Parent: dir, OriginMS: time.Now().UnixMilli(), Retention: time.Minute, MaxBytes: 64 << 20, LiveDelay: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	publisher, err := buffer.Publisher(linearbuffer.Producer{ID: 1, Fragmented: fragmented}, func() error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	output, err := gateway.AttachOutput(publisher)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err = gateway.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	work, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- decoder.RunLinearHLS(work, supervisor, "rolling-live-capture", ffmpeg, gateway.URL(), output, decoder.LinearEncoding{HLSInput: true, Video: true, Audio: true, Fragmented: fragmented, CopyAAC: fragmented}, reservation, nil, libraries...)
	}()
	defer func() { stop(); <-done }()
	for {
		window := buffer.Window()
		if window.Ready && window.EndUS-window.StartUS >= 12_000_000 {
			return
		}
		select {
		case err := <-done:
			done <- err // cleanup still waits for the physically reaped worker
			if err == nil {
				err = errors.New("live capture exited")
			}
			t.Fatalf("producer exited before ready: %+v: %v", window, err)
		case <-ctx.Done():
			t.Fatalf("producer never advanced past startup buffer: %+v", window)
		case <-ticker.C:
		}
	}
}
