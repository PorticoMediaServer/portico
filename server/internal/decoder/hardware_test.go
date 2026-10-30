package decoder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFFmpeg is a real file on disk, because the detector hashes the binary's
// identity. Its contents are never executed: Run is substituted.
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	data := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "linux" {
		native, err := exec.LookPath("true")
		if err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(native)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHardwareProbeFallsBackToSoftware(t *testing.T) {
	path := fakeFFmpeg(t)
	var calls atomic.Int64
	detector := &HardwareDetector{Run: func(context.Context, *exec.Cmd) error {
		calls.Add(1)
		return errors.New("no capable devices found")
	}}
	// "auto" tries every backend this platform can host, then settles.
	selected := detector.Select(context.Background(), path, "auto", "")
	if selected.Backend != BackendSoftware || selected.Available != true {
		t.Fatal("failed probes did not fall back to software", selected)
	}
	first := calls.Load()
	if first == 0 {
		t.Fatal("auto selection probed nothing")
	}
	// The verdict is cached by ffmpeg identity: a second selection runs nothing.
	if again := detector.Select(context.Background(), path, "auto", ""); again.Backend != BackendSoftware {
		t.Fatal(again)
	}
	if calls.Load() != first {
		t.Fatal("probe repeated for an unchanged binary", calls.Load(), first)
	}
	// A backend named explicitly that cannot run also falls back, and says why.
	named := detector.Select(context.Background(), path, string(BackendVAAPI), "")
	if named.Backend != BackendSoftware || named.Reason == ProbeAvailable {
		t.Fatal("named backend did not fall back", named)
	}
	// Replacing the binary invalidates the cache.
	if err := os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	detector.Select(context.Background(), path, "auto", "")
	if calls.Load() == first {
		t.Fatal("replaced binary reused a stale verdict")
	}
}

func TestHardwareProbeSucceedsAndContributesArguments(t *testing.T) {
	path := fakeFFmpeg(t)
	// Pick a backend this platform can actually host, so the test is meaningful
	// on macOS, Linux and Windows alike rather than skipping on two of them.
	var backend HardwareBackend
	for _, b := range HardwareBackends {
		if backendSpecs[b].supported() {
			backend = b
			break
		}
	}
	if backend == "" {
		t.Skip("no hardware backend is defined for " + runtime.GOOS)
	}
	var seen [][]string
	detector := &HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		seen = append(seen, confinedArguments(t, cmd))
		return nil
	}}
	probe := detector.Probe(context.Background(), path, backend, "")
	if !probe.Available || probe.Reason != ProbeAvailable {
		t.Fatal("probe did not succeed", probe)
	}
	if probe.Encoder != backendSpecs[backend].encoder {
		t.Fatal(probe.Encoder)
	}
	if len(seen) == 0 {
		t.Fatal("no probe process was built")
	}
	// The probe encodes a real synthetic clip rather than listing capabilities.
	joined := strings.Join(seen[0], " ")
	if !strings.Contains(joined, "lavfi") || !strings.Contains(joined, "testsrc2") || !strings.Contains(joined, probe.Encoder) || !strings.Contains(joined, "-f null") {
		t.Fatal("probe is not a real bounded encode", joined)
	}

	graph, err := BuildConversion(ConversionRequest{Probe: probe, ConvertVideo: true, ConvertAudio: true, AudioChannels: 2, VideoBitrateBPS: 4_000_000, MaxHeight: 720})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Backend != backend {
		t.Fatal("available backend not used", graph.Backend)
	}
	if !strings.Contains(strings.Join(graph.Video, " "), backendSpecs[backend].encoder) {
		t.Fatal(graph.Video)
	}
	var encode string
	for _, s := range graph.Stages {
		if s.Operation == StageEncode {
			encode = s.Execution
		}
	}
	if encode != ExecutionHardware {
		t.Fatal("hardware encode stage not published", graph.Stages)
	}
}

func TestHardwareProbeRefusesUnsupportedPlatformWithoutRunning(t *testing.T) {
	path := fakeFFmpeg(t)
	var ran atomic.Int64
	detector := &HardwareDetector{Run: func(context.Context, *exec.Cmd) error { ran.Add(1); return nil }}
	for backend, spec := range backendSpecs {
		if spec.supported() {
			continue
		}
		probe := detector.Probe(context.Background(), path, backend, "")
		if probe.Available || probe.Reason != ProbeUnsupportedPlatform {
			t.Fatal("off-platform backend probed", backend, probe)
		}
	}
	if ran.Load() != 0 {
		t.Fatal("off-platform probe started a process")
	}
}

