package reading

import "testing"

func TestChooseReadingVersionPrefersOpenableProgressThenPrimary(t *testing.T) {
	a, b, c := Version{SourceID: "a"}, Version{SourceID: "b", InventoryAvailable: true}, Version{SourceID: "c", InventoryAvailable: true}
	for _, tc := range []struct {
		versions   []Version
		progressed string
		want       string
	}{
		{[]Version{a, b, c}, "", "b"},
		{[]Version{a, b, c}, "c", "c"},
		{[]Version{a, b, c}, "a", "b"}, // saved on a version that cannot open now
		{[]Version{a, b, c}, "gone", "b"},
		{[]Version{a}, "", "a"},
		{nil, "a", ""},
	} {
		if got := chooseReadingVersion(tc.versions, tc.progressed); got != tc.want {
			t.Fatalf("%v %q: got %q want %q", tc.versions, tc.progressed, got, tc.want)
		}
	}
}
