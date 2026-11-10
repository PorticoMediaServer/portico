package streaming

import (
	"math"
	"testing"
)

func TestUnindexedPacketTimelineKeepsTheLastKeyframe(t *testing.T) {
	writer := newPacketTimelineWriter(1.4, 19)
	for _, chunk := range []string{"1.4,K_\n1.44,__\n5.", "4,K_\n9.4,K_\n13.4,K_\n17.4,K_\n"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	timeline, err := writer.timeline()
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 4, 12, 16, 19}
	if len(timeline.Boundaries) != len(want) {
		t.Fatal(timeline.Boundaries)
	}
	for i := range want {
		if math.Abs(timeline.Boundaries[i]-want[i]) > 0.000001 {
			t.Fatal(timeline.Boundaries)
		}
	}
	bad := newPacketTimelineWriter(0, 60)
	_, _ = bad.Write([]byte("0,K_\n"))
	if _, err := bad.timeline(); err == nil {
		t.Fatal("accepted unsafe keyframe gap")
	}
}
