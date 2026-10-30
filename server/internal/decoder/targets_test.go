package decoder

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"strings"
	"testing"
	"time"
)

func argValue(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}
func targetFacts() *ToolchainFacts {
	return &ToolchainFacts{Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "libopus": true, "flac": true}, Filters: map[string]bool{"zscale": true, "tonemap": true, "ass": true, "overlay": true}}
}

func TestConversionTargetsDescribeEveryOutput(t *testing.T) {
	for _, tc := range []struct {
		codec, rangeName, pixel, transfer string
		depth                             int
		encoder                           string
	}{{"h264", "sdr", "yuv420p", "", 8, "libx264"}, {"hevc", "sdr", "yuv420p", "", 8, "libx265"}, {"hevc", "hdr10", "yuv420p10le", "smpte2084", 10, "libx265"}, {"hevc", "hlg", "yuv420p10le", "arib-std-b67", 10, "libx265"}, {"av1", "hdr10", "yuv420p10le", "smpte2084", 10, "libaom-av1"}} {
		t.Run(tc.codec+tc.rangeName, func(t *testing.T) {
			target := ConversionTarget{VideoCodec: tc.codec, BitDepth: tc.depth, DynamicRange: tc.rangeName, RateControl: "quality", Height: 1080}
			g, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: targetFacts()})
			if err != nil {
				t.Fatal(err)
			}
			if argValue(g.Video, "-c:v") != tc.encoder || argValue(g.Video, "-pix_fmt") != tc.pixel || argValue(g.Video, "-color_trc") != tc.transfer || argValue(g.Video, "-crf") == "" {
				t.Fatal(g.Video)
			}
			if tc.codec != "h264" && argValue(g.Video, "-tag:v") == "dvh1" {
				t.Fatal("conversion advertised Dolby Vision")
			}
		})
	}
	for _, codec := range []string{"aac", "ac3", "eac3", "opus", "flac"} {
		target := ConversionTarget{AudioCodec: codec, AudioChannels: 6}
		g, err := BuildConversion(ConversionRequest{Target: &target, ConvertAudio: true, Toolchain: targetFacts()})
		if err != nil {
			t.Fatal(codec, err)
		}
		if argValue(g.Audio, "-ac") != "6" || argValue(g.Audio, "-ar") != "48000" || argValue(g.Audio, "-channel_layout") != "5.1" {
			t.Fatal(g.Audio)
		}
		if codec == "flac" && argValue(g.Audio, "-b:a") != "" {
			t.Fatal("lossless audio got a lossy bitrate", g.Audio)
		}
	}
	for _, codec := range []string{"flac", "opus"} {
		target := ConversionTarget{AudioCodec: codec, AudioChannels: 8}
		g, err := BuildConversion(ConversionRequest{Target: &target, ConvertAudio: true, Downmix: true, Toolchain: targetFacts()})
		if err != nil || argValue(g.Audio, "-channel_layout") != "7.1" {
			t.Fatal(g, err)
		}
	}
	for _, codec := range []string{"ac3", "eac3", "dts", "truehd"} {
		target := ConversionTarget{VideoCodec: "hevc", AudioCodec: codec, AudioChannels: 8}
		g, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, CopyAudio: true})
		if err != nil || argValue(g.Audio, "-c:a") != "copy" {
			t.Fatal(codec, g, err)
		}
	}
}

func TestConversionTargetRefusesWrongPicturesAndContainers(t *testing.T) {
	for _, target := range []ConversionTarget{{VideoCodec: "hevc", BitDepth: 8, DynamicRange: "hdr10"}, {SourceRange: "hdr10", DynamicRange: "sdr"}, {VideoCodec: "hevc", DolbyVisionProfile: 5, ToneMap: true}, {VideoCodec: "hevc", MasterDisplay: "evil:params=1"}} {
		if _, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: targetFacts()}); err == nil {
			t.Fatal("accepted invalid target", target)
		}
	}
	target := ConversionTarget{AudioCodec: "eac3", AudioChannels: 8}
	if _, err := BuildConversion(ConversionRequest{Target: &target, ConvertAudio: true}); err == nil {
		t.Fatal("claimed unsupported E-AC-3 7.1 encoder")
	}
}

