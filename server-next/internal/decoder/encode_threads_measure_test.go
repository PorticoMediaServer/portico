package decoder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// How fast 4K HDR10 software conversions run with the share against today's two threads, one,
// two and four at once, with the arguments BuildConversion gives (HEVC 10-bit HDR10, 3840x2160,
// 24 fps, preset veryfast). Each conversion takes its threads from ReservePlaybackEncode as it
// starts, as a playback window does, and gives them back when its converter exits. A
// measurement for the pull request, not a check of a machine's speed: it runs when
// PORTICO_MEASURE_THREADS=1, with the FFmpeg in PORTICO_FFMPEG.
func TestMeasureTheShareOfTheMachine(t *testing.T) {
	ffmpeg := os.Getenv("PORTICO_FFMPEG")
	if os.Getenv("PORTICO_MEASURE_THREADS") != "1" || ffmpeg == "" {
		t.Skip("a measurement; set PORTICO_MEASURE_THREADS=1 and PORTICO_FFMPEG to run it")
	}
	ctx := context.Background()
	facts, err := ProbeToolchain(ctx, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	const frames = 240
	src := filepath.Join(t.TempDir(), "src.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-y", "-f", "lavfi", "-i",
		"testsrc2=s=3840x2160:r=24:d=10,format=yuv420p10le,noise=alls=10:allf=t", "-c:v", "libx265", "-preset", "ultrafast", "-b:v", "40M",
		"-pix_fmt", "yuv420p10le", "-x265-params", "log-level=error:hdr10=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:max-cll=1000,400",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", src).CombinedOutput(); err != nil {
		t.Fatalf("source: %v\n%s", err, out)
	}
	command := func(threads int) *exec.Cmd {
		graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, Toolchain: &facts, SoftwareThreads: threads, SoftwarePreset: "veryfast",
			Target: &ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", Width: 3840, Height: 2160, FrameRate: 24,
				VideoBitrateBPS: 24_500_000, Packaging: "fmp4_hls"}})
		if err != nil {
			t.Fatal(err)
		}
		args := append([]string{"-v", "error", "-nostdin", "-i", src}, graph.Video...)
		return exec.Command(ffmpeg, append(args, "-an", "-f", "null", "-")...)
	}
	run := func(together int, share bool) (each []float64, all float64) {
		began := time.Now()
		done := make(chan [2]float64, together)
		for i := range together {
			threads, release := 2, func() {}
			if share {
				threads, release = ReservePlaybackEncode()
			}
			cmd := command(threads)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			go func() {
				err := cmd.Wait()
				release()
				if err != nil {
					t.Errorf("conversion %d: %v", i, err)
				}
				done <- [2]float64{float64(i), frames / time.Since(began).Seconds()}
			}()
		}
		each = make([]float64, together)
		for range together {
			d := <-done
			each[int(d[0])] = d[1]
		}
		return each, float64(together*frames) / time.Since(began).Seconds()
	}
	for rep := range 2 {
		for _, together := range []int{1, 2, 4} {
			for _, share := range []bool{false, true} {
				name := "today: 2 threads each"
				if share {
					name = fmt.Sprintf("the share of %d processors", processors())
				}
				each, all := run(together, share)
				t.Logf("run %d | %d at once | %-28s | each %v frames/s | together %.2f", rep+1, together, name, rounded(each), all)
			}
		}
	}
}

func rounded(v []float64) []string {
	out := make([]string, len(v))
	for i, f := range v {
		out[i] = fmt.Sprintf("%.2f", f)
	}
	return out
}
