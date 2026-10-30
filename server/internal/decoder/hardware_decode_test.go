package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func TestHardwareDecodeUsesSourceCellInsteadOfOutput(t *testing.T) {
	backend := BackendVAAPI
	if !backendSpecs[backend].supported() {
		t.Skip("VAAPI graph requires Linux")
	}
	target := ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr"}
	request := ConversionRequest{Target: &target, ConvertVideo: true, Probe: HardwareProbe{Backend: backend, Available: true, ScaleFilters: true, DecodeCodecs: map[string]bool{"h264:8": true, "hevc:10": true}}, SourceVideoCodec: "hevc", SourceBitDepth: 10}
	graph, err := BuildConversion(request)
	if err != nil || argValue(graph.Input, "-hwaccel") != "vaapi" {
		t.Fatal("qualified source decode was not used", graph, err)
	}
	for _, source := range []struct {
		codec string
		depth int
	}{{"", 0}, {"hevc", 12}, {"vp9", 8}} {
		request.SourceVideoCodec, request.SourceBitDepth = source.codec, source.depth
		graph, err := BuildConversion(request)
		if err != nil || argValue(graph.Input, "-hwaccel") != "" || !strings.Contains(argValue(graph.Video, "-vf"), "hwupload") {
			t.Fatal("output H264 or another source depth granted unmeasured decode", source, graph, err)
		}
	}
	request.SourceVideoCodec, request.SourceBitDepth = "hevc", 10
	target.FrameRate = 24 // A supplied target owns the output geometry/rate.
	graph, err = BuildConversion(request)
	filter := argValue(graph.Video, "-vf")
	if err != nil || !strings.HasPrefix(filter, "hwdownload,format=p010le,fps=") || strings.Index(filter, "hwupload") < strings.Index(filter, "fps=") || argValue(graph.Input, "-hwaccel") != "vaapi" {
		t.Fatal("hardware decode did not cross system-frame filters with bounded transfers", graph, err)
	}
}

func TestHardwareDecodeTargetRuntime(t *testing.T) {
	path := os.Getenv("PORTICO_TEST_HARDWARE_FFMPEG")
	if path == "" {
		t.Skip("explicit retained f3 pair and physical render device required")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.New()
	_, err = io.Copy(sum, file)
	file.Close()
	if err != nil || hex.EncodeToString(sum.Sum(nil)) != "e45b768839cb9d169196f6b94845b334b7bfc439e6cf6b301efe769c5f3b5b2e" {
		t.Fatal("physical target test requires the exact retained f3 FFmpeg", err)
	}
	backend := HardwareBackend(os.Getenv("PORTICO_TEST_HARDWARE_BACKEND"))
	if backend != BackendVAAPI && backend != BackendQSV {
		t.Fatal("this target gate is limited to the observed Intel render device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	detector := HardwareDetector{Timeout: 5 * time.Second}
	probe := detector.Probe(ctx, path, backend, "/dev/dri/renderD128")
	posture := mediaexec.Decide(path)
	result, _ := json.Marshal(struct {
		Backend     HardwareBackend `json:"backend"`
		Encoder     bool            `json:"encoder"`
		Decode      map[string]bool `json:"decode"`
		Output      map[string]bool `json:"outputEncoders"`
		Quality     map[string]bool `json:"outputQuality"`
		Scale       bool            `json:"scale"`
		ToneMap     bool            `json:"toneMap"`
		Deinterlace bool            `json:"deinterlace"`
		Sandbox     bool            `json:"sandbox"`
		Reason      string          `json:"sandboxReason"`
	}{backend, probe.Available, probe.DecodeCodecs, probe.OutputEncoders, probe.OutputQuality, probe.ScaleFilters, probe.ToneMapFilters, probe.DeinterlaceFilters, posture.Sandboxed, posture.Reason})
	t.Log(string(result))
	if !probe.Available || !probe.DecodeCodecs["h264:8"] {
		t.Fatal("observed Intel device did not pass the required H264 encode/decode cell", probe.Reason)
	}
	if _, claimed := probe.DecodeCodecs["vc1:8"]; claimed {
		t.Fatal("unmeasured VC1 became a target claim")
	}
}

func TestHardwareDecodeProbeCannotPassWithSoftwareFrames(t *testing.T) {
	backend := BackendVAAPI
	if !backendSpecs[backend].supported() {
		t.Skip("VAAPI probe dialect requires Linux")
	}
	var generated []string
	attempts := 0
	d := HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		args := confinedArguments(t, cmd)
		if argValue(args, "-i") == "testsrc2=size=160x128:rate=25" {
			file := args[len(args)-1]
			generated = append(generated, file)
			// A controlled command-construction fixture, never executed media.
			return os.WriteFile(file, []byte("controlled encoded fixture"), 0600)
		}
		attempts++
		if strings.HasPrefix(filepath.Base(argValue(args, "-i")), "mpeg2-") && argValue(args, "-f") != "mpegvideo" {
			t.Fatal("MPEG-2 muxer name was mistaken for its demuxer", args)
		}
		if argValue(args, "-hwaccel_output_format") != "vaapi" || !strings.HasPrefix(argValue(args, "-vf"), "hwdownload,format=") {
			t.Fatal("decoder software fallback could satisfy probe", args)
		}
		if strings.Contains(filepath.Base(argValue(args, "-i")), "-10") {
			return errors.New("controlled failed hardware surface")
		}
		return nil
	}}
	cells := d.probeDecodeCodecs(context.Background(), fakeFFmpeg(t), backend, "/dev/dri/renderD128", time.Second)
	if attempts != 10 || !cells["h264:8"] || cells["hevc:10"] || cells["av1:10"] {
		t.Fatal("codec/depth probe outcomes were inferred", cells, attempts)
	}
	if _, measured := cells["vc1:8"]; measured {
		t.Fatal("VC1 was claimed without a licensed fixture")
	}
	for _, file := range generated {
		if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
			t.Fatal("finished decode fixtures not removed", file, err)
		}
	}
}