func TestHEVCEncoderNeedsCodecAndDepthQualification(t *testing.T) {
	for _, backend := range HardwareBackends {
		if !backendSpecs[backend].supported() {
			continue
		}
		target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10"}
		probe := HardwareProbe{Backend: backend, Available: true, OutputEncoders: map[string]bool{"hevc:8": true}}
		g, err := BuildConversion(ConversionRequest{Target: &target, Probe: probe, ConvertVideo: true, Toolchain: targetFacts()})
		if err != nil || g.Backend != BackendSoftware {
			t.Fatal(g, err)
		}
		probe.OutputEncoders["hevc:10"] = true
		g, err = BuildConversion(ConversionRequest{Target: &target, Probe: probe, ConvertVideo: true, Toolchain: targetFacts()})
		if err != nil || argValue(g.Video, "-c:v") != "hevc_"+string(backend) {
			t.Fatal(g, err)
		}
		if backend == BackendVAAPI && !strings.Contains(argValue(g.Video, "-vf"), "p010") {
			t.Fatal(g.Video)
		}
	}
}

func runTinyMedia(t *testing.T, ctx context.Context, binary string, args []string, dir string) {
	t.Helper()
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: args, ReadPaths: []string{dir}, WriteDirs: []string{dir}, Fonts: true})
	if err != nil {
		t.Fatal(err)
	}
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s\nargs: %v", err, output, args)
	}
}

func TestSoftwareTargetsEncodeTinySamples(t *testing.T) {
	binary, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	facts, err := ProbeToolchain(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		codec, rangeName string
		depth            int
	}{{"h264", "sdr", 8}, {"hevc", "sdr", 8}, {"hevc", "hdr10", 10}, {"hevc", "hlg", 10}, {"av1", "sdr", 10}} {
		t.Run(tc.codec+tc.rangeName, func(t *testing.T) {
			if _, err := softwareVideoEncoder(tc.codec, &facts); err != nil {
				t.Skip(err)
			}
			dir := t.TempDir()
			target := ConversionTarget{VideoCodec: tc.codec, BitDepth: tc.depth, DynamicRange: tc.rangeName, Width: 160, Height: 90, FrameRate: 60, RateControl: "quality"}
			g, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, SoftwarePreset: "ultrafast", Toolchain: &facts})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "sample.mp4")
			args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=0.5"}
			args = append(args, g.Video...)
			args = append(args, "-an", "-f", "mp4", path)
			runTinyMedia(t, ctx, binary, args, dir)
			cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: probe, Args: []string{"-v", "error", "-show_entries", "stream=codec_name,pix_fmt,color_transfer,color_primaries,color_space,width,height,avg_frame_rate", "-of", "json", path}, ReadPaths: []string{path}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Streams []struct {
					Codec    string `json:"codec_name"`
					Pixel    string `json:"pix_fmt"`
					Transfer string `json:"color_transfer"`
					Rate     string `json:"avg_frame_rate"`
				}
			}
			if json.Unmarshal(raw, &got) != nil || len(got.Streams) != 1 || got.Streams[0].Codec != tc.codec || got.Streams[0].Rate != "24/1" {
				t.Fatalf("wrong output %s", raw)
			}
			if tc.depth == 10 && !strings.Contains(got.Streams[0].Pixel, "10") {
				t.Fatalf("lost ten-bit output %s", raw)
			}
			runTinyMedia(t, ctx, binary, []string{"-v", "error", "-i", path, "-f", "null", "-"}, dir)
		})
	}
	for _, codec := range []string{"aac", "ac3", "eac3", "opus", "flac"} {
		t.Run(codec, func(t *testing.T) {
			dir := t.TempDir()
			channels := 6
			if codec == "opus" || codec == "flac" {
				channels = 8
			}
			target := ConversionTarget{AudioCodec: codec, AudioChannels: channels}
			g, err := BuildConversion(ConversionRequest{Target: &target, ConvertAudio: true, Toolchain: &facts})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "audio.mp4")
			args := []string{"-v", "error", "-f", "lavfi", "-i", "anullsrc=r=96000:cl=7.1", "-t", "0.25"}
			args = append(args, g.Audio...)
			args = append(args, "-strict", "-2", path)
			runTinyMedia(t, ctx, binary, args, dir)
			runTinyMedia(t, ctx, binary, []string{"-v", "error", "-i", path, "-f", "null", "-"}, dir)
		})
	}
}

