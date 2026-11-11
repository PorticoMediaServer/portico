package streaming

// Review of #2391 (must-change 1). Copy into server-next/internal/streaming/.
// A converter stopped for going silent (a remote source that paused) says
// nothing about the hardware encoder, so the stream keeps its hardware plan and
// no "hardware encoder failed" is recorded for the owner.

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

func TestAStalledHardwareConverterIsNotMovedToSoftware(t *testing.T) {
	silence := converterSilence
	converterSilence = 300 * time.Millisecond
	t.Cleanup(func() { converterSilence = silence })
	rig := newMediaRig(t)
	path := rig.generate("stalls.avi", 60, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	var runs atomic.Int32
	rig.streams.hls.execute = func(ctx context.Context, _ mediaexec.Job) (error, string) {
		runs.Add(1)
		<-ctx.Done() // silent: the source stopped sending
		return ctx.Err(), ""
	}
	request, info := rig.request(path)
	plan, err := Plan(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.VideoAction != "convert" {
		t.Skip("the clip is not converted here")
	}
	if plan.ConversionTarget == nil {
		t.Skip("no conversion target")
	}
	// A hardware encoder the toolchain measured, as on a machine with one.
	key := plan.ConversionTarget.VideoCodec + ":" + strconv.Itoa(plan.ConversionTarget.BitDepth)
	plan.ConversionProbe = &decoder.HardwareProbe{Backend: decoder.BackendVAAPI, Available: true, Device: "/dev/dri/renderD128",
		Identity: decoder.CurrentToolchain().Identity, OutputEncoders: map[string]bool{key: true}, OutputQuality: map[string]bool{key: true}}
	plan.HardwareBackend, plan.HardwareDevice = string(decoder.BackendVAAPI), "/dev/dri/renderD128"
	stream, err := rig.streams.Start(context.Background(), StartRequest{
		SessionID: "stall", Plan: plan, Request: &request, Path: path, Size: info.Size(), Modified: info.ModTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rig.streams.Stop("stall")
	deadline := time.Now().Add(20 * time.Second)
	for {
		rig.get(context.Background(), stream.Grant, "segment-000000.ts", "")
		status, _ := rig.streams.Status("stall")
		if status.Plan.HardwareBackend != string(decoder.BackendVAAPI) {
			t.Fatalf("after a stall the stream's plan became %q: a paused source moved the rest of the film to software", status.Plan.HardwareBackend)
		}
		if status.Failure != nil && strings.HasPrefix(status.Failure.Code, "hardware_encoder_failed") {
			t.Fatalf("a stall was recorded as %q", status.Failure.Code)
		}
		if runs.Load() >= 2 || status.State == "failed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no restart after a stall (%d runs)", runs.Load())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A converter stopped from outside while it encodes on the graphics card (the kernel when
// memory ran out, somebody's kill) says nothing about the card either: the stream keeps its
// hardware plan, nothing is counted against the card, and the converter is started again.
func TestAHardwareConverterKilledFromOutsideIsNotAHardwareFailure(t *testing.T) {
	rig := newMediaRig(t)
	path := rig.generate("killed.avi", 60, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3")
	var runs atomic.Int32
	rig.streams.hls.execute = func(context.Context, mediaexec.Job) (error, string) {
		runs.Add(1)
		return exec.Command("sh", "-c", "kill -9 $$").Run(), ""
	}
	request, info := rig.request(path)
	plan, err := Plan(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.VideoAction != "convert" || plan.ConversionTarget == nil {
		t.Skip("the clip is not converted here")
	}
	key := plan.ConversionTarget.VideoCodec + ":" + strconv.Itoa(plan.ConversionTarget.BitDepth)
	plan.ConversionProbe = &decoder.HardwareProbe{Backend: decoder.BackendVAAPI, Available: true, Device: "/dev/dri/renderD128",
		Identity: decoder.CurrentToolchain().Identity, OutputEncoders: map[string]bool{key: true}, OutputQuality: map[string]bool{key: true}}
	plan.HardwareBackend, plan.HardwareDevice = string(decoder.BackendVAAPI), "/dev/dri/renderD128"
	stream, err := rig.streams.Start(context.Background(), StartRequest{
		SessionID: "killed", Plan: plan, Request: &request, Path: path, Size: info.Size(), Modified: info.ModTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rig.streams.Stop("killed")
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		rig.get(context.Background(), stream.Grant, "segment-000000.ts", "")
		status, _ := rig.streams.Status("killed")
		if status.Plan.HardwareBackend != string(decoder.BackendVAAPI) || rig.streams.hls.hardwareFailures.Load() != 0 {
			t.Fatalf("after a kill from outside the plan is %q and %d hardware failures are counted", status.Plan.HardwareBackend, rig.streams.hls.hardwareFailures.Load())
		}
		if status.State == "failed" {
			// Three tries, all killed: it ends as a converter that keeps stopping, not as a card that failed.
			if status.Failure == nil || status.Failure.Code != FailureConverterStopped || runs.Load() != converterDeaths {
				t.Fatalf("failed as %+v after %d runs", status.Failure, runs.Load())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s after %d kills", status.State, runs.Load())
		}
	}
}
