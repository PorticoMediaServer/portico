package catalog

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
)

func TestLibrarySummaryIsViewerRestrictedAndPathFree(t *testing.T) {
	db, s, names := browseFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE catalog_item_details SET poster_url='/art/public',backdrop_url='/art/wash' WHERE entity_id=?`, names["m1"].ID); err != nil {
		t.Fatal(err)
	}
	age := 13
	for _, restrictions := range []identity.ContentRestrictions{{}, {MaximumAge: &age, BlockUnrated: true}} {
		viewer := Viewer{Profile: "p", Fence: "summary", Libraries: []string{"a"}, Restrictions: restrictions}
		summaries, err := s.SummarizeLibraries(ctx, viewer, []Library{{ID: "a", Kind: "movie"}, {ID: "tv", Kind: "tv"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(summaries) != 1 {
			t.Fatalf("unallowed library: %+v", summaries)
		}
		summary := summaries["a"]
		page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		if len(summary.Counts) != 1 || summary.Counts[0].Count != page.PageInfo.Total || summary.Counts[0].AtLeast || len(summary.CoverURLs) > 3 {
			t.Fatalf("summary=%+v browse=%+v", summary, page.PageInfo)
		}
		raw, _ := json.Marshal(summary)
		for _, secret := range []string{"source", "path", "progress", "currentFile", "account"} {
			if strings.Contains(string(raw), `"`+secret+`"`) {
				t.Fatalf("private/progress field: %s", raw)
			}
		}
	}
	none, err := s.SummarizeLibraries(ctx, Viewer{}, []Library{{ID: "a", Kind: "movie"}})
	if err != nil || len(none) != 0 {
		t.Fatalf("unbound viewer summary %+v %v", none, err)
	}
}
func TestLibraryAudienceHonorsDisabledAndLibraryScope(t *testing.T) {
	db, _, _ := browseFixture(t)
	for _, id := range []string{"owner", "all", "limited", "disabled"} {
		if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, id, id, id+"-profile"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE direct_memberships SET allowed_libraries=CASE account_id WHEN 'limited' THEN '["tv"]' ELSE '[]' END,disabled=CASE account_id WHEN 'disabled' THEN 1 ELSE 0 END WHERE account_id IN('owner','all','limited','disabled')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	audience, err := libraryAudience(context.Background(), tx, "a")
	if err != nil || audience.Everyone || audience.Accounts != 2 {
		t.Fatalf("audience %+v %v", audience, err)
	}
	audience, err = libraryAudience(context.Background(), tx, "tv")
	if err != nil || !audience.Everyone || audience.Accounts != 3 {
		t.Fatalf("audience %+v %v", audience, err)
	}
}
