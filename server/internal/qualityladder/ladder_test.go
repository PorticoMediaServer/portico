package qualityladder

import (
	"reflect"
	"testing"
)

func TestCanonicalQualityOrderBitratesAndExactData(t *testing.T) {
	if !reflect.DeepEqual(IDs(), []string{"original", "automatic", "2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p"}) {
		t.Fatal(IDs())
	}
	want := []int{40000000, 20000000, 8000000, 4000000, 2000000, 1500000, 500000}
	data := []float64{18, 9, 3.6, 1.8, .9, .675, .225}
	for i, p := range Fixed() {
		if p.VideoBitrateBPS != want[i] || p.DataGBPerHour != data[i] {
			t.Fatalf("step %d %+v", i, p)
		}
	}
	for _, gone := range []string{"1440p", "1080p", "720p", "360p", "auto"} {
		if _, ok := Find(gone); ok {
			t.Fatalf("old alias %s", gone)
		}
	}
	r, _ := Find("1080p-high")
	if !r.SameAsSource(1080, 8_000_000) || r.SameAsSource(1080, 25_000_000) || r.SameAsSource(2160, 8_000_000) || r.SameAsSource(0, 8_000_000) || r.SameAsSource(1080, 0) {
		t.Fatal("source equivalence guessed or ignored a dimension")
	}
}

func TestAwayLimitNeverInventsNearestResolution(t *testing.T) {
	if p, ok := AtBitrate(2_000_000); !ok || p.ID != "720p-low" || p.Height != 720 {
		t.Fatal(p, ok)
	}
	for _, bits := range []int{0, 3_000_000, 999_999} {
		if _, ok := AtBitrate(bits); ok {
			t.Fatal("invented nearest step", bits)
		}
	}
}
