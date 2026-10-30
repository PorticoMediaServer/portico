package librarychannels

import (
	"testing"
	"time"
)

func TestBoundarySkippedAndRepeatedTimes(t *testing.T) {
	cases := []struct {
		date, zone, utc, adjustment string
		minute                      int
	}{{"2026-03-08", "America/Halifax", "2026-03-08T06:00:00Z", "gap-forward", 150}, {"2026-11-01", "America/Halifax", "2026-11-01T04:30:00Z", "repeated-first", 90}, {"2026-09-05", "America/Halifax", "2026-09-05T15:00:00Z", "none", 720}, {"2026-10-04", "Australia/Lord_Howe", "2026-10-03T15:30:00Z", "gap-forward", 135}, {"2011-12-30", "Pacific/Apia", "2011-12-30T10:00:00Z", "gap-forward", 720}}
	for _, c := range cases {
		t.Run(c.date+c.zone, func(t *testing.T) {
			got, e := ResolveBoundary(c.date, c.minute, c.zone)
			if e != nil || got.UTC.Format(time.RFC3339) != c.utc || got.Adjustment != c.adjustment {
				t.Fatalf("got %#v %v, want %s %s", got, e, c.utc, c.adjustment)
			}
		})
	}
}
func TestOvernightAndSevenCalendarDayDuration(t *testing.T) {
	block, e := ResolveBlock("2026-03-07", 23*60, 3*60, "America/Halifax")
	if e != nil || block.End.UTC.Sub(block.Start.UTC) != 3*time.Hour {
		t.Fatalf("overnight %#v %v", block, e)
	}
	spring, e := SevenDayWindow("2026-03-05", "America/Halifax")
	if e != nil || spring.End.UTC.Sub(spring.Start.UTC) != 167*time.Hour {
		t.Fatalf("spring %#v %v", spring, e)
	}
	fall, e := SevenDayWindow("2026-10-29", "America/Halifax")
	if e != nil || fall.End.UTC.Sub(fall.Start.UTC) != 169*time.Hour {
		t.Fatalf("fall %#v %v", fall, e)
	}
}
func TestInvalidTimeInputsFailClosed(t *testing.T) {
	for _, c := range []struct {
		date, zone string
		minute     int
	}{{"2026-02-30", "UTC", 0}, {"2026-03-08", "Local", 0}, {"2026-03-08", "Missing/Zone", 0}, {"2026-03-08", "UTC", 1440}, {"2026-03-08", "UTC", -1}} {
		if _, e := ResolveBoundary(c.date, c.minute, c.zone); e != ErrLocalTime {
			t.Fatal("invalid boundary accepted", c, e)
		}
	}
}
