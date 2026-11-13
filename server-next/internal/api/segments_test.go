package api

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"testing"
)

func TestOwnerReviewsSegmentsAndKeepsEditsThroughRedetection(t *testing.T) {
	sh := newShelf(t, "Film.2020.mkv")
	ctx := context.Background()
	page := get[SegmentTitlePage](t, sh.s, sh.owner, "/v1/admin/segments/titles?libraryId="+sh.library)
	if len(page.Titles) != 1 {
		t.Fatal(page)
	}
	title := page.Titles[0]
	review := get[SegmentTitleReview](t, sh.s, sh.owner, "/v1/admin/segments/titles/"+title.ID)
	if len(review.Seasons) != 1 || len(review.Seasons[0].Entries) != 1 {
		t.Fatal(review)
	}
	entry := review.Seasons[0].Entries[0]
	if entry.SourceID == "" || entry.SourceRevision == "" || entry.DurationUS == "0" {
		t.Fatal(entry)
	}
	req := SegmentMarkerEdit{SourceID: entry.SourceID, SourceRevision: entry.SourceRevision, ExpectedRevision: entry.MarkerRevision,
		RequestID: "one-intro", Action: "create", Kind: "intro", StartUS: "1000000", EndUS: "2000000"}
	path := "/v1/items/" + entry.ItemID + "/analysis/markers"
	for i := 0; i < 2; i++ {
		if w := call(t, sh.s, "POST", path, sh.owner, req, nil); w.Code != 204 {
			t.Fatal(w.Code, w.Body)
		}
	}
	review = get[SegmentTitleReview](t, sh.s, sh.owner, "/v1/admin/segments/titles/"+title.ID)
	entry = review.Seasons[0].Entries[0]
	if len(entry.Markers) != 1 || !entry.Markers[0].Approved || !entry.Markers[0].AutomaticSafe {
		t.Fatal(entry)
	}
	req.RequestID, req.Action, req.MarkerID = "dismiss-intro", "dismiss", entry.Markers[0].ID
	req.ExpectedRevision = entry.MarkerRevision
	if w := call(t, sh.s, "POST", path, sh.owner, req, nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	entries, err := sh.s.db.SegmentEntries(ctx, sh.library, title.ID, 0, 1)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	found := []db.SegmentMarker{{Kind: db.MarkerIntro, Source: db.MarkerConfirmed, StartMS: 5000, EndMS: 6000, Confidence: .99}}
	if err := sh.s.db.SaveSegmentAnalysis(ctx, entries[0], "standard", "complete", "", []byte(`{}`), found); err != nil {
		t.Fatal(err)
	}
	review = get[SegmentTitleReview](t, sh.s, sh.owner, "/v1/admin/segments/titles/"+title.ID)
	entry = review.Seasons[0].Entries[0]
	if len(entry.Markers) != 1 || entry.Markers[0].ViewerVisible || entry.Markers[0].AutomaticSafe {
		t.Fatal("detection lost the dismissal", entry)
	}
	req.RequestID = "stale-edit"
	if w := call(t, sh.s, "POST", path, sh.owner, req, nil); w.Code != 412 {
		t.Fatal("stale review applied", w.Code, w.Body)
	}
	for _, token := range []string{sh.member, sh.limitedAdmin, ""} {
		for _, route := range []string{"/v1/admin/segments", "/v1/admin/segments/titles?libraryId=" + sh.library, "/v1/admin/segments/titles/" + title.ID} {
			w := call(t, sh.s, "GET", route, token, nil, nil)
			if w.Code != 403 && w.Code != 401 {
				t.Fatal(route, w.Code, w.Body)
			}
		}
		if w := call(t, sh.s, "POST", path, token, req, nil); w.Code != 403 && w.Code != 401 {
			t.Fatal(w.Code, w.Body)
		}
	}
}

func TestSegmentReviewPagesTitlesBeforeLoadingTheirMarkers(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	status := get[SegmentDetectionStatus](t, s, owner, "/v1/admin/segments")
	if len(status.Libraries) != 2 || status.Mode != "standard" || status.Files.Pending == 0 {
		t.Fatal(status)
	}
	seen := map[string]bool{}
	for _, lib := range status.Libraries {
		cursor := ""
		for {
			page := get[SegmentTitlePage](t, s, owner, "/v1/admin/segments/titles?libraryId="+lib.LibraryID+"&cursor="+cursor)
			if len(page.Titles) > 50 {
				t.Fatal("unbounded page", len(page.Titles))
			}
			for _, title := range page.Titles {
				if seen[title.ID] {
					t.Fatal("repeated title", title.ID)
				}
				seen[title.ID] = true
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	if len(seen) < 51 {
		t.Fatal("the fixture did not exercise a second page", fmt.Sprint(len(seen)))
	}
}