func TestHDRBurnKeepsSourceRange(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dir := t.TempDir()
	file := filepath.Join(dir, "cue.ass")
	if err := os.WriteFile(file, []byte("[Script Info]\nScriptType: v4.00+\nPlayResX: 160\nPlayResY: 90\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, Bold, Alignment\nStyle: Default,Arial,24,&H00FFFFFF,1,5\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:00.00,0:00:02.00,Default,BURN\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", Width: 160, Height: 90, FrameRate: 24}
	g, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, BurnIn: &BurnIn{File: file, Format: "ass", Width: 160, Height: 90}, SoftwarePreset: "ultrafast"})
	if err != nil {
		t.Fatal(err)
	}
	if g.VideoMap == "" {
		t.Fatal("HDR burn did not use a separate subtitle plane")
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=black:s=160x90:r=24:d=0.25,format=yuv420p10le,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc"}
	args = append(args, g.Video...)
	args = append(args, "-map", g.VideoMap, "-an", filepath.Join(dir, "burn.mp4"))
	runTinyMedia(t, ctx, binary, args, dir)
}

func TestSoftwareToneMapUsesHDRFixture(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	dir := t.TempDir()
	input := filepath.Join(dir, "hdr.hevc")
	if err := os.WriteFile(input, hdrProbeClip, 0600); err != nil {
		t.Fatal(err)
	}
	target := ConversionTarget{VideoCodec: "h264", SourceRange: "hdr10", ToneMap: true, Width: 160, Height: 120, FrameRate: 10}
	graph, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: targetFacts()})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-v", "error", "-i", input}
	args = append(args, graph.Video...)
	args = append(args, "-frames:v", "5", "-an", filepath.Join(dir, "sdr.mp4"))
	runTinyMedia(t, ctx, binary, args, dir)
	if !strings.Contains(argValue(graph.Video, "-vf"), "tonemap=") || argValue(graph.Video, "-color_trc") != "bt709" {
		t.Fatal(graph.Video)
	}
}

func TestBackgroundConversionLimitWaitsAndCancels(t *testing.T) {
	preparedAdmission.Lock()
	before := preparedAdmission.limit
	preparedAdmission.Unlock()
	SetBackgroundConversionLimit(1)
	defer SetBackgroundConversionLimit(before)
	held, err := admitPreparedConversion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := admitPreparedConversion(ctx); err == nil {
		release()
		held()
		t.Fatal("owner cap admitted concurrent work")
	}
	held()
	release, err := admitPreparedConversion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestBitmapBurnPreservesGlyphShapeOnCroppedVideo(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	file := filepath.Join(dir, "cue.sup")
	if err := os.WriteFile(file, pgsFixture(), 0600); err != nil {
		t.Fatal(err)
	}
	graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, BurnIn: &BurnIn{File: file, Format: "pgs", InputStartUS: 5000000, PositionUS: 5000000, Width: 160, Height: 60}})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=black:s=160x60:r=24:d=1"}
	args = append(args, graph.ExtraInput...)
	for i := 0; i < len(graph.Video); i++ {
		if graph.Video[i] == "-profile:v" || graph.Video[i] == "-level:v" || graph.Video[i] == "-x264-params" {
			i++
			continue
		}
		args = append(args, graph.Video[i])
	}
	path := filepath.Join(dir, "picture.gray")
	args = append(args, "-map", graph.VideoMap, "-frames:v", "24", "-c:v", "rawvideo", "-pix_fmt", "gray", "-f", "rawvideo", path)
	runTinyMedia(t, ctx, binary, args, dir)
	frame, err := os.ReadFile(path)
	if err != nil || len(frame) != 160*60*24 {
		t.Fatal(err, len(frame))
	}
	frame = frame[12*160*60 : 13*160*60]
	left, right, top, bottom := 160, 0, 60, 0
	for i, value := range frame {
		if value > 150 {
			x, y := i%160, i/160
			left = min(left, x)
			right = max(right, x)
			top = min(top, y)
			bottom = max(bottom, y)
		}
	}
	width, height := right-left+1, bottom-top+1
	if height <= 0 || math.Abs(float64(width)/float64(height)-2) > 0.25 {
		t.Fatal("bitmap glyph distorted", width, height, left, top)
	}
}

