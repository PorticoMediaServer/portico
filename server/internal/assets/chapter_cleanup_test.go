package assets

import (
	"math"
	"testing"
)

func TestCleanChaptersKeepsUsableEntries(t *testing.T) {
	got, status := CleanChapters([]Chapter{{"Good", 0, 10}, {"Bad", math.NaN(), 20}, {"Good 2", 10, 21}, {"End", 21, 35}}, 30)
	if status != "partial" || len(got) != 3 || got[2].End != 30 {
		t.Fatalf("%+v %s", got, status)
	}
}
