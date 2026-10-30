package playback

import (
	"context"
	"testing"
)

func TestAirPlayCopyFallbackKeepsReceiverIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping measured slow test in short mode")
	}
	f := newReplanFixture(t, 1_600_000)
	session := f.start("airplay-fallback", V1Choice{AirPlay: true})
	before := f.plan(session.ID)
	if !before.AirPlay || before.Mode != "hls" || before.VideoAction != "copy" || len(before.Renditions) != 0 {
		t.Fatalf("initial receiver plan: %+v", before)
	}
	converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, before)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []*DeliveryPlan{converted, f.plan(session.ID)} {
		if !plan.AirPlay || plan.Mode != "hls" || plan.VideoAction != "convert" || len(plan.Renditions) != 0 || plan.AudioStream != before.AudioStream {
			t.Fatalf("fallback lost receiver choices: airplay=%t mode=%q video=%q renditions=%+v audio=%d wantAudio=%d", plan.AirPlay, plan.Mode, plan.VideoAction, plan.Renditions, plan.AudioStream, before.AudioStream)
		}
		if plan.OutputContainer != "mpegts_hls" || plan.OutputVideoCodec != "h264" || plan.OutputAudioCodec != "aac" {
			t.Fatalf("fallback escaped receiver capabilities: container=%q video=%q audio=%q", plan.OutputContainer, plan.OutputVideoCodec, plan.OutputAudioCodec)
		}
	}
}
