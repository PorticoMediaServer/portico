package catalog

import (
	"fmt"
	"testing"
)

func TestCompactBrowseSortAnchorsUseMaintainedBuckets(t *testing.T) {
	_, service, _ := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	cases := []struct {
		field, direction string
		want             []BrowsePositionAnchor
	}{
		{"year", "desc", []BrowsePositionAnchor{{"2015", 0}, {"2005", 2}, {"1995", 3}, {"1985", 4}}},
		{"added", "desc", []BrowsePositionAnchor{{"2026-09", 0}}},
		{"communityRating", "desc", []BrowsePositionAnchor{{"0", 0}}},
		{"duration", "desc", []BrowsePositionAnchor{{"0", 0}}},
		{"personalRating", "desc", []BrowsePositionAnchor{{"9", 0}, {"0", 1}}},
		{"lastPlayed", "desc", []BrowsePositionAnchor{{"2026-09", 0}, {"", 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			page, err := service.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: []BrowseSortSelection{{Field: tc.field, Direction: tc.direction}}, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(page.PositionIndex) != fmt.Sprint(tc.want) {
				t.Fatalf("%s anchors %v, want %v", tc.field, page.PositionIndex, tc.want)
			}
		})
	}
}
