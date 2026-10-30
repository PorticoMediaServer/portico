package catalog

import (
	"errors"
	"testing"
)

// savedQueryEchoPage asks the genuine Saved producer for one shape/filter page
// and returns its echoed query, cursor scope behaviour and section counts.
func savedQueryEchoPage(t *testing.T, s *Service, libraries []string, filter, shape string, limit int, cursor string) ContentEnvelope {
	t.Helper()
	r := ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "s", Profile: "p", ViewerFence: "f",
		View: "watchlist", Limit: limit, Filter: filter, Shape: shape, Cursor: cursor}
	out, e := s.Saved(r, libraries)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

// The published query category is the selected filter alone: the saved parser
// compares it with the requested filter and refuses anything else. The cursor
// scope stays filter:shape, so a page minted for one shape cannot be continued
// in another and a filter change cannot reuse it.
func TestSavedQueryEchoCarriesTheFilterAlone(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	if _, e := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at) VALUES
		('p',?,1,0,NULL,1,1,'2024-01-01'),('p',?,1,0,NULL,1,0,'2024-03-01'),('p',?,1,0,NULL,1,0,'2024-02-01')`,
		names["a"].ID, names["b"].ID, names["c"].ID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,45000,0,0,'playback')`, names["b"].ID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE personal_items SET favorite=1 WHERE profile_id='p' AND item_id=?`, names["c"].ID); e != nil {
		t.Fatal(e)
	}
	// Watched is an accepted explicit intent, not the legacy personal_items
	// cache column seeded above. Use the genuine writer to establish it.
	watched := true
	if _, e := s.SetPersonal("a", "p", names["a"].Public, PersonalMutation{OperationID: "saved-watched", ExpectedRevision: 1, Watched: &watched}, nil); e != nil {
		t.Fatal(e)
	}
	libraries := []string{"m"}
	for _, shape := range []string{"", "posters", "squares"} {
		for _, filter := range []string{"all", "unwatched", "inProgress", "watched", "favorites"} {
			page := savedQueryEchoPage(t, s, libraries, filter, shape, 40, "")
			if page.Query.Category != filter {
				t.Fatalf("shape %q filter %q echoed %q", shape, filter, page.Query.Category)
			}
			if page.Query.Sort != "title" || page.Query.Direction != "asc" {
				t.Fatalf("default order echo %q %q", page.Query.Sort, page.Query.Direction)
			}
		}
	}
	// A page minted for posters cannot be continued against another shape or filter.
	minted := ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "s", Profile: "p", ViewerFence: "f",
		View: "watchlist", Limit: 1, Filter: "all", Shape: "posters"}
	first, e := s.Saved(minted, libraries)
	if e != nil {
		t.Fatal(e)
	}
	if first.Sections[0].NextCursor == "" {
		t.Fatal("expected a continuation cursor")
	}
	otherShape := minted
	otherShape.Cursor, otherShape.Shape = first.Sections[0].NextCursor, "squares"
	if _, e := s.Saved(otherShape, libraries); !errors.Is(e, ErrCursor) {
		t.Fatalf("shape change reused the cursor: %v", e)
	}
	otherFilter := minted
	otherFilter.Cursor, otherFilter.Filter = first.Sections[0].NextCursor, "unwatched"
	if _, e := s.Saved(otherFilter, libraries); !errors.Is(e, ErrCursor) {
		t.Fatalf("filter change reused the cursor: %v", e)
	}
	otherFilter.Filter = "watched"
	if _, e := s.Saved(otherFilter, libraries); !errors.Is(e, ErrCursor) {
		t.Fatalf("watched change reused the cursor: %v", e)
	}
	// Watched and Favorites read the same profile's saved rows and the same
	// visibility/shape base: a is watched, c is favorited, b is neither.
	counts := map[string]int{}
	for _, option := range savedQueryEchoPage(t, s, libraries, "all", "posters", 40, "").Filters[0].Options {
		counts[option.ID] = option.Count
	}
	if counts["watched"] != 1 || counts["favorites"] != 1 || counts["unwatched"] != 2 || counts["inProgress"] != 1 {
		t.Fatalf("published filter counts %v", counts)
	}
	for filter, want := range map[string]string{"watched": names["a"].Public, "favorites": names["c"].Public} {
		page := savedQueryEchoPage(t, s, libraries, filter, "posters", 40, "")
		if page.Sections[0].TotalCount != 1 || len(page.Sections[0].Entries) != 1 || page.Sections[0].Entries[0].ID != want {
			t.Fatalf("%s filter %v", filter, page.Sections)
		}
	}
	same := minted
	same.Cursor = first.Sections[0].NextCursor
	if _, e := s.Saved(same, libraries); e != nil {
		t.Fatalf("same scope refused its own cursor: %v", e)
	}
}
