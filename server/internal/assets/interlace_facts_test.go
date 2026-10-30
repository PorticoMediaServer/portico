package assets

import "testing"

func TestObservedInterlaceFieldOrder(t *testing.T) {
	for _, order := range []string{"progressive", "tt", "bb", "tb", "bt", "unknown", "", "UNSPECIFIED"} {
		d := streamDetail("video", "h264", 1920, 1080, probeStreamDetail{FieldOrder: order})
		actual := d.ObservedInterlaced()
		known := order == "progressive" || order == "tt" || order == "bb" || order == "tb" || order == "bt"
		if (actual != nil) != known {
			t.Fatalf("%q %+v %v", order, d, actual)
		}
		if actual != nil && *actual != (order != "progressive") {
			t.Fatal(order, *actual)
		}
	}
}

func TestObservedInterlaceDecodedFramesOverrideOnlyWithEvidence(t *testing.T) {
	for _, initial := range []string{"", InterlaceFieldOrderProgressive, InterlaceFieldOrderInterlaced} {
		d := StreamDetail{StreamBreadth: StreamBreadth{InterlaceAnalysis: initial}}
		analyzeFrames([]byte(`{"frames":[{"best_effort_timestamp_time":"0","interlaced_frame":0},{"best_effort_timestamp_time":"0.04","interlaced_frame":0}]}`), &d)
		if initial == "" && d.ObservedInterlaced() != nil {
			t.Fatal("short progressive window invented stream scan", d)
		}
		if initial != "" && d.InterlaceAnalysis != initial {
			t.Fatal("known field order lost", d)
		}
		analyzeFrames([]byte(`{"frames":[{"best_effort_timestamp_time":"0","interlaced_frame":0},{"best_effort_timestamp_time":"0.04","interlaced_frame":1}]}`), &d)
		if got := d.ObservedInterlaced(); got == nil || !*got {
			t.Fatal("actual interlaced frame lost", d)
		}
	}
	if got := (StreamBreadth{InterlaceAnalysis: "decoded_frames"}).ObservedInterlaced(); got != nil {
		t.Fatal("old window record became progressive")
	}
}
