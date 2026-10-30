package decoder

import (
	"strings"
	"testing"
)

func TestSoftwareDecodeKeepsHardwareFilterAndEncoder(t *testing.T) {
	exercised := false
	for _, backend := range []HardwareBackend{BackendVAAPI, BackendQSV, BackendNVENC} {
		if !backendSpecs[backend].supported() {
			continue
		}
		exercised = true
		request := ConversionRequest{Probe: HardwareProbe{Backend: backend, Available: true, NativeFilters: true, DecodeCodecs: map[string]bool{"h264:8": true}}, SourceVideoCodec: "h264", SourceBitDepth: 8, ConvertVideo: true, MaxHeight: 720, SoftwareDecode: true}
		graph, err := BuildConversion(request)
		if err != nil || argValue(graph.Input, "-hwaccel") != "" || !strings.Contains(argValue(graph.Video, "-vf"), "hwupload") || graph.Backend != backend {
			t.Fatal("decode-off did not keep software decode and hardware upload/encode", backend, graph, err)
		}
		for _, stage := range graph.Stages {
			if stage.Operation == StageDecode && stage.Execution != ExecutionSoftware {
				t.Fatal("decode-off graph advertised hardware decode", stage)
			}
			if stage.Operation == StageScale && stage.Execution != ExecutionHardware {
				t.Fatal("software decode unnecessarily disabled qualified hardware scaling", stage)
			}
		}
		request.SoftwareDecode = false
		enabled, err := BuildConversion(request)
		if err != nil || argValue(enabled.Input, "-hwaccel") == "" {
			t.Fatal("existing decode-enabled graph regressed", backend, enabled, err)
		}
		request.DisableHardwareDecode = true
		geometry, err := BuildConversion(request)
		if err != nil || argValue(geometry.Input, "-hwaccel") != "" || strings.Contains(argValue(geometry.Video, "-vf"), backendSpecs[backend].scale(720)) {
			t.Fatal("legacy geometry guard lost system-frame correction", backend, geometry, err)
		}
	}
	if !exercised {
		t.Skip("this platform has no uploaded-surface backend")
	}
}
