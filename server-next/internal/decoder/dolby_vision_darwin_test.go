package decoder

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

func TestMacProfile5Planner(t *testing.T) {
	facts := &ToolchainFacts{
		Filters:  map[string]bool{"tonemapx": true, "tonemap_videotoolbox": true},
		Encoders: map[string]bool{"libx264": true},
	}
	target := ConversionTarget{VideoCodec: "h264", SourceRange: "dolby_vision", DolbyVisionProfile: 5, ToneMap: true}
	probe := HardwareProbe{Backend: BackendVideoToolbox, Available: true, DolbyVisionFilters: true}
	r := ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: facts, ToneMapAlgorithm: "mobius"}
	for _, tc := range []struct {
		probe         HardwareProbe
		systemFilters bool
		want          string
	}{
		{probe, false, "tonemap_videotoolbox"},
		{probe, true, "tonemapx"},
		{HardwareProbe{Backend: BackendVideoToolbox, Available: true}, false, "tonemapx"},
		{HardwareProbe{Backend: BackendSoftware}, false, "tonemapx"},
	} {
		r.Probe, r.DisableHardwareDecode = tc.probe, tc.systemFilters
		graph, err := BuildConversion(r)
		if err != nil || !strings.Contains(argValue(graph.Video, "-vf"), tc.want+"=tonemap=mobius:") {
			t.Fatalf("with %+v and system filters %t: %v, %v", tc.probe, tc.systemFilters, graph.Video, err)
		}
	}

	previous := installedToolchain.Load()
	t.Cleanup(func() { installedToolchain.Store(previous) })
	installedToolchain.Store(facts)
	spec := AnalysisSpec{Kind: "trickplay", SourceVideo: PreviewVideoFacts{DynamicRange: "dolby_vision", DolbyVisionProfile: 5}}
	tone, hardware, err := analysisPreviewTone(spec.SourceVideo, true)
	if err != nil || hardware || analysisNeedsHardware(spec) || !strings.Contains(tone, "tonemapx=tonemap=hable:") {
		t.Fatalf("profile 5 preview: %q, hardware=%t, %v", tone, hardware, err)
	}
}

func TestMacProfile5Conversion(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	facts, err := ProbeToolchain(ctx, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Filters["tonemapx"] {
		decodertest.Unavailable(t, "profile 5 fixture needs Jellyfin FFmpeg with tonemapx")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "profile5.hevc")
	runTinyMedia(t, ctx, ffmpeg, []string{
		"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=24,format=yuv420p10le", "-frames:v", "1",
		"-color_range", "pc", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "pools=1:frame-threads=1:log-level=error", input,
	}, dir)

	// libx265's library parameters do not expose the x265 CLI's RPU-file
	// option. Append the first Annex B RPU NAL to the single HEVC access unit.
	rpu, err := os.ReadFile("../../../fixtures/video/dovi-metadata/dv-5-V29.rpu")
	if err != nil {
		t.Fatal(err)
	}
	next := bytes.Index(rpu[4:], []byte{0, 0, 0, 1})
	if next < 0 {
		t.Fatal("expected multiple Annex B RPUs in the fixture")
	}
	base, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, append(base, rpu[:4+next]...), 0600); err != nil {
		t.Fatal(err)
	}

	probePicture := func(t *testing.T, file string) string {
		t.Helper()
		args := []string{"-v", "error", "-select_streams", "v:0", "-show_entries",
			"frame=width,height,pix_fmt,color_transfer,color_space,color_primaries:frame_side_data=side_data_type", "-of", "compact", file}
		cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: ffprobe, Args: args, ReadPaths: []string{dir}})
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("probe %s: %v: %s", file, err, output)
		}
		return string(output)
	}
	if picture := probePicture(t, input); !strings.Contains(picture, "Dolby Vision Metadata") {
		t.Fatalf("fixture did not decode with RPU metadata: %s", picture)
	}

	libraries, err := ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Fatal(err)
	}
	detector := HardwareDetector{Libraries: libraries}
	hardware := detector.Probe(ctx, ffmpeg, BackendVideoToolbox, "")
	t.Logf("VideoToolbox available=%t reason=%s Metal tone mapping=%t", hardware.Available, hardware.Reason, hardware.DolbyVisionFilters)
	for _, tc := range []struct {
		codec, dynamic, pixel, transfer, matrix, primaries string
		depth                                              int
	}{
		{"h264", "sdr", "yuv420p", "bt709", "bt709", "bt709", 8},
		{"hevc", "hdr10", "yuv420p10le", "smpte2084", "bt2020nc", "bt2020", 10},
	} {
		for _, useHardware := range []bool{false, true} {
			name, selected := "tonemapx", HardwareProbe{Backend: BackendSoftware}
			if useHardware {
				name, selected = "tonemap_videotoolbox", hardware
			}
			t.Run(tc.dynamic+"/"+name, func(t *testing.T) {
				if useHardware && (!hardware.DolbyVisionFilters || !hardware.OutputEncoders[tc.codec+":"+strconv.Itoa(tc.depth)]) {
					t.Skip("runner did not qualify VideoToolbox for this output")
				}
				target := ConversionTarget{
					VideoCodec: tc.codec, BitDepth: tc.depth, DynamicRange: tc.dynamic, SourceRange: "dolby_vision", DolbyVisionProfile: 5,
					ToneMap: tc.dynamic == "sdr", Width: 160, Height: 120, FrameRate: 24, VideoBitrateBPS: 500_000,
				}
				graph, err := BuildConversion(ConversionRequest{
					Target: &target, ConvertVideo: true, Toolchain: &facts, Probe: selected, SourceVideoCodec: "hevc", SourceBitDepth: 10,
				})
				if err != nil || !strings.Contains(argValue(graph.Video, "-vf"), name+"=") || HardwareStagesRequireDevice(graph.Stages) != useHardware {
					t.Fatalf("expected %s: %+v, %v", name, graph, err)
				}
				output := filepath.Join(dir, tc.dynamic+"-"+name+".mp4")
				args := append([]string{"-v", "error"}, graph.Input...)
				args = append(args, "-i", input)
				args = append(args, graph.Video...)
				args = append(args, "-frames:v", "1", "-an", output)
				cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{
					Executable: ffmpeg, Args: args, Libraries: libraries, ReadPaths: []string{dir}, WriteDirs: []string{dir}, Hardware: useHardware,
				})
				if err != nil {
					t.Fatal(err)
				}
				if log, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("convert with %s: %v: %s", name, err, log)
				}
				picture := probePicture(t, output)
				for _, want := range []string{"width=160", "height=120", "pix_fmt=" + tc.pixel,
					"color_transfer=" + tc.transfer, "color_space=" + tc.matrix, "color_primaries=" + tc.primaries} {
					if !strings.Contains(picture, want) || strings.Contains(picture, "Dolby Vision") {
						t.Fatalf("expected converted %s picture (%s): %s", tc.dynamic, want, picture)
					}
				}
				t.Log(strings.TrimSpace(picture))
			})
		}
	}
}