func TestSoftwareToneMapArgumentsAndAlgorithms(t *testing.T) {
	for _, algorithm := range ToneMapAlgorithms {
		graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, ToneMap: true, ToneMapAlgorithm: algorithm, ConvertAudio: true, AudioChannels: 2})
		if err != nil {
			t.Fatal(algorithm, err)
		}
		joined := strings.Join(graph.Video, " ")
		if !strings.Contains(joined, "tonemap=tonemap="+algorithm+":desat=0") {
			t.Fatal(algorithm, joined)
		}
		if !strings.Contains(joined, "zscale=t=linear:npl=100") || !strings.Contains(joined, "format=yuv420p") {
			t.Fatal("tone-map graph incomplete", joined)
		}
	}
	if _, err := BuildConversion(ConversionRequest{ConvertVideo: true, ToneMap: true, ToneMapAlgorithm: "nonsense"}); err == nil {
		t.Fatal("unknown tone-map algorithm accepted")
	}
	// Tone mapping without a conversion is incoherent and is refused, not ignored.
	if _, err := BuildConversion(ConversionRequest{CopyVideo: true, ToneMap: true}); err == nil {
		t.Fatal("tone map accepted on a copied stream")
	}
}

func TestHDRDetectionFromProbeFacts(t *testing.T) {
	for _, transfer := range HDRTransfers {
		if !IsHDR(transfer, "bt2020") {
			t.Fatal("HDR transfer not recognised", transfer)
		}
	}
	if IsHDR("bt2020-10", "bt2020") || IsHDR("smpte428", "bt2020") {
		t.Fatal("BT.2020 SDR and DCI transfers are not HDR")
	}
	if IsHDR("", "bt2020") {
		t.Fatal("BT.2020 primaries alone must not mean HDR")
	}
	if IsHDR("bt709", "bt709") {
		t.Fatal("SDR source treated as HDR")
	}
	if IsHDR("", "") {
		t.Fatal("unmeasured source treated as HDR")
	}
}

// The real encoder, when this host has one. Everything here is bounded and the
// test skips cleanly where ffmpeg is not installed.
func TestHardwareProbeAgainstRealFFmpeg(t *testing.T) {
	path := decodertest.QualifiedFFmpeg(t)
	if !filepath.IsAbs(path) {
		t.Skip("ffmpeg path is not absolute")
	}
	detector := &HardwareDetector{Timeout: 25 * time.Second}
	report := detector.Report(context.Background(), path, "")
	if len(report) < 2 {
		t.Fatal("report omitted backends", report)
	}
	var software bool
	for _, probe := range report {
		if probe.Backend == BackendSoftware {
			software = probe.Available
			continue
		}
		if probe.Available && probe.Reason != ProbeAvailable {
			t.Fatal("available backend without an available reason", probe)
		}
		if !probe.Available && probe.Reason == ProbeAvailable {
			t.Fatal("unavailable backend claiming availability", probe)
		}
	}
	if !software {
		t.Fatal("software encoding reported unavailable")
	}
	selected := detector.Select(context.Background(), path, "auto", "")
	graph, err := BuildConversion(ConversionRequest{Probe: selected, ConvertVideo: true, ConvertAudio: true, AudioChannels: 2, VideoBitrateBPS: 2_000_000, MaxHeight: 480})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Stages) == 0 {
		t.Fatal("conversion published no stages")
	}
}

func TestConversionForcesHardwareIDRFrames(t *testing.T) {
	for _, backend := range append([]HardwareBackend{BackendSoftware}, HardwareBackends...) {
		t.Run(string(backend), func(t *testing.T) {
			spec := backendSpecs[backend]
			if backend != BackendSoftware {
				spec.platforms = nil
				original := backendSpecs[backend]
				backendSpecs[backend] = spec
				defer func() { backendSpecs[backend] = original }()
			}
			graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, Probe: HardwareProbe{Backend: backend, Available: true}})
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(graph.Video, " ")
			if !strings.Contains(args, "-force_key_frames expr:gte(t,n_forced*6)") {
				t.Fatal(args)
			}
			option := ""
			switch backend {
			case BackendNVENC:
				option = "-forced-idr 1"
			case BackendQSV, BackendAMF:
				option = "-forced_idr 1"
			}
			if option != "" && !strings.Contains(args, option) {
				t.Fatalf("missing %s: %s", option, args)
			}
			if option == "" && (strings.Contains(args, "-forced-idr") || strings.Contains(args, "-forced_idr")) {
				t.Fatal(args)
			}
		})
	}
}

