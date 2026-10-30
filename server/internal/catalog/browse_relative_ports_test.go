package catalog

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestBrowseWithinLastDaysWindowBoundaries(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	added := map[string]string{
		"m1": "2026-10-16T12:00:00.000Z", // exactly now - 7 days: included
		"m2": "2026-10-16T11:59:59.999Z", // one millisecond earlier: excluded
		"m3": "2026-10-23T12:00:00.000Z", // exactly now: included
		"m4": "2026-10-23T12:00:00.001Z", // one millisecond in the future: excluded
		"m5": "2026-10-01T00:00:00.000Z", // outside seven days
	}
	for name, value := range added {
		c.Fields(names[name].ID, map[string]any{"added_text": value})
	}
	c.Drain()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }

	got := browseIDs(t, s, names, `{"variant":"relative","field":"dateAdded","operator":"within-last-days","value":7}`)
	if strings.Join(got, ",") != "m1,m3" {
		t.Fatalf("seven-day window: want m1,m3 got %v", got)
	}
	// The same titles at a wider window: the 1 October title is within 30 days.
	got = browseIDs(t, s, names, `{"variant":"relative","field":"dateAdded","operator":"within-last-days","value":30}`)
	if strings.Join(got, ",") != "m1,m2,m3,m5" {
		t.Fatalf("thirty-day window: want m1,m2,m3,m5 got %v", got)
	}
}

func TestBrowseWithinLastDaysEveryDateField(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	c.Fields(names["m1"].ID, map[string]any{"added_text": "2026-10-20T00:00:00.000Z"})
	c.Attributes(names["m2"].ID, "releaseDate", "2026-10-20")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,revision,last_played_at) VALUES('p',?,1,'2026-10-20T00:00:00Z')
 ON CONFLICT(profile_id,item_id) DO UPDATE SET last_played_at=excluded.last_played_at`, names["m3"].ID)
	c.Drain()
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	for _, test := range []struct {
		field, want string
	}{
		{"dateAdded", "m1"},
		{"releaseDate", "m2"},
		{"lastPlayedAt", "m3"},
	} {
		t.Run(test.field, func(t *testing.T) {
			got := browseIDs(t, s, names, fmt.Sprintf(`{"variant":"relative","field":%q,"operator":"within-last-days","value":7}`, test.field))
			if strings.Join(got, ",") != test.want {
				t.Fatalf("want %s got %v", test.want, got)
			}
		})
	}
}

func TestRelativeStandaloneCompilationRequiresCallerInstant(t *testing.T) {
	for name, compile := range map[string]func(*BrowseNode, ...time.Time) (string, []any, error){
		"shared":  CompileBrowseSelection,
		"compact": CompileCompactBrowseSelection,
	} {
		t.Run(name, func(t *testing.T) {
			node := relativeNode("dateAdded", 7)
			if _, _, err := compile(node); err == nil {
				t.Fatal("relative compiler invented a wall clock")
			}
			if _, _, err := compile(node, time.Time{}); err == nil {
				t.Fatal("relative compiler accepted unknown instant")
			}
			if _, _, err := compile(node, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			absolute := &BrowseNode{Field: "dateAdded", Operator: "at-least", Value: "2026-10-16"}
			if _, _, err := compile(absolute); err != nil {
				t.Fatal("absolute date acquired a clock dependency", err)
			}
		})
	}
}
