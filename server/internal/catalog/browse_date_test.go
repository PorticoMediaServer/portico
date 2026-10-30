package catalog

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A date operand names a whole UTC day, and an RFC 3339 operand the second it
// falls in, whatever its offset: stored dates are UTC text with any fraction.
func TestBrowseDateFiltersCompareDaysAndInstants(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	c.Fields(names["m1"].ID, map[string]any{"added_text": "2026-09-01T14:23:11.123Z"})
	c.Fields(names["m2"].ID, map[string]any{"added_text": "2026-09-02T23:59:59.999Z"})
	c.Fields(names["m3"].ID, map[string]any{"added_text": "2026-09-03T00:00:00Z"})
	c.Attributes(names["m2"].ID, "releaseDate", "2005-06-01")
	c.Exec(`UPDATE personal_items SET last_played_at='2026-09-10T18:30:00Z' WHERE profile_id='p' AND item_id=?`, names["m2"].ID)
	c.Drain()
	cases := []struct {
		name, query string
		want        []string
	}{
		{"added on a day", `{"variant":"predicate","field":"dateAdded","operator":"equals","value":"2026-09-01"}`, []string{"m1"}},
		{"added on another day", `{"variant":"predicate","field":"dateAdded","operator":"not-equals","value":"2026-09-01"}`, []string{"m2", "m3", "m4", "m5"}},
		{"added before a day", `{"variant":"predicate","field":"dateAdded","operator":"less-than","value":"2026-09-02"}`, []string{"m1"}},
		{"added by the end of a day", `{"variant":"predicate","field":"dateAdded","operator":"at-most","value":"2026-09-02"}`, []string{"m1", "m2"}},
		{"added after a day", `{"variant":"predicate","field":"dateAdded","operator":"greater-than","value":"2026-09-02"}`, []string{"m3", "m4", "m5"}},
		{"added from a day", `{"variant":"predicate","field":"dateAdded","operator":"at-least","value":"2026-09-02"}`, []string{"m2", "m3", "m4", "m5"}},
		{"added between days", `{"variant":"predicate","field":"dateAdded","operator":"between","value":["2026-09-01","2026-09-02"]}`, []string{"m1", "m2"}},
		{"added at an instant", `{"variant":"predicate","field":"dateAdded","operator":"equals","value":"2026-09-01T14:23:11Z"}`, []string{"m1"}},
		{"added after an instant", `{"variant":"predicate","field":"dateAdded","operator":"greater-than","value":"2026-09-01T14:23:11Z"}`, []string{"m2", "m3", "m4", "m5"}},
		{"added from an instant in another zone", `{"variant":"predicate","field":"dateAdded","operator":"at-least","value":"2026-09-01T16:23:11+02:00"}`, []string{"m1", "m2", "m3", "m4", "m5"}},
		{"added up to an instant in another zone", `{"variant":"predicate","field":"dateAdded","operator":"at-most","value":"2026-09-03T02:00:00+02:00"}`, []string{"m1", "m2", "m3"}},
		{"added between instants", `{"variant":"predicate","field":"dateAdded","operator":"between","value":["2026-09-02T23:59:59Z","2026-09-03T00:00:00Z"]}`, []string{"m2", "m3"}},
		{"played on a day", `{"variant":"predicate","field":"lastPlayedAt","operator":"equals","value":"2026-09-10"}`, []string{"m2"}},
		{"played by the end of a day", `{"variant":"predicate","field":"lastPlayedAt","operator":"at-most","value":"2026-09-10"}`, []string{"m2"}},
		{"not played on a day", `{"variant":"predicate","field":"lastPlayedAt","operator":"not-equals","value":"2026-09-10"}`, []string{"m1", "m3", "m4", "m5"}},
		{"released on a day", `{"variant":"predicate","field":"releaseDate","operator":"equals","value":"2005-06-01"}`, []string{"m2"}},
		{"released between days", `{"variant":"predicate","field":"releaseDate","operator":"between","value":["2005-01-01","2005-06-01"]}`, []string{"m2"}},
		{"released after a day", `{"variant":"predicate","field":"releaseDate","operator":"greater-than","value":"2005-06-01"}`, []string{}},
		{"released from midnight of its day", `{"variant":"predicate","field":"releaseDate","operator":"at-least","value":"2005-06-01T00:00:00Z"}`, []string{"m2"}},
		{"released before midnight of its day", `{"variant":"predicate","field":"releaseDate","operator":"less-than","value":"2005-06-01T00:00:00Z"}`, []string{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := browseIDs(t, s, names, test.query)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("want %v got %v", test.want, got)
			}
		})
	}
}

