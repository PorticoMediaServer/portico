package decoder

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func supportedTestBackend(t *testing.T) HardwareBackend {
	t.Helper()
	for _, backend := range HardwareBackends {
		if backendSpecs[backend].supported() {
			return backend
		}
	}
	t.Skip("no hardware backend on this platform")
	return ""
}

func TestHardwareOutputsPromoteOnlyMeasuredCodecDepth(t *testing.T) {
	backend := supportedTestBackend(t)
	probe := HardwareProbe{Backend: backend, Available: true, Identity: "controlled-selected-device", OutputEncoders: map[string]bool{"h264:8": true, "hevc:10": true}}
	facts := targetFacts()
	facts.Identity = probe.Identity
	out := AvailableConversionOutputsForHardware(facts, probe)
	if len(out.Video) != 5 || out.Video[0].Codec != "hevc" || out.Video[0].BitDepth != 10 || out.Video[1].Codec != "h264" || !out.Video[1].Hardware {
		t.Fatal("hardware H264 did not outrank software AV1", out.Video)
	}
	for _, video := range out.Video {
		if video.Hardware != (video.Codec == "h264" || video.Codec == "hevc" && video.BitDepth == 10) {
			t.Fatal("unmeasured codec or depth marked hardware", video)
		}
	}
	raw, err := json.Marshal(out)
	if err != nil || strings.Contains(string(raw), "hardware") {
		t.Fatal("private planner hint escaped into a public output document", string(raw), err)
	}
	for _, invalid := range []HardwareProbe{{Backend: backend, Available: true, OutputEncoders: probe.OutputEncoders}, {Backend: backend, Identity: probe.Identity, OutputEncoders: probe.OutputEncoders}, {Backend: BackendSoftware, Available: true, Identity: probe.Identity, OutputEncoders: probe.OutputEncoders}} {
		for _, output := range AvailableConversionOutputsForHardware(facts, invalid).Video {
			if output.Hardware {
				t.Fatal("unqualified backend gained hardware output", invalid, output)
			}
		}
	}
	for _, identity := range []string{"", "different-selected-toolchain"} {
		facts.Identity = identity
		for _, output := range AvailableConversionOutputsForHardware(facts, probe).Video {
			if output.Hardware {
				t.Fatal("mismatched toolchain gained hardware ranking", identity, output)
			}
		}
	}
	facts.Identity = probe.Identity
	delete(facts.Encoders, "libx264")
	for _, output := range AvailableConversionOutputsForHardware(facts, probe).Video {
		if output.Codec == "h264" {
			t.Fatal("invented output without its software fallback", output)
		}
	}
}

func TestHardwareAV1RequiresIndependentDepthAndQualityProbes(t *testing.T) {
	backend := supportedTestBackend(t)
	clearHardwareCircuit(backend)
	t.Cleanup(func() { clearHardwareCircuit(backend) })
	seen8, seen10, quality := false, false, false
	detector := HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		args := confinedArguments(t, cmd)
		if argValue(args, "-c:v") != "av1_"+string(backend) {
			return nil
		}
		if strings.Contains(argValue(args, "-vf"), "p010") || argValue(args, "-pix_fmt") == "yuv420p10le" {
			seen10 = true
			return errors.New("controlled unsupported 10-bit AV1 encoder")
		}
		seen8 = true
		if argValue(args, "-b:v") != "200k" {
			quality = true
			return errors.New("controlled unsupported AV1 quality mode")
		}
		return nil
	}}
	probe := detector.Probe(context.Background(), fakeFFmpeg(t), backend, "")
	if !seen8 || !seen10 || !quality || !probe.OutputEncoders["av1:8"] || probe.OutputEncoders["av1:10"] || probe.OutputQuality["av1:8"] {
		t.Fatal("AV1 capability inferred instead of independently probed", probe, seen8, seen10, quality)
	}
	target := ConversionTarget{VideoCodec: "av1", BitDepth: 8, DynamicRange: "sdr", RateControl: "quality"}
	graph, err := BuildConversion(ConversionRequest{Target: &target, Probe: probe, ConvertVideo: true, Toolchain: targetFacts()})
	if err != nil || graph.Backend != BackendSoftware || argValue(graph.Video, "-c:v") != "libaom-av1" {
		t.Fatal("unqualified hardware quality mode lost the CPU fallback", graph, err)
	}
}

func TestHardwareGraphFallsBackForAnotherSelectedRuntime(t *testing.T) {
	facts := targetFacts()
	facts.Identity = "current-runtime"
	probe := HardwareProbe{Backend: supportedTestBackend(t), Available: true, Identity: facts.Identity,
		NativeFilters: true, DecodeCodecs: map[string]bool{"hevc:10": true}}
	request := ConversionRequest{ConvertVideo: true, SourceVideoCodec: "hevc", SourceBitDepth: 10, Toolchain: facts, Probe: probe}
	graph, err := BuildConversion(request)
	if err != nil || graph.Backend != probe.Backend {
		t.Fatal("matching runtime lost its measured hardware route", graph, err)
	}
	request.Probe.Identity = "previous-runtime"
	graph, err = BuildConversion(request)
	if err != nil || graph.Backend != BackendSoftware || HardwareStagesRequireDevice(graph.Stages) || argValue(graph.Video, "-c:v") != "libx264" {
		t.Fatal("previous runtime's hardware result survived graph composition", graph, err)
	}
}