func TestSDRHardwareScaleDoesNotRequireToneMap(t *testing.T) {
	g, err := BuildConversion(ConversionRequest{ConvertVideo: true, MaxHeight: 720, SourceVideoCodec: "h264", SourceBitDepth: 8, Probe: HardwareProbe{Backend: BackendNVENC, Available: true, ScaleFilters: true, DecodeCodecs: map[string]bool{"h264:8": true}}, Toolchain: targetFacts()})
	if err != nil || !strings.Contains(argValue(g.Video, "-vf"), "scale_cuda") || argValue(g.Input, "-hwaccel") != "cuda" {
		t.Fatal(g, err)
	}
}
func TestDolbyVisionBaseCopyStripsItsBrandAndEnhancement(t *testing.T) {
	facts := targetFacts()
	facts.BitstreamFilters = map[string]bool{"dovi_rpu": true, "filter_units": true}
	target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "dolby_vision", DolbyVisionProfile: 7}
	graph, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, Toolchain: facts})
	if err != nil || argValue(graph.Video, "-bsf:v") != "dovi_rpu=strip=1,filter_units=remove_types=63" {
		t.Fatal(graph, err)
	}
	target.DynamicRange = "hlg"
	if _, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, Toolchain: facts}); err == nil {
		t.Fatal("relabelled HDR10 as HLG")
	}
}
func TestHardwareQualityNeedsIndependentQualification(t *testing.T) {
	for _, backend := range HardwareBackends {
		if !backendSpecs[backend].supported() {
			continue
		}
		target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, RateControl: "quality"}
		proof := HardwareProbe{Backend: backend, Available: true, OutputEncoders: map[string]bool{"hevc:10": true}}
		g, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Probe: proof, Toolchain: targetFacts()})
		if err != nil || g.Backend != BackendSoftware {
			t.Fatal(g, err)
		}
		proof.OutputQuality = map[string]bool{"hevc:10": true}
		g, err = BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Probe: proof, Toolchain: targetFacts()})
		if err != nil || g.Backend != backend {
			t.Fatal(g, err)
		}
		if backend == BackendNVENC && argValue(g.Video, "-cq") == "" {
			t.Fatal(g.Video)
		}
		target.VideoBitrateBPS = 1_000_000
		g, err = BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Probe: proof, Toolchain: targetFacts()})
		if err != nil || g.Backend != BackendSoftware {
			t.Fatal("ignored quality ceiling", g, err)
		}
	}
}

func TestHDRBitmapBurnKeepsTenBitPlane(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	file := filepath.Join(dir, "cue.sup")
	if err := os.WriteFile(file, pgsFixture(), 0600); err != nil {
		t.Fatal(err)
	}
	target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", Width: 160, Height: 60, FrameRate: 24}
	graph, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, SoftwarePreset: "ultrafast", BurnIn: &BurnIn{File: file, Format: "pgs", InputStartUS: 5000000, PositionUS: 5000000, Width: 160, Height: 60}})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=black:s=160x60:r=24:d=1,format=yuv420p10le,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc"}
	args = append(args, graph.ExtraInput...)
	args = append(args, graph.Video...)
	args = append(args, "-map", graph.VideoMap, "-frames:v", "24", "-an", filepath.Join(dir, "burn.mp4"))
	runTinyMedia(t, ctx, binary, args, dir)
}

func TestSVTFixedQualityUsesACappedQualityMode(t *testing.T) {
	target := ConversionTarget{VideoCodec: "av1", RateControl: "bitrate", VideoBitrateBPS: 900000}
	args := videoRateArgs(target, "libsvtav1", "medium")
	if argValue(args, "-crf") != "28" || argValue(args, "-b:v") != "0" || argValue(args, "-maxrate") != "900000" || argValue(args, "-bufsize") != "1800000" {
		t.Fatal(args)
	}
}

func TestSVTFixedQualityEncodesUnderABitrateCeiling(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	facts, err := ProbeToolchain(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Encoders["libsvtav1"] {
		t.Skip("SVT AV1 is not available")
	}
	target := ConversionTarget{VideoCodec: "av1", RateControl: "bitrate", VideoBitrateBPS: 900000, Width: 160, Height: 90}
	graph, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: &facts})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "sample.mp4")
	args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=1"}
	args = append(args, graph.Video...)
	args = append(args, "-an", "-f", "mp4", output)
	runTinyMedia(t, ctx, binary, args, dir)
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatal("empty conversion", err)
	}
}
