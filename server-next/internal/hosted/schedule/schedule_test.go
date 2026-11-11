package schedule

import (
	"testing"
	"time"
)

func TestNextCheckInIsTheServersOwnSecond(t *testing.T) {
	id := "srv_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	first := NextCheckIn(id, now)
	if !first.After(now) || first.Sub(now) > 24*time.Hour || first.Hour()*3600+first.Minute()*60+first.Second() != Slot(id) {
		t.Fatalf("next check-in %v is not the slot %d", first, Slot(id))
	}
	if second := NextCheckIn(id, first); second.Sub(first) != 24*time.Hour {
		t.Fatalf("the check-in after that is %v later", second.Sub(first))
	}
}

func TestAServerThatWasOffWaitsForItsSlotUnlessADayHasPassed(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	if _, missed := StartDelay(now.Add(-23*time.Hour), now, 0.5); missed {
		t.Fatal("a server whose last contact was 23 hours ago called at start")
	}
	wait, missed := StartDelay(now.Add(-25*time.Hour), now, 0.5)
	if !missed || wait != 30*time.Minute {
		t.Fatalf("a server off for 25 hours: missed=%v wait=%v", missed, wait)
	}
}

func TestRetriesGrowToACeilingAndNeverReturnTogether(t *testing.T) {
	p := CheckInRetry
	for attempt := 0; attempt < 40; attempt++ {
		low, high := p.Wait(attempt, 0), p.Wait(attempt, 0.999999)
		if low < p.First/2 || high > p.Ceiling || high < low+low/2 {
			t.Fatalf("attempt %d waits between %v and %v", attempt, low, high)
		}
	}
	if After(time.Second) != MinimumRetryAfter || After(time.Hour) != time.Hour {
		t.Fatal("Retry-After is not obeyed with its floor")
	}
}
