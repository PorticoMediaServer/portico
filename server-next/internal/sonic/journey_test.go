package sonic

import (
	"reflect"
	"testing"
)

func TestJourneyKeepsRealEndpointsUniqueAndMovesTowardTarget(t *testing.T) {
	tracks := []JourneyTrack{}
	for n := int64(0); n <= 8; n++ {
		point := clusterFixture(t, n+1, float32(8-n), float32(n), 1)
		tracks = append(tracks, JourneyTrack{point.ID, point.Vector})
	}
	var offAxis [Dimensions]float32
	offAxis[2] = 1
	tracks = append(tracks, JourneyTrack{100, offAxis})
	got, err := Journey(tracks, 1, 9, 5)
	if err != nil || len(got) != 5 || got[0] != 1 || got[len(got)-1] != 9 {
		t.Fatal(got, err)
	}
	for n := 1; n < len(got); n++ {
		if got[n] <= got[n-1] || got[n] == 100 {
			t.Fatal("journey repeated, left its direction or substituted an endpoint", got)
		}
	}
	for i, j := 0, len(tracks)-1; i < j; i, j = i+1, j-1 {
		tracks[i], tracks[j] = tracks[j], tracks[i]
	}
	reversed, err := Journey(tracks, 1, 9, 5)
	if err != nil || !reflect.DeepEqual(got, reversed) {
		t.Fatal("query order changed journey", got, reversed, err)
	}
}
func TestJourneyRejectsMissingOrCorruptEvidenceWithoutPadding(t *testing.T) {
	point := clusterFixture(t, 1, 1, 0, 1)
	a := JourneyTrack{1, point.Vector}
	b := JourneyTrack{2, point.Vector}
	got, err := Journey([]JourneyTrack{a, b}, 1, 2, 8)
	if err != nil || !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatal("identical directions fabricated a path", got, err)
	}
	for _, tracks := range [][]JourneyTrack{{a}, {a, a}, {a, b, {ID: 3}}} {
		if _, err := Journey(tracks, 1, 2, 3); err == nil {
			t.Fatal("invalid journey evidence accepted", tracks)
		}
	}
	if _, err := Journey([]JourneyTrack{a, b}, 1, 2, MaxJourneyLength+1); err == nil {
		t.Fatal("unbounded journey length")
	}
}
