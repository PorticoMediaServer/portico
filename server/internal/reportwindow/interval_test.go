package reportwindow

import (
	"testing"
	"time"
)

func TestViewerCalendarIntervalsDSTAndQuarterOffsets(t *testing.T) {
	for _, tc := range []struct {
		zone, until, day string
		hours            time.Duration
	}{
		{"America/New_York", "2026-03-10T12:00:00Z", "2026-03-08", 23 * time.Hour},
		{"America/New_York", "2026-11-03T12:00:00Z", "2026-11-01", 25 * time.Hour},
		{"Australia/Lord_Howe", "2026-10-06T00:00:00Z", "2026-10-04", 23*time.Hour + 30*time.Minute},
		{"Asia/Kathmandu", "2027-01-01T01:00:00Z", "2026-12-31", 24 * time.Hour},
		{"Pacific/Chatham", "2027-01-01T01:00:00Z", "2026-12-31", 24 * time.Hour},
	} {
		t.Run(tc.zone+tc.until, func(t *testing.T) {
			for _, p := range []struct {
				period string
				count  int
			}{{"today", 1}, {"7d", 7}, {"30d", 30}, {"90d", 90}, {"1y", 52}, {"12h", 12}, {"24h", 24}} {
				r, e := Resolve(p.period, tc.zone, tc.until, -1)
				if e != nil {
					t.Fatal(e)
				}
				if len(r.Buckets) != p.count || r.TimeZone != tc.zone {
					t.Fatal(r)
				}
				previous := r.FromMS()
				seen := map[string]bool{}
				for i, b := range r.Buckets {
					a, _ := time.Parse(time.RFC3339Nano, b.Start)
					z, _ := time.Parse(time.RFC3339Nano, b.End)
					if a.UnixMilli() != previous || z.Before(a) || seen[b.Start] {
						t.Fatal("noncontiguous identities", b)
					}
					if a.UnixMilli()%Grain.Milliseconds() != 0 || z.UnixMilli()%Grain.Milliseconds() != 0 {
						t.Fatal("non-quarter boundary", b)
					}
					if i == len(r.Buckets)-1 && !b.Partial {
						t.Fatal("current bucket not partial", b)
					}
					seen[b.Start] = true
					previous = z.UnixMilli()
				}
				if previous != r.UntilMS() {
					t.Fatal("incomplete interval")
				}
			}
			r, _ := Resolve("7d", tc.zone, tc.until, -1)
			found := false
			for _, b := range r.Buckets {
				a, _ := time.Parse(time.RFC3339Nano, b.Start)
				z, _ := time.Parse(time.RFC3339Nano, b.End)
				if a.In(r.zone).Format("2006-01-02") == tc.day {
					found = true
					if z.Sub(a) != tc.hours {
						t.Fatal("DST duration", b, z.Sub(a))
					}
				}
			}
			if !found {
				t.Fatal("day absent")
			}
		})
	}
}

func TestRepeatedHoursEmptyCurrentBucketAndToday(t *testing.T) {
	r, e := Resolve("24h", "America/New_York", "2026-11-01T07:00:00Z", -1)
	if e != nil {
		t.Fatal(e)
	}
	repeated := []string{}
	for _, b := range r.Buckets {
		at, _ := time.Parse(time.RFC3339Nano, b.Start)
		if at.In(r.zone).Hour() == 1 && at.In(r.zone).Day() == 1 {
			repeated = append(repeated, b.Start)
		}
	}
	if len(repeated) != 2 || repeated[0] == repeated[1] {
		t.Fatal("repeated hours collapsed", repeated)
	}
	last := r.Buckets[len(r.Buckets)-1]
	if last.Start != last.End || !last.Partial {
		t.Fatal("empty current hour", last)
	}
	today, e := Resolve("today", "Asia/Kathmandu", "2026-12-31T18:15:00Z", -1)
	if e != nil {
		t.Fatal(e)
	}
	if len(today.Buckets) != 1 || today.Buckets[0].Start != today.Buckets[0].End {
		t.Fatal("midnight/year boundary", today)
	}
	day, _ := Resolve("today", "Asia/Kathmandu", "2027-01-01T01:00:00Z", -1)
	hours, _ := Resolve("24h", "Asia/Kathmandu", "2027-01-01T01:00:00Z", -1)
	if day.From == hours.From || len(hours.Buckets) != 24 {
		t.Fatal("Today aliases24h")
	}
}

func TestReportContextRequiresExplicitZoneAndCompletedQuarter(t *testing.T) {
	for _, v := range []struct{ zone, until string }{{"", "2026-10-22T12:00:00Z"}, {"Local", "2026-10-22T12:00:00Z"}, {"Mars/Olympus", "2026-10-22T12:00:00Z"}, {"../etc/passwd", "2026-10-22T12:00:00Z"}, {"America/Halifax", ""}, {"Asia/Kathmandu", "2026-10-22T12:14:59Z"}, {"UTC", "2026-10-22T12:00:00.000000001Z"}} {
		if _, e := Resolve("7d", v.zone, v.until, -1); e == nil {
			t.Fatal("invalid context accepted", v)
		}
	}
	if _, e := Resolve("365d", "UTC", "2026-10-22T12:00:00Z", -1); e == nil {
		t.Fatal("retired period accepted")
	}
	r, e := Resolve("all", "Asia/Kathmandu", "2026-10-22T12:00:00Z", time.Date(2025, 1, 3, 4, 5, 0, 0, time.UTC).UnixMilli())
	if e != nil || r.From != "2024-12-31T18:15:00Z" {
		t.Fatal(r, e)
	}
}
