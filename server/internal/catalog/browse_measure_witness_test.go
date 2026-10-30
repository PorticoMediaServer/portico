package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A restricted viewer's cached count, facet panel and duration must follow a
// background rating classification that changes no browse row, order or
// viewer revision (B8).
func TestMeasureCachesFollowRatingClassification(t *testing.T) {
	_, s, _ := browseFixture(t)
	age := 12
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age}}
	action := &BrowseNode{Field: "genre", Operator: "contains", Value: "Action"}
	measure := func() (int, int, int64) {
		t.Helper()
		counts, err := s.BrowseCounts(viewer, "a", BrowseCountRequest{Pivot: "movies", Queries: []BrowseCountQuery{{ID: "all", Query: nil}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		genres, err := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "genre"})
		if err != nil {
			t.Fatal(err)
		}
		actionCount := 0
		for _, value := range genres.Values {
			if value.Label == "Action" {
				actionCount = value.Count
			}
		}
		duration, _, err := s.browseQueryDuration(viewer, FacetRequest{Library: "a", Pivot: "movies", Query: action})
		if err != nil {
			t.Fatal(err)
		}
		return counts.Counts[0].Total, actionCount, duration
	}
	// The R spelling is still pending, so the live restriction admits m2.
	total, actionCount, duration := measure()
	if total != 5 || actionCount != 2 || duration != 300000 {
		t.Fatalf("before classification: total=%d action=%d duration=%d", total, actionCount, duration)
	}
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	total, actionCount, duration = measure()
	if total != 4 || actionCount != 1 || duration != 100000 {
		t.Fatalf("after classification the cached measures are stale: total=%d action=%d duration=%d", total, actionCount, duration)
	}
}