func TestVAAPINativeProbeUsesUploadedHDRInput(t *testing.T) {
	if !backendSpecs[BackendVAAPI].supported() {
		t.Skip("VA-API requires Linux")
	}
	d := &HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		args := confinedArguments(t, cmd)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "tonemap_vaapi") {
			return nil
		}
		if strings.Contains(joined, "testsrc2") {
			t.Fatal("tone mapper received SDR testsrc instead of HDR fixture")
		}
		if !strings.Contains(joined, "format=p010,hwupload,tonemap_vaapi=") {
			t.Fatal("native tone mapper must receive uploaded ten-bit frames", joined)
		}
		for i, arg := range args {
			if arg == "-i" {
				data, err := os.ReadFile(args[i+1])
				if err != nil {
					t.Fatal(err)
				}
				if len(data) == 0 {
					t.Fatal("empty HDR probe input")
				}
			}
		}
		return nil
	}}
	probe := d.Probe(context.Background(), fakeFFmpeg(t), BackendVAAPI, "")
	if !probe.Available || !probe.NativeFilters {
		t.Fatal(probe)
	}
}

func TestConversionStagesCarryDeviceRequirement(t *testing.T) {
	for _, tc := range []struct {
		request  ConversionRequest
		hardware bool
	}{
		{ConversionRequest{ConvertVideo: true}, false},
		{ConversionRequest{CopyVideo: true, CopyAudio: true, Probe: HardwareProbe{Backend: BackendNVENC, Available: true}}, false},
		{ConversionRequest{ConvertVideo: true, ToneMap: true, Toolchain: &ToolchainFacts{VulkanAvailable: true, Filters: map[string]bool{"libplacebo": true}}}, true},
		{ConversionRequest{ConvertVideo: true, ToneMap: true, Toolchain: &ToolchainFacts{OpenCLAvailable: true, Filters: map[string]bool{"tonemap_opencl": true}}}, true},
	} {
		graph, err := BuildConversion(tc.request)
		if err != nil {
			t.Fatal(err)
		}
		if HardwareStagesRequireDevice(graph.Stages) != tc.hardware {
			t.Fatal(graph)
		}
	}
}

func TestHardwareProbeQualifiesForcedIDROptions(t *testing.T) {
	for backend, spec := range backendSpecs {
		t.Run(string(backend), func(t *testing.T) {
			for _, filters := range []string{"", "native_filters"} {
				args := strings.Join(probeArgs(spec, "", filters), " ")
				option := ""
				switch backend {
				case BackendNVENC:
					option = "-forced-idr 1"
				case BackendQSV, BackendAMF:
					option = "-forced_idr 1"
				}
				if option != "" && !strings.Contains(args, option) {
					t.Fatalf("probe lacks production option %s: %s", option, args)
				}
				if option == "" && (strings.Contains(args, "-forced-idr") || strings.Contains(args, "-forced_idr")) {
					t.Fatal("probe uses unsupported option", args)
				}
			}
		})
	}
}

func TestHardwareProbeRejectsUnsupportedForcedIDROption(t *testing.T) {
	var backend HardwareBackend
	for _, candidate := range []HardwareBackend{BackendNVENC, BackendQSV, BackendAMF} {
		if backendSpecs[candidate].supported() {
			backend = candidate
			break
		}
	}
	if backend == "" {
		t.Skip("no backend with a forced-IDR option on this platform")
	}
	d := &HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		args := strings.Join(confinedArguments(t, cmd), " ")
		if strings.Contains(args, "-forced-idr") || strings.Contains(args, "-forced_idr") {
			return errors.New("Unrecognized option: forced IDR")
		}
		return nil
	}}
	path := fakeFFmpeg(t)
	if probe := d.Probe(context.Background(), path, backend, ""); probe.Available || probe.Reason != ProbeEncodeFailed {
		t.Fatal("unsupported production option passed qualification", probe)
	}
	if selected := d.SelectCached(path, string(backend), ""); selected.Backend != BackendSoftware {
		t.Fatal(selected)
	}
}
