package decoder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHLSPackagingArguments(t *testing.T) {
	args, ext, err := HLSPackagingArgs("fmp4_hls", "init-12.mp4")
	if err != nil || ext != "m4s" || argValue(args, "-hls_segment_type") != "fmp4" || argValue(args, "-hls_fmp4_init_filename") != "init-12.mp4" {
		t.Fatal(args, ext, err)
	}
	if _, _, err = HLSPackagingArgs("fmp4_hls", "../init.mp4"); err == nil {
		t.Fatal("accepted escaped init path")
	}
	_, ext, err = HLSPackagingArgs("mpegts_hls", "")
	if err != nil || ext != "ts" {
		t.Fatal(ext, err)
	}
}

func TestHLSCodecMuxArguments(t *testing.T) {
	for _, tc := range []struct {
		video, audio string
		profile      int
		tag, strict  string
	}{
		{"h264", "aac", 0, "", ""},
		{"hevc", "aac", 0, "hvc1", ""},
		{"hevc", "aac", 5, "dvh1", "unofficial"},
		{"hevc", "aac", 7, "dvhe", "unofficial"},
		{"hevc", "aac", 8, "dvh1", "unofficial"},
		{"hevc", "truehd", 7, "dvhe", "-2"},
		{"", "truehd", 0, "", "-2"},
		{"", "flac", 0, "", "-2"},
	} {
		args := HLSMuxArgs(true, tc.video, tc.audio, tc.profile)
		if argValue(args, "-tag:v") != tc.tag || argValue(args, "-strict") != tc.strict {
			t.Fatal(tc, args)
		}
		if len(HLSMuxArgs(false, tc.video, tc.audio, tc.profile)) != 0 {
			t.Fatal("MP4 codec signalling leaked into MPEG-TS", tc)
		}
	}
}

func TestFragmentedConversionKeepsInitAcrossWindows(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	probe := decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	facts, err := ProbeToolchain(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"h264", "hevc", "av1"} {
		t.Run(codec, func(t *testing.T) {
			if _, err := softwareVideoEncoder(codec, &facts); err != nil {
				t.Skip(err)
			}
			root := t.TempDir()
			var init []byte
			target := ConversionTarget{VideoCodec: codec, Width: 160, Height: 90, FrameRate: 24, Packaging: "fmp4_hls", RateControl: "quality"}
			graph, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, SoftwarePreset: "ultrafast", Toolchain: &facts})
			if err != nil {
				t.Fatal(err)
			}
			for _, from := range []int{0, 1} {
				dir := filepath.Join(root, strconv.Itoa(from))
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=7"}
				args = append(args, graph.Video...)
				packaging, ext, err := HLSPackagingArgs("fmp4_hls", fmt.Sprintf("init-%d.mp4", from))
				if err != nil {
					t.Fatal(err)
				}
				args = append(args, packaging...)
				args = append(args, "-an", "-f", "hls", "-hls_time", "6", "-hls_list_size", "0", "-hls_flags", "independent_segments+temp_file", "-output_ts_offset", strconv.Itoa(from*6), "-start_number", strconv.Itoa(from), "-hls_segment_filename", filepath.Join(dir, "segment-%06d."+ext), filepath.Join(dir, "window.m3u8"))
				runTinyMedia(t, ctx, binary, args, dir)
				data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("init-%d.mp4", from)))
				if err != nil {
					t.Fatal(err)
				}
				if from == 0 {
					init = data
				} else if !bytes.Equal(init, data) {
					t.Fatal("init changed across seek windows")
				}
				if info, err := os.Stat(filepath.Join(dir, fmt.Sprintf("segment-%06d.m4s", from))); err != nil || info.Size() == 0 {
					t.Fatal("missing fragment", err)
				}
				fragment, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("segment-%06d.m4s", from)))
				if err != nil {
					t.Fatal(err)
				}
				standalone := filepath.Join(dir, "standalone.mp4")
				if err := os.WriteFile(standalone, append(append([]byte{}, init...), fragment...), 0600); err != nil {
					t.Fatal(err)
				}
				cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: probe, Args: []string{"-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", "-read_intervals", "%+#1", standalone}, ReadPaths: []string{standalone}})
				if err != nil {
					t.Fatal(err)
				}
				raw, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				pts, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
				if err != nil {
					t.Fatal(err, string(raw))
				}
				if mathBad := pts < 0 || pts > 0.5; mathBad {
					t.Fatal("unexpected raw muxer origin", pts)
				}
				runTinyMedia(t, ctx, binary, []string{"-v", "error", "-i", standalone, "-an", "-threads", "2", "-f", "null", "-"}, dir)

			}
		})
	}
}
