package decoder

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestToneMappingProbeKeepsMasteredAndUnmasteredResultsSeparate(t *testing.T) {
	if !backendSpecs[BackendVAAPI].supported() {
		t.Skip("VA-API requires Linux")
	}
	mastered, unmastered := false, false
	d := &HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		args := confinedArguments(t, cmd)
		if !strings.Contains(strings.Join(args, " "), "tonemap_vaapi=") {
			return nil
		}
		for i, arg := range args {
			if arg != "-i" {
				continue
			}
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case bytes.Equal(data, hdrUnmasteredProbeClip):
				unmastered = true
				return errors.New("No mastering display data from input")
			case bytes.Equal(data, hdrProbeClip):
				mastered = true
			}
		}
		return nil
	}}
	probe := d.Probe(context.Background(), fakeFFmpeg(t), BackendVAAPI, "")
	if !mastered || !unmastered || !probe.Available || !probe.ScaleFilters || !probe.NativeFilters || !probe.ToneMapFilters || probe.ToneMapWithoutMastering {
		t.Fatalf("a metadata-dependent tone mapper must keep its working mastered result and reject only the unmastered case: %+v; clips=%v,%v", probe, mastered, unmastered)
	}
}

func TestToneMappingSelectsTheMeasuredSourceMetadataCase(t *testing.T) {
	if !backendSpecs[BackendVAAPI].supported() {
		t.Skip("VA-API requires Linux")
	}
	probe := HardwareProbe{Backend: BackendVAAPI, Available: true, NativeFilters: true, ToneMapFilters: true}
	for _, mastered := range []bool{true, false} {
		target := ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", SourceRange: "hdr10", ToneMap: true}
		graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, ToneMap: true, SourceHasMasteringDisplay: mastered, Target: &target, Probe: probe})
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(graph.Video, " ")
		if strings.Contains(args, "tonemap_vaapi=") != mastered || !strings.Contains(args, "h264_vaapi") {
			t.Fatal(mastered, args)
		}
		if !mastered && !strings.Contains(args, "tonemap=") {
			t.Fatal("unmastered source lost software tone mapping", args)
		}
		if target.MasterDisplay != "" {
			t.Fatal("SDR output gained HDR mastering metadata", target)
		}
	}
}