// A timestamp operand is compared at the precision it was written with, a
// fraction included, against stored timestamps whose fractions have any
// length: neither side's digits are dropped.
func TestBrowseDateFiltersKeepFractionsOfASecond(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	for name, added := range map[string]string{
		"m1": "2026-09-01T14:23:11Z",
		"m2": "2026-09-01T14:23:11.123Z",
		"m3": "2026-09-01T14:23:11.5Z",
		"m4": "2026-09-01T14:23:11.500999Z",
		"m5": "2026-09-01T14:23:11.999Z",
	} {
		c.Fields(names[name].ID, map[string]any{"added_text": added})
	}
	c.Drain()
	cases := []struct {
		name, query string
		want        []string
	}{
		{"before .500", `{"variant":"predicate","field":"dateAdded","operator":"less-than","value":"2026-09-01T14:23:11.500Z"}`, []string{"m1", "m2"}},
		{"up to .500", `{"variant":"predicate","field":"dateAdded","operator":"at-most","value":"2026-09-01T14:23:11.500Z"}`, []string{"m1", "m2", "m3", "m4"}},
		{"after .500", `{"variant":"predicate","field":"dateAdded","operator":"greater-than","value":"2026-09-01T14:23:11.500Z"}`, []string{"m5"}},
		{"from .500", `{"variant":"predicate","field":"dateAdded","operator":"at-least","value":"2026-09-01T14:23:11.500Z"}`, []string{"m3", "m4", "m5"}},
		{"at .500", `{"variant":"predicate","field":"dateAdded","operator":"equals","value":"2026-09-01T14:23:11.500Z"}`, []string{"m3", "m4"}},
		{"not at .500", `{"variant":"predicate","field":"dateAdded","operator":"not-equals","value":"2026-09-01T14:23:11.500Z"}`, []string{"m1", "m2", "m5"}},
		{"at .5", `{"variant":"predicate","field":"dateAdded","operator":"equals","value":"2026-09-01T14:23:11.5Z"}`, []string{"m3", "m4"}},
		{"at .123", `{"variant":"predicate","field":"dateAdded","operator":"equals","value":"2026-09-01T14:23:11.123Z"}`, []string{"m2"}},
		{"before .123", `{"variant":"predicate","field":"dateAdded","operator":"less-than","value":"2026-09-01T14:23:11.123Z"}`, []string{"m1"}},
		{"after .123", `{"variant":"predicate","field":"dateAdded","operator":"greater-than","value":"2026-09-01T14:23:11.123Z"}`, []string{"m3", "m4", "m5"}},
		{"after .999", `{"variant":"predicate","field":"dateAdded","operator":"greater-than","value":"2026-09-01T14:23:11.999Z"}`, []string{}},
		{"from .999", `{"variant":"predicate","field":"dateAdded","operator":"at-least","value":"2026-09-01T14:23:11.999Z"}`, []string{"m5"}},
		{"before a microsecond past the second", `{"variant":"predicate","field":"dateAdded","operator":"less-than","value":"2026-09-01T14:23:11.000001Z"}`, []string{"m1"}},
		{"from .500 in another zone", `{"variant":"predicate","field":"dateAdded","operator":"at-least","value":"2026-09-01T16:23:11.500+02:00"}`, []string{"m3", "m4", "m5"}},
		{"between .123 and .500", `{"variant":"predicate","field":"dateAdded","operator":"between","value":["2026-09-01T14:23:11.123Z","2026-09-01T14:23:11.500Z"]}`, []string{"m2", "m3", "m4"}},
		{"in the second", `{"variant":"predicate","field":"dateAdded","operator":"equals","value":"2026-09-01T14:23:11Z"}`, []string{"m1", "m2", "m3", "m4", "m5"}},
		{"after the second", `{"variant":"predicate","field":"dateAdded","operator":"greater-than","value":"2026-09-01T14:23:11Z"}`, []string{}},
		{"before .5 of the last second of a minute", `{"variant":"predicate","field":"dateAdded","operator":"less-than","value":"2026-09-01T14:23:59.5Z"}`, []string{"m1", "m2", "m3", "m4", "m5"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := browseIDs(t, s, names, test.query)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("want %v got %v", test.want, got)
			}
		})
	}
}
