package decoder

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A subtitle burned into an HDR picture keeps its transparency through the colour conversion:
// an opaque subtitle covers the film, a half-transparent one lets half of it through, the film
// beside it is untouched, and the answer is the same every run. Run with a real FFmpeg, since
// the fault this holds was FFmpeg 8.1 returning the transparency changed (jellyfin-ffmpeg 8.1.3
// and Portico's own build), where 8.0 did not.
func TestAnHDRBurnedSubtitleKeepsItsTransparency(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if filters, _ := exec.Command(ffmpeg, "-hide_banner", "-filters").Output(); !strings.Contains(string(filters), " zscale ") {
		t.Skip("this ffmpeg has no zscale filter, so it burns no subtitle into an HDR picture")
	}
	average := regexp.MustCompile(`YAVG=([0-9.]+)`)
	// A grey 10-bit film, and a 100x50 white subtitle in the middle of a transparent picture.
	brightness := func(dynamicRange, opacity, crop string) float64 {
		t.Helper()
		cue := "color=white@" + opacity + ":s=100x50:d=1,format=rgba,pad=320:180:110:65:color=black@0"
		graph := "[1:v]" + hdrSubtitleFilter(dynamicRange) + "[burn_cues];[0:v][burn_cues]overlay=format=yuv420p10:eof_action=pass:shortest=0," +
			"format=yuv420p10le,crop=" + crop + ",signalstats,metadata=print:key=lavfi.signalstats.YAVG:file=-"
		out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=0x404040:s=320x180:d=1,format=yuv420p10le", "-f", "lavfi", "-i", cue,
			"-filter_complex", graph, "-frames:v", "1", "-f", "null", "-").CombinedOutput()
		found := average.FindSubmatch(out)
		if err != nil || found == nil {
			t.Fatalf("%s at opacity %s: %v\n%s", dynamicRange, opacity, err, out)
		}
		value, _ := strconv.ParseFloat(string(found[1]), 64)
		return value
	}
	const inside, beside = "60:30:130:75", "60:30:10:10"
	for _, dynamicRange := range []string{"hdr10", "hlg"} {
		film := brightness(dynamicRange, "1", beside)
		opaque := brightness(dynamicRange, "1", inside)
		half := brightness(dynamicRange, "0.5", inside)
		if opaque-film < 100 {
			t.Fatalf("%s: an opaque white subtitle is %.0f over a film of %.0f: it does not cover the film", dynamicRange, opaque, film)
		}
		if share := (half - film) / (opaque - film); share < 0.45 || share > 0.55 {
			t.Fatalf("%s: a half-transparent subtitle shows %.0f%% of itself (film %.0f, half %.0f, opaque %.0f)", dynamicRange, share*100, film, half, opaque)
		}
		for run := 0; run < 3; run++ {
			if again := brightness(dynamicRange, "1", inside); again != opaque {
				t.Fatalf("%s: the same subtitle came out %.0f and then %.0f", dynamicRange, opaque, again)
			}
		}
	}
	// The subtitle picture stays 10-bit with transparency.
	if !strings.HasSuffix(hdrSubtitleFilter("hdr10"), "format=yuva420p10le") {
		t.Fatal("the HDR subtitle picture is no longer 10-bit with transparency")
	}
}
