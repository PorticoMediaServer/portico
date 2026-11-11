package mediaengine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// TestMediaMatrixHardware converts on the graphics chip named by
// PORTICO_TEST_HARDWARE_BACKEND, for a browser that takes only H.264: an HEVC film at 1080p,
// a 4K HDR10 one tone-mapped to SDR, and the 1080p one with its picture subtitle burned in.
// Each must be planned on that chip, come out as planned, and be made at least as fast as it
// plays. The speeds are logged as SPEED lines.
func TestMediaMatrixHardware(t *testing.T) {
	ffmpeg, ffprobe := matrixTools(t)
	backend := os.Getenv("PORTICO_TEST_HARDWARE_BACKEND")
	if backend == "" {
		t.Skip("no graphics chip named: set PORTICO_TEST_HARDWARE_BACKEND to qsv, vaapi or nvenc")
	}
	// What each backend's own trial says, so a fall back to software comes with its reason.
	detector := &decoder.HardwareDetector{Timeout: 30 * time.Second}
	if libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil); err == nil {
		detector.Libraries = libraries
	}
	for _, probe := range detector.Report(context.Background(), ffmpeg, os.Getenv("PORTICO_TEST_HARDWARE_DEVICE")) {
		t.Logf("hardware trial: %s available=%v reason=%s encoder=%s native filters=%v", probe.Backend, probe.Available, probe.Reason, probe.Encoder, probe.NativeFilters)
	}
	m := newMatrix(t, ffmpeg, ffprobe)
	t.Cleanup(func() { t.Logf("what was planned:\n%s", strings.Join(m.summary, "\n")) })
	dir := t.TempDir()
	sup := filepath.Join(dir, "box.sup")
	if err := os.WriteFile(sup, pictureSubtitle(1920, 1080, 600, 420, 720, 240, pgsFrom, pgsTo), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each film in a library of its own.
	generate := func(file string, args ...string) string {
		path := filepath.Join(t.TempDir(), "library", strings.TrimSuffix(file, filepath.Ext(file)), file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		m.run(t, common(path, args...)...)
		return path
	}
	sdr := generate("Ten Eighty (2020).mkv", append(lavfi("1920x1080", 24), "-i", sup, "-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-pix_fmt", "yuv420p", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error", "-c:a", "aac", "-ac", "2", "-c:s", "copy")...)
	hdr := generate("Four K (2020).mkv", append(lavfi("3840x2160", 24), "-vf", toHDR("smpte2084"), "-c:v", "libx265", "-preset", "ultrafast",
		"-x265-params", "log-level=error:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display="+masterDisplay+":max-cll=1000,400",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-c:a", "aac", "-ac", "2")...)

	for _, c := range []struct {
		name, path string
		burn       bool
	}{{"hevc-1080p", sdr, false}, {"hevc-4k-hdr10-tone-mapped", hdr, false}, {"hevc-1080p-burned", sdr, true}} {
		t.Run(c.name, func(t *testing.T) {
			item, subtitles := m.title(t, c.path)
			body := api.PlaybackStart{ItemID: item, StartFrom: "beginning"}
			if c.burn {
				id := subtitles["pgs"]
				body.Subtitles = &api.SubtitlePick{TrackID: &id, Delivery: "burn"}
			}
			// The chip is tried out beside the server's start; until then a conversion is
			// planned in software. Wait for it, up to three minutes.
			var session *api.PlaybackSession
			var plan streaming.DeliveryPlan
			var began time.Time
			for deadline := time.Now().Add(3 * time.Minute); ; {
				var refused string
				began = time.Now()
				session, plan, refused = m.start(t, "web", body)
				if refused != "" {
					t.Fatalf("FINDING %s for web: refused: %s", c.name, refused)
				}
				if plan.HardwareBackend == backend || time.Now().After(deadline) {
					break
				}
				m.stop(session)
				time.Sleep(5 * time.Second)
			}
			defer m.stop(session)
			m.note(fmt.Sprintf("%-26s web  %s", c.name, describe(plan)))
			f := &finding{t: t}
			f.check(plan.HardwareBackend == backend, "planned on %s, not %s: see the hardware trial lines for why", plan.HardwareBackend, backend)
			video, audio := m.probe(t, c.path, false)
			outVideo, outAudio, files, _, _ := m.segments(t, session.Presentation.URL, 0, 99)
			elapsed := time.Since(began)
			speed := float64(clipSeconds) / elapsed.Seconds()
			t.Logf("SPEED %s on %s: %d seconds of %dx%d made in %.1fs, %.2fx real time", c.name, plan.HardwareBackend, clipSeconds, video.Width, video.Height, elapsed.Seconds(), speed)
			f.check(speed >= 1, "made at %.2fx real time: a viewer would wait", speed)
			checkVideo(f, plan, video, outVideo, false)
			checkAudio(f, plan, audio, outAudio)
			checkTimeline(f, outVideo, outAudio, 0, clipSeconds)
			if plan.ToneMap && outVideo != nil {
				mean, black, clipped := brightness(m.frame(t, 3, "-i", files[0]))
				wantMean, wantBlack, wantClipped := brightness(m.frame(t, 3, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=4"))
				t.Logf("tone-mapped brightness %.0f against %.0f for the SDR original", mean, wantMean)
				f.check(mean >= wantMean/2 && mean <= wantMean+45 && black <= wantBlack+0.2 && clipped <= wantClipped+0.1,
					"tone-mapped picture: mean %.0f, black %.2f, clipped %.2f; the SDR original: mean %.0f, black %.2f, clipped %.2f",
					mean, black, clipped, wantMean, wantBlack, wantClipped)
			}
			if c.burn {
				// The box covers the middle of the picture from 5 to 9 seconds.
				shown := m.frame(t, 7, "-i", files[0])
				after := m.frame(t, 3, "-i", files[0])
				inside, _ := boxChange(shown, after)
				var bright float64
				for y := boxY; y < boxY+boxH; y++ {
					for x := boxX; x < boxX+boxW; x++ {
						bright += float64(shown[y*320+x])
					}
				}
				bright /= boxW * boxH
				f.check(bright > 200 && inside > 40, "the burned box is not in the picture at 7s (brightness %.0f, change %.0f)", bright, inside)
			}
			f.report(c.name + " on " + backend)
		})
	}
}
