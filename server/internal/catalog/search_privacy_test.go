package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func searchPrivacyCatalog(t *testing.T) (*Service, SearchRequest) {
	t.Helper()
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	c.Movie(movies, "/movies/northwind.mkv", "Northwind Film", 2026)
	c.Drain()
	c.Exec(`INSERT INTO live_source_identities(id) VALUES('active'),('disabled')`)
	c.Exec(`INSERT INTO live_generations VALUES('current','active','Current',1,2,0,'now'),('old','active','Old',1,1,0,'now'),('disabled','disabled','Disabled',1,1,0,'now')`)
	c.Exec(`INSERT INTO live_sources VALUES('active','Active',1,'active',1,'current','now'),('disabled','Disabled',1,'disabled',1,'disabled','now')`)
	c.Exec(`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end) VALUES
	 ('current','news','news','Northwind News','101',NULL,'News',0,X'00','',''),
	 ('current','news24','news24','Northwind News 24','102',NULL,'News',1,X'00','',''),
	 ('old','old','old','Northwind Old','103',NULL,'News',0,X'00','',''),
	 ('disabled','disabled','disabled','Northwind Disabled','104',NULL,'News',0,X'00','','')`)
	return New(c.DB), SearchRequest{Viewer: Viewer{Profile: "kids", Fence: "policy", Libraries: []string{"movies"}}, Q: "Northwind", Limit: 1}
}

func assertSearchWithholdsLiveTV(t *testing.T, out SearchEnvelope) {
	t.Helper()
	for _, group := range out.Groups {
		if group.ID == "live-tv" || group.EntityKind == "channel" {
			t.Errorf("denied Live TV group exposes counts or results: %+v", group)
		}
	}
	for _, id := range out.Query.Groups {
		if id == "live-tv" {
			t.Error("denied Live TV group in query projection")
		}
	}
	for _, capability := range out.Capabilities.Groups {
		if capability.ID == "live-tv" {
			t.Errorf("denied Live TV capability: %+v", capability)
		}
	}
}

func TestSearchLiveTVPrivacyAllAndMixed(t *testing.T) {
	s, request := searchPrivacyCatalog(t)
	for _, groups := range [][]string{nil, {"movies", "live-tv"}, {"live-tv"}} {
		request.Groups = groups
		out, err := s.Search(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		assertSearchWithholdsLiveTV(t, out)
		if len(groups) != 1 && (len(out.Groups) == 0 || len(out.Groups[0].Items) != 1 || out.Groups[0].Items[0].Title != "Northwind Film") {
			t.Errorf("nonlive search lost: %+v", out.Groups)
		}
	}
}

func TestSearchLiveTVPrivacyExplicitAndRevokedCursor(t *testing.T) {
	s, request := searchPrivacyCatalog(t)
	request.Group = "live-tv"
	if _, err := s.Search(context.Background(), request); !errors.Is(err, ErrSearchQuery) {
		t.Fatalf("denied explicit live search: %v", err)
	}
	request.LiveTV = true
	out, err := s.Search(context.Background(), request)
	if err != nil || len(out.Groups) != 1 || out.Groups[0].TotalCount != 2 || !out.Groups[0].TotalCountExact || len(out.Groups[0].Items) != 1 || !out.Groups[0].HasMore || out.Groups[0].NextCursor == "" {
		t.Fatalf("allowed live search: %+v %v", out, err)
	}
	request.Cursor = out.Groups[0].NextCursor
	page, err := s.Search(context.Background(), request)
	if err != nil || len(page.Groups[0].Items) != 1 || page.Groups[0].Items[0].ID == out.Groups[0].Items[0].ID || page.Groups[0].HasMore || page.Groups[0].NextCursor != "" {
		t.Fatalf("allowed continuation: %+v %v", page, err)
	}
	seen := map[string]bool{out.Groups[0].Items[0].ID: true, page.Groups[0].Items[0].ID: true}
	if !seen["news"] || !seen["news24"] {
		t.Fatal("search included an obsolete generation or disabled source")
	}
	// Keep the old fence deliberately: the capability denial itself must hold,
	// even if another composition forgets to refresh its viewer fence.
	request.LiveTV = false
	if _, err = s.Search(context.Background(), request); !errors.Is(err, ErrCursor) {
		t.Fatalf("revoked live continuation: %v", err)
	}
}

func TestSearchLiveTVPrivacyGuardsLeafLookup(t *testing.T) {
	s, request := searchPrivacyCatalog(t)
	for _, cursor := range []string{"", "old-continuation"} {
		request.Cursor = cursor
		out := SearchGroupResult{Items: []ContentEntry{}}
		err := s.searchLiveGroup(context.Background(), request, searchGroups[len(searchGroups)-1], "scope", "stamp", ContentRevision{}, 1, &out)
		if err == nil || len(out.Items) != 0 || out.TotalCount != 0 || out.TotalCountExact || out.HasMore || out.NextCursor != "" {
			t.Errorf("denied leaf lookup: count=%d items=%d more=%t cursor=%t error=%v", out.TotalCount, len(out.Items), out.HasMore, out.NextCursor != "", err)
		}
	}
}

func TestSearchChannelAnchorUsesActualActiveSourceMapping(t *testing.T) {
	s, request := searchPrivacyCatalog(t)
	request.Group, request.LiveTV, request.Limit = "live-tv", true, 20
	out, err := s.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 1 || len(out.Groups[0].Items) != 2 {
		t.Fatalf("active rows: %+v", out.Groups)
	}
	for _, item := range out.Groups[0].Items {
		nav := item.Navigation
		if nav == nil || nav.View != "channel" || nav.EntityID != item.ID || nav.ChannelID != item.ID || nav.SourceID != "active" || nav.Provenance != "live-source" {
			t.Fatalf("channel mapping omitted or inferred: %+v", nav)
		}
	}
	// A source replacement is read from its current published generation. The
	// result must not retain the previous source's identity or obsolete rows.
	if _, err = s.db.Exec(`UPDATE live_sources SET state='disabled' WHERE id='active'; UPDATE live_sources SET state='active' WHERE id='disabled'`); err != nil {
		t.Fatal(err)
	}
	out, err = s.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 1 || len(out.Groups[0].Items) != 1 || out.Groups[0].Items[0].Navigation.SourceID != "disabled" || out.Groups[0].Items[0].ID != "disabled" {
		t.Fatalf("stale source identity retained: %+v", out.Groups)
	}
}
