package console

import (
	"testing"
	"time"
)

func TestLoadKeepsHardwareAndProcessorCountsLiveAndInHourlyAverages(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	hardware, processor := 2, 1
	load := &Load{Now: func() time.Time { return now }, ConversionKinds: func() (int, int) { return hardware, processor }}
	load.Sample()
	now = now.Add(loadEvery)
	hardware, processor = 0, 3
	load.Sample()
	live := load.Live(now)
	for name, want := range map[string]float64{"hardwareConversions": 0, "processorConversions": 3, "conversions": 3} {
		points := live[name]
		if len(points) != 2 || points[1].Value != want {
			t.Fatal(name, points, want)
		}
	}
	_, day := load.Day(now)
	for name, want := range map[string]float64{"hardwareConversions": 1, "processorConversions": 2, "conversions": 3} {
		points := day[name]
		if len(points) != 1 || points[0].Value != want {
			t.Fatal(name, points, want)
		}
	}
	now = now.Add(time.Hour)
	hardware, processor = 0, 0
	load.Sample()
	_, day = load.Day(now)
	if len(day["hardwareConversions"]) != 2 || day["hardwareConversions"][1].Value != 0 || day["processorConversions"][1].Value != 0 {
		t.Fatal("an idle hour retained old counts", day)
	}
}
