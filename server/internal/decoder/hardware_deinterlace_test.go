package decoder

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestHardwareDeinterlaceRequiresItsOwnProbeAndPreservesFilterOrder(t *testing.T) {
	backend := BackendVAAPI
	if !backendSpecs[backend].supported() {
		t.Skip("VAAPI graph requires Linux")
	}
	request := ConversionRequest{ConvertVideo: true, Deinterlace: true, SourceVideoCodec: "h264", SourceBitDepth: 8,
		Probe: HardwareProbe{Backend: backend, Available: true, ScaleFilters: true, DecodeCodecs: map[string]bool{"h264:8": true}}}
	graph, err := BuildConversion(request)
	filter := argValue(graph.Video, "-vf")
	if err != nil || !strings.HasPrefix(filter, "hwdownload,format=nv12,yadif=") || strings.Contains(filter, "deinterlace_vaapi") {
		t.Fatal("unprobed deinterlacer bypassed the software fallback", graph, err)
	}
	request.Probe.DeinterlaceFilters = true
	graph, err = BuildConversion(request)
	filter = argValue(graph.Video, "-vf")
	if err != nil || !strings.HasPrefix(filter, nativeDeinterlace(backend)+",") || strings.Contains(filter, "hwdownload") {
		t.Fatal("qualified hardware deinterlace did not stay on device surfaces", graph, err)
	}
	found := false
	for _, stage := range graph.Stages {
		if stage.Operation == StageDeinterlace {
			found = stage.Execution == ExecutionHardware
		}
	}
	if !found {
		t.Fatal("hardware deinterlacing not reported by the actual graph", graph.Stages)
	}
	request.SoftwareDecode = true
	graph, err = BuildConversion(request)
	filter = argValue(graph.Video, "-vf")
	if err != nil || !strings.Contains(filter, "hwupload") || !strings.Contains(filter, "deinterlace_vaapi") || strings.Index(filter, "hwupload") > strings.Index(filter, "deinterlace_vaapi") || argValue(graph.Input, "-hwaccel") != "" {
		t.Fatal("software-decode frames did not upload before hardware deinterlace", graph, err)
	}
}

func TestHardwareDeinterlaceProbeUsesInterlacedInputAndCannotBypassFilter(t *testing.T) {
	backend := supportedTestBackend(t)
	spec := backendSpecs[backend]
	if spec.upload == "" {
		backend, spec = BackendVAAPI, backendSpecs[BackendVAAPI]
	}
	calls := 0
	d := HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		calls++
		args := confinedArguments(t, cmd)
		filter := argValue(args, "-vf")
		if !strings.HasPrefix(filter, "setfield=tff,") || !strings.HasSuffix(filter, nativeDeinterlace(backend)) {
			t.Fatal("progressive/filter-free probe could falsely pass", args)
		}
		return errors.New("controlled deinterlacer unavailable")
	}}
	if d.probeDeinterlace(context.Background(), fakeFFmpeg(t), spec, backend, "", time.Second) || calls != 1 {
		t.Fatal("failed deinterlace test became available", calls)
	}
	if d.probeDeinterlace(context.Background(), fakeFFmpeg(t), backendSpec{}, BackendVideoToolbox, "", time.Second) || calls != 1 {
		t.Fatal("missing upload/filter chain became a deinterlace capability", calls)
	}
}
