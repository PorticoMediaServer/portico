package catalog

import "testing"

// BE-API-04: a limit above the published maximum is clamped to it, not reset
// to the default page.
func TestPageLimitClampsToThePublishedMaximum(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, BrowseDefaultLimit}, {-3, BrowseDefaultLimit}, {1, 1}, {100, 100}, {101, BrowseMaximumLimit}, {5000, BrowseMaximumLimit}} {
		if got := pageLimit(c.in); got != c.want {
			t.Fatalf("pageLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
