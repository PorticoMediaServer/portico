package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCopyFMP4SegmentsAreSelfConsistentAfterRestart(t *testing.T) {
	for _, codec := range []string{"hevc", "av1"} {
		t.Run(codec, func(t *testing.T) { testCopyFMP4Restart(t, codec) })
	}
}
func testCopyFMP4Restart(t *testing.T, codec string) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	source := filepath.Join(root, "hdr.mkv")
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-t", "25", "-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le", "-x265-params", "keyint=96:min-keyint=96:scenecut=0:log-level=error", "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", source}
	if codec == "av1" {
		encoders, err := exec.CommandContext(ctx, binary, "-hide_banner", "-encoders").CombinedOutput()
		if err != nil || !strings.Contains(string(encoders), "libsvtav1") {
			decodertest.Unavailable(t, "libsvtav1 required")
		}
		args = []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x96:rate=24", "-t", "25", "-c:v", "libsvtav1", "-preset", "12", "-g", "96", "-svtav1-params", "scd=0:lp=2", "-pix_fmt", "yuv420p10le", source}
	}
	if out, e := exec.CommandContext(ctx, binary, args...).CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	timeline := &copyTimeline{Boundaries: []float64{0, 4, 12, 16, 24, 25}}
	var reference []byte
	for _, from := range []int{0, 3} {
		stage := filepath.Join(root, fmt.Sprint(from))
		outdir := stage + "-out"
		os.MkdirAll(stage, 0700)
		os.MkdirAll(outdir, 0700)
		args = []string{"-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", timeline.Boundaries[from]+.002), "-i", source, "-map", "0:v:0", "-an", "-c:v", "copy", "-tag:v", "hvc1", "-map_metadata", "-1", "-fflags", "+bitexact", "-muxdelay", "0", "-muxpreload", "0"}
		if codec == "av1" {
			for i := range args {
				if args[i] == "hvc1" {
					args[i] = "av01"
				}
			}
		}
		args = append(args, copySegmentArgs(timeline, from, stage, true)...)
		if out, e := exec.CommandContext(ctx, binary, args...).CombinedOutput(); e != nil {
			t.Fatalf("segments: %v %s", e, out)
		}
		_, e := promoteFMP4Segments(stage, outdir, timeline, from, true)
		if e != nil {
			t.Fatal(e)
		}
		init, e := os.ReadFile(filepath.Join(outdir, "init.mp4"))
		if e != nil {
			t.Fatal(e)
		}
		if reference == nil {
			reference = init
		} else if !bytes.Equal(reference, init) {
			t.Fatal("restart changed initialization")
		}
		for index := from; index < timeline.count(); index++ {
			media, e := os.ReadFile(filepath.Join(outdir, fmt.Sprintf("segment-%06d.m4s", index)))
			if e != nil {
				t.Fatal(e)
			}
			combined := filepath.Join(root, "joined.mp4")
			os.WriteFile(combined, append(append([]byte{}, init...), media...), 0600)
			data, e := exec.CommandContext(ctx, decodertest.QualifiedFFprobe(t), "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "json", combined).Output()
			if e != nil {
				t.Fatal(e)
			}
			var parsed struct {
				Packets []struct {
					PTS string `json:"pts_time"`
				} `json:"packets"`
			}
			if json.Unmarshal(data, &parsed) != nil || len(parsed.Packets) == 0 {
				t.Fatal(string(data))
			}
			first := math.Inf(1)
			for _, p := range parsed.Packets {
				v, _ := strconv.ParseFloat(p.PTS, 64)
				first = math.Min(first, v)
			}
			if math.Abs(first-timeline.Boundaries[index]) > .001 {
				t.Fatalf("window %d segment %d PTS %f want %f", from, index, first, timeline.Boundaries[index])
			}
			if out, e := exec.CommandContext(ctx, binary, "-v", "error", "-i", combined, "-f", "null", "-").CombinedOutput(); e != nil {
				t.Fatalf("decode: %v %s", e, out)
			}
		}
	}
}
func TestMP4BoxBounds(t *testing.T) {
	for _, v := range [][]byte{{}, {0, 0, 0, 1, 'm', 'o', 'o', 'f'}, {0, 0, 0, 100, 'm', 'o', 'o', 'f'}} {
		if len(v) == 0 {
			continue
		}
		if _, e := mp4Boxes(v, 0, len(v)); e == nil {
			t.Fatal("accepted truncated box")
		}
	}
}
