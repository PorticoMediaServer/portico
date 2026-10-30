package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A Saved page reads its shape and filter counts in one pass over the saved
// set, on a continuation as on a first page, and the counts are the ones the
// filters themselves select (B9).
func TestSavedCountsAreOnePass(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	if _, e := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at) VALUES
		('p',?,1,1,NULL,1,0,'2024-01-01'),('p',?,1,0,NULL,1,0,'2024-03-01'),('p',?,1,0,NULL,1,0,'2024-02-01')`,
		names["a"].ID, names["b"].ID, names["c"].ID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,45000,0,0,'playback')`, names["b"].ID); e != nil {
		t.Fatal(e)
	}
	libraries := []string{"m"}
	for _, shape := range []string{"", "posters", "squares"} {
		counts := map[string]int{}
		for _, filter := range []string{"all", "unwatched", "inProgress", "watched", "favorites"} {
			page := savedQueryEchoPage(t, s, libraries, filter, shape, 100, "")
			selected := 0
			for _, section := range page.Sections {
				selected += len(section.Entries)
			}
			for _, option := range page.Filters[0].Options {
				counts[option.ID] = option.Count
			}
			if counts[filter] != selected {
				t.Fatalf("shape %q filter %q counts %d but selects %d", shape, filter, counts[filter], selected)
			}
		}
	}
	first := savedQueryEchoPage(t, s, libraries, "all", "", 1, "")
	if len(first.Sections) == 0 || first.Sections[0].NextCursor == "" {
		t.Fatalf("no continuation: %+v", first)
	}
	ctx, statements := dbwork.TraceStatements(context.Background())
	if err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
		_, err := s.WithContext(ctx).Saved(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "s", Profile: "p", ViewerFence: "f",
			View: "watchlist", Limit: 1, Filter: "all", Cursor: first.Sections[0].NextCursor}, libraries)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	passes := 0
	for _, statement := range statements() {
		if strings.HasPrefix(statement, "SELECT COALESCE(sum(i.kind") || strings.HasPrefix(statement, "SELECT count(*) FROM personal_items") {
			passes++
		}
	}
	if passes != 1 {
		t.Fatalf("a continuation counted the saved set %d times", passes)
	}
}
