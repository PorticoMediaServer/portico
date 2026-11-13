package streaming

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestConversionLoadChild(t *testing.T) {
	if os.Getenv("PORTICO_LOAD_CHILD") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

func TestConversionLoadFollowsStartedEncodersAndFallback(t *testing.T) {
	h := &HLS{}
	s := &Streams{hls: h}
	check := func(hardware, processor int) {
		t.Helper()
		a, b := s.ConversionLoad()
		if a != hardware || b != processor {
			t.Fatalf("got %d hardware, %d processor; want %d,%d", a, b, hardware, processor)
		}
	}
	begin, end := h.conversionCount()
	check(0, 0) // A plan and an admitted run have not started an encoder.
	plan := &DeliveryPlan{VideoAction: "convert", HardwareBackend: "vaapi"}
	stages := []decoder.HardwareStage{{Operation: decoder.StageEncode, Execution: decoder.ExecutionHardware}}
	kind := conversionKind(plan, []string{"-c:v", "h264_vaapi"}, stages)
	input, hold, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer hold.Close()
	ctx := childprocess.WithStarted(context.Background(), func() { begin(kind) })
	cmd := exec.Command(os.Args[0], "-test.run=^TestConversionLoadChild$")
	cmd.Env = append(os.Environ(), "PORTICO_LOAD_CHILD=1")
	cmd.Stdin = input
	defer func() { hold.Close(); _ = cmd.Wait() }()
	if err := childprocess.Start(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	hold.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	end()
	check(0, 0)
	// The hardware plan remains, but the observed fallback encoder is software.
	begin, end = h.conversionCount()
	stages = []decoder.HardwareStage{{Operation: decoder.StageEncode, Execution: decoder.ExecutionSoftware}}
	begin(conversionKind(plan, []string{"-c:v", "libx264"}, stages))
	check(0, 1)
	begin("software")
	check(0, 1)
	end()
	end()
	check(0, 0)
	begin, end = h.conversionCount()
	begin(conversionKind(&DeliveryPlan{VideoAction: "copy", AudioAction: "convert"}, nil, nil))
	check(0, 1)
	end()
	begin, end = h.conversionCount()
	begin(conversionKind(&DeliveryPlan{VideoAction: "copy", AudioAction: "copy"}, nil, nil))
	check(0, 0)
	end()
	begin, end = h.conversionCount()
	end()
	begin("hardware")
	check(0, 0)
}
