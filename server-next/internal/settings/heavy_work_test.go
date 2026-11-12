package settings

import (
	"testing"
	"time"
)

// Heavy work runs at any time by default; Overnight only is 1 to 6 am on the server's clock;
// chosen hours start on the chosen days and a stretch that ends the next morning carries on
// past midnight. Outside its hours it says when it may next start.
func TestHeavyWorkHours(t *testing.T) {
	loc, _ := time.LoadLocation("America/Halifax")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, loc) } // 12 Nov 2026 is a Thursday
	cases := []struct {
		name  string
		h     HeavyWorkSettings
		now   time.Time
		open  bool
		opens time.Time
	}{
		{"any time", HeavyWorkSettings{Hours: HeavyAnyTime}, at(12, 14, 0), true, at(12, 14, 0)},
		{"overnight, inside", HeavyWorkSettings{Hours: HeavyOvernight}, at(12, 3, 30), true, at(12, 3, 30)},
		{"overnight, after", HeavyWorkSettings{Hours: HeavyOvernight}, at(12, 6, 0), false, at(13, 1, 0)},
		{"overnight, before", HeavyWorkSettings{Hours: HeavyOvernight}, at(12, 0, 59), false, at(12, 1, 0)},
		{"chosen, past midnight", HeavyWorkSettings{Hours: HeavyChosen, Days: []string{"wed"}, From: "23:00", To: "05:00"}, at(12, 2, 0), true, at(12, 2, 0)},
		{"chosen, next week", HeavyWorkSettings{Hours: HeavyChosen, Days: []string{"wed"}, From: "23:00", To: "05:00"}, at(12, 5, 0), false, at(18, 23, 0)},
		{"chosen, later today", HeavyWorkSettings{Hours: HeavyChosen, Days: []string{"thu", "sat"}, From: "09:00", To: "17:00"}, at(12, 8, 0), false, at(12, 9, 0)},
	}
	for _, c := range cases {
		if open, opens := c.h.Open(c.now); open != c.open || !opens.Equal(c.opens) {
			t.Errorf("%s: open %v until %v, want %v and %v", c.name, open, opens, c.open, c.opens)
		}
	}
}

// A chosen stretch needs days, and a start and an end that differ; the other choices drop them.
func TestHeavyWorkHoursAreChecked(t *testing.T) {
	for _, bad := range []HeavyWorkSettings{{Hours: "sometimes"}, {Hours: HeavyChosen, From: "01:00", To: "02:00"},
		{Hours: HeavyChosen, Days: []string{"mon", "mon"}, From: "01:00", To: "02:00"}, {Hours: HeavyChosen, Days: []string{"mon"}, From: "1am", To: "02:00"},
		{Hours: HeavyChosen, Days: []string{"mon"}, From: "02:00", To: "02:00"}} {
		if _, err := General.Check(GeneralSettings{Name: "Portico", UpdateCheck: UpdatesAutomatic, HeavyWork: bad}); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	checked, err := General.Check(GeneralSettings{Name: "Portico", UpdateCheck: UpdatesAutomatic,
		HeavyWork: HeavyWorkSettings{Hours: HeavyOvernight, Days: []string{"mon"}, From: "01:00"}})
	got := checked.HeavyWork
	if err != nil || got.Days != nil || got.From != "" {
		t.Fatalf("overnight kept chosen hours: %+v %v", got, err)
	}
}

// How much of the week the hours are open, for how long heavy work takes.
func TestHeavyWorkOpenShare(t *testing.T) {
	for _, c := range []struct {
		h    HeavyWorkSettings
		want float64
	}{
		{HeavyWorkSettings{Hours: HeavyAnyTime}, 1},
		{HeavyWorkSettings{Hours: HeavyOvernight}, 5.0 / 24},
		{HeavyWorkSettings{Hours: HeavyChosen, Days: []string{"sat", "sun"}, From: "22:00", To: "06:00"}, 16.0 / 168},
	} {
		if got := c.h.OpenShare(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.h, got, c.want)
		}
	}
}
