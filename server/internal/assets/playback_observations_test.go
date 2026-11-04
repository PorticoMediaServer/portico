package assets

import "testing"

func TestFrameObservationFailureAndShortSamplesKeepHeaderAnswers(t *testing.T) {
	header := StreamDetail{DynamicRange: RangeDolbyVision, DolbyVisionProfile: 8, DolbyVisionCompatibility: 1, MasterDisplay: "header mastering", MaxCLL: "1000,400"}
	for _, raw := range []string{"", `{}`, `{"frames":[]}`, `{"frames":[`, `{"frames":[null]}`, `{"frames":[7]}`} {
		for _, apply := range []func([]byte, *StreamDetail) error{ApplyFrameCadence, ApplyFrameHDR} {
			got := header
			if err := apply([]byte(raw), &got); err == nil || got != header {
				t.Fatalf("incomplete probe changed facts: %s %+v %v", raw, got, err)
			}
		}
	}
	got := header
	if err := ApplyFrameCadence([]byte(`{"frames":[{"best_effort_timestamp_time":"0.0","interlaced_frame":0},{"best_effort_timestamp_time":"0.04","interlaced_frame":0}]}`), &got); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFrameHDR([]byte(`{"frames":[{"media_type":"video","width":1920,"height":1080}]}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.ObservedInterlaced() != nil || got.FrameRateMode != "" || got.HDR10Plus || got.DolbyVisionMetadataKnown || got.DolbyVisionRPU || got.DolbyVisionBaseLayer || got.DynamicRange != header.DynamicRange || got.DolbyVisionProfile != 8 || got.MasterDisplay != header.MasterDisplay || got.MaxCLL != header.MaxCLL {
		t.Fatal("short/no-side-data sample established an absent fact or lost headers", got)
	}
}
