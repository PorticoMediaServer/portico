package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestBrowseContextFacetsAndCountBatchHTTP(t *testing.T) {
	handler, owner, _ := homeAPIFixture(t)
	query := `{"variant":"group","all":[{"variant":"predicate","field":"year","operator":"at-least","value":2002},{"variant":"predicate","field":"genre","operator":"contains","value":"Drama"}]}`
	path := "/v1/libraries/movies/facets?field=genre&pivot=movies&query=" + url.QueryEscape(query)
	w := homeAPIRequest(t, handler, owner.AccessToken, "GET", path, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var facet catalog.FacetPage
	if err := json.Unmarshal(w.Body.Bytes(), &facet); err != nil {
		t.Fatal(err)
	}
	if len(facet.Values) != 0 {
		t.Fatalf("facet ignored other filter: %+v", facet)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "POST", "/v1/libraries/movies/browse/counts", `{"pivot":"movies","queries":[{"id":"clear-genre","query":{"variant":"predicate","field":"year","operator":"at-least","value":2002}},{"id":"clear-all","query":null}]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/libraries/{id}/browse/counts", w)
	var batch catalog.BrowseCountBatch
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Counts) != 2 || batch.Counts[0].Total != 2 || batch.Counts[1].Total != 3 {
		t.Fatalf("relax counts %+v", batch)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "POST", "/v1/browse/counts", `{"pivot":"titles","queries":[{"id":"my-list","query":{"variant":"predicate","field":"watchlisted","operator":"equals","value":true}}]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/browse/counts", w)
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Counts) != 1 || batch.Counts[0].Total != 3 {
		t.Fatalf("group count %+v", batch)
	}
	for _, body := range []string{`{"queries":[]}`, `{"queries":[{"id":"x","query":{"variant":"predicate","field":"unpublished","operator":"equals","value":1}}]}`, `{"queries":[{"id":"x","query":null},{"id":"x","query":null}]}`} {
		w = homeAPIRequest(t, handler, owner.AccessToken, "POST", "/v1/libraries/movies/browse/counts", body)
		if w.Code != 400 {
			t.Fatalf("bad count query accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w = homeAPIRequest(t, handler, "", "POST", "/v1/browse/counts", `{"queries":[{"id":"x","query":null}]}`)
	if w.Code != 401 {
		t.Fatalf("unauthenticated count %d", w.Code)
	}
}

func TestBrowseHomeDisplayKeepsCanonicalMembershipAndActualActions(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	tv := f.c.Library("tv", "TV", "tv", "/tv")
	weekly := f.c.Show(tv, "Weekly", 2020)
	one := f.c.Season(weekly, 1)
	previous := f.c.Episode(weekly, one, 1, "/tv/weekly1.mkv")
	newest := f.c.Episode(weekly, one, 2, "/tv/weekly2.mkv")
	pack := f.c.Show(tv, "Season Pack", 2021)
	oldSeason, newSeason := f.c.Season(pack, 1), f.c.Season(pack, 2)
	old := f.c.Episode(pack, oldSeason, 1, "/tv/pack1.mkv")
	part1 := f.c.Episode(pack, newSeason, 1, "/tv/pack2-1.mkv")
	part2 := f.c.Episode(pack, newSeason, 2, "/tv/pack2-2.mkv")
	for _, it := range []catalogtest.Item{previous, old} {
		f.c.Fields(it.ID, map[string]any{"added_text": "2026-01-01T00:00:00.000Z"})
	}
	for _, it := range []catalogtest.Item{newest, part1, part2} {
		f.c.Fields(it.ID, map[string]any{"added_text": "2026-10-20T00:00:00.000Z"})
	}
	owner, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	profile := identity.PersonalKey(owner.Viewer)
	f.c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision)VALUES(?,?,1,0,1),(?,?,0,1,1)`, profile, weekly.ID, profile, newest.ID)
	f.c.Exec(`UPDATE direct_profiles SET allowed_libraries='["movies","tv"]',revision=revision+1 WHERE id=?`, f.kids.Viewer.ProfileID)
	f.c.Drain()
	query := `{"variant":"group","all":[{"variant":"predicate","field":"homeRow","operator":"equals","value":"new_on_server"},{"variant":"predicate","field":"library","operator":"equals","value":"tv"},{"variant":"predicate","field":"year","operator":"at-least","value":2020}]}`
	read := func(token string, display bool, cursor string, rangeBody string) (catalog.BrowseResult, int, string) {
		t.Helper()
		body := map[string]any{"pivot": "titles", "query": json.RawMessage(query), "sort": []map[string]string{{"field": "title", "direction": "asc"}}, "limit": 1}
		if display {
			body["homeRowDisplay"] = "new_on_server"
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		if rangeBody != "" {
			body["range"] = json.RawMessage(rangeBody)
		}
		raw, _ := json.Marshal(body)
		w := homeAPIRequest(t, f.h, token, "POST", "/v1/browse", string(raw))
		var page catalog.BrowseResult
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			assertSpecResponse(t, "POST", "/v1/browse", w)
		}
		return page, w.Code, w.Body.String()
	}
	canonical, status, raw := read(f.token, false, "", "")
	if status != 200 || len(canonical.Entries) != 1 || canonical.Entries[0].ID != pack.Public || len(canonical.DisplayTargets) != 0 {
		t.Fatalf("generic titles changed: %d %s", status, raw)
	}
	first, status, raw := read(f.token, true, "", "")
	if status != 200 || len(first.Entries) != 1 || first.Entries[0].ID != newSeason.Public || first.Entries[0].Kind != "season" || len(first.DisplayTargets) != 1 || first.DisplayTargets[0].MembershipID != pack.Public || first.PageInfo.Total != 2 {
		t.Fatalf("actual season projection %d %s", status, raw)
	}
	if !reflect.DeepEqual(first.PositionIndex, canonical.PositionIndex) || first.PageInfo.DurationMs != canonical.PageInfo.DurationMs {
		t.Fatal("display mode changed canonical measures")
	}
	replay, status, raw := read(f.token, true, "", "")
	if status != 200 || !reflect.DeepEqual(first.Entries, replay.Entries) {
		t.Fatalf("replay changed %d %s", status, raw)
	}
	next, status, raw := read(f.token, true, first.PageInfo.NextCursor, "")
	if status != 200 || len(next.Entries) != 1 || next.Entries[0].ID != newest.Public || next.Entries[0].Kind != "episode" || next.DisplayTargets[0].MembershipID != weekly.Public || next.PageInfo.NextCursor != "" || next.PageInfo.HasMore {
		t.Fatalf("actual episode projection %d %s", status, raw)
	}
	if actions := next.Entries[0].HomeActions; actions == nil || !actions.State.Favorite || actions.State.Watchlisted {
		t.Fatalf("show state/grants copied to episode: %+v", actions)
	}
	// Anchors stay canonical even when the visible card is an episode.
	anchored, status, raw := read(f.token, true, "", fmt.Sprintf(`{"start":1,"revision":%q,"anchorId":%q}`, first.PageInfo.Revision, weekly.Public))
	if status != 200 || len(anchored.Entries) != 1 || anchored.Entries[0].ID != newest.Public {
		t.Fatalf("canonical anchor %d %s", status, raw)
	}
	_, status, raw = read(f.token, false, first.PageInfo.NextCursor, "")
	if status == 200 {
		t.Fatalf("display cursor crossed modes: %s", raw)
	}
	_, status, raw = read(f.token, true, canonical.PageInfo.NextCursor, "")
	if status == 200 {
		t.Fatalf("canonical cursor crossed modes: %s", raw)
	}
	// A client-supplied ID or negative/alternative source cannot confer authority.
	for _, bad := range []string{`{"pivot":"titles","homeRowDisplay":"new_on_server","query":{"variant":"group","not":{"variant":"predicate","field":"homeRow","operator":"equals","value":"new_on_server"}}}`, `{"pivot":"titles","homeRowDisplay":"new_on_server","query":{"variant":"predicate","field":"favorite","operator":"equals","value":true}}`, `{"pivot":"titles","homeRowDisplay":"new_on_server","limit":61,"query":{"variant":"predicate","field":"homeRow","operator":"equals","value":"new_on_server"}}`} {
		w := homeAPIRequest(t, f.h, f.token, "POST", "/v1/browse", bad)
		if w.Code != 400 {
			t.Fatalf("unbound display %d %s", w.Code, w.Body.String())
		}
	}
	member, status, raw := read(f.kids.AccessToken, true, "", "")
	if status != 200 || member.PageInfo.Total != 2 {
		t.Fatalf("member fixture %d %s", status, raw)
	}
	f.c.Exec(`UPDATE direct_profiles SET allowed_libraries='["movies"]',revision=revision+1 WHERE id=?`, f.kids.Viewer.ProfileID)
	denied, status, raw := read(f.kids.AccessToken, true, member.PageInfo.NextCursor, "")
	if status == 200 && (len(denied.Entries) > 0 || denied.PageInfo.Total > 0) || strings.Contains(raw, newest.Public) || strings.Contains(raw, newSeason.Public) {
		t.Fatalf("revoked replay leaked: %d %s", status, raw)
	}
	denied, status, raw = read(f.kids.AccessToken, true, "", "")
	if status != 200 || len(denied.Entries) != 0 || denied.PageInfo.NextCursor != "" || denied.PageInfo.HasMore {
		t.Fatalf("revoked source not true empty end: %d %s", status, raw)
	}
}

func TestBrowseCountsAndFacetsDoNotDiscloseRestrictedTotals(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	f.restrictLiveTV(false)
	w := homeAPIRequest(t, f.h, f.kids.AccessToken, "POST", "/v1/libraries/movies/browse/counts", `{"pivot":"movies","queries":[{"id":"all","query":null},{"id":"adult","query":{"variant":"predicate","field":"title","operator":"contains","value":"Adult"}}]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var batch catalog.BrowseCountBatch
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Counts) != 2 || batch.Counts[0].Total != 2 || batch.Counts[1].Total != 0 {
		t.Fatalf("restricted counts %+v", batch)
	}
	w = homeAPIRequest(t, f.h, f.kids.AccessToken, "POST", "/v1/libraries/private/browse/counts", `{"queries":[{"id":"all","query":null}]}`)
	if w.Code != 403 && w.Code != 404 {
		t.Fatalf("private library count %d %s", w.Code, w.Body.String())
	}
	q := url.QueryEscape(`{"variant":"predicate","field":"homeRow","operator":"equals","value":"new_on_server"}`)
	w = homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", "/v1/browse/facets?field=library&pivot=titles&query="+q, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var facet catalog.FacetPage
	if err := json.Unmarshal(w.Body.Bytes(), &facet); err != nil {
		t.Fatal(err)
	}
	if len(facet.Values) != 1 || facet.Values[0].Value != "movies" || facet.Values[0].Count != 2 {
		t.Fatalf("restricted library counts %+v", facet)
	}
}

func TestBrowseMusicUnmatchedUsesPublicMemberRouteAndCurrentSource(t *testing.T) {
	f := newSearchPrivacyFixture(t)
	music := f.c.Library("music", "Music", "music", "/music")
	artist := f.c.Artist(music, "Tagged artist")
	album := f.c.Album(artist, "Real unmatched album", 2026)
	song := f.c.Song(album, 1, "/music/song.flac", "Tagged song")
	f.c.Exec(`UPDATE direct_profiles SET allowed_libraries='["movies","music"]',revision=revision+1 WHERE id=?`, f.kids.Viewer.ProfileID)
	f.c.Drain()
	f.c.Exec(`INSERT INTO listening_metadata_attention(entity_id,kind,library_id,reason,generation,dismissed_generation)VALUES(?,'song','music','needs_selection',1,1) ON CONFLICT(entity_id) DO UPDATE SET reason='needs_selection',dismissed_generation=generation`, song.ID)
	for _, token := range []string{f.token, f.kids.AccessToken} {
		w := homeAPIRequest(t, f.h, token, "GET", "/v1/libraries/music/browse-capabilities?pivot=albums", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var caps catalog.BrowseCapabilities
		if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil {
			t.Fatal(err)
		}
		offered := false
		for _, field := range caps.Fields {
			offered = offered || field.ID == "match"
		}
		if !offered {
			t.Fatal("public match capability absent")
		}
		if caps.ResolvedPivot == nil || !strings.Contains(strings.Join(caps.ResolvedPivot.Filters, ","), "match") {
			t.Fatal("albums pivot does not offer the public match filter")
		}
		fileSize := false
		for _, sort := range caps.Sorts {
			if sort.OwnerOnly != (sort.ID == "fileSize") {
				t.Fatalf("incorrect owner-only offer %+v", sort)
			}
			fileSize = fileSize || sort.ID == "fileSize"
		}
		if !fileSize {
			t.Fatal("file size offer missing")
		}
		w = homeAPIRequest(t, f.h, token, "POST", "/v1/libraries/music/browse", `{"pivot":"albums","query":{"variant":"predicate","field":"match","operator":"equals","value":"unmatched"}}`)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var page catalog.BrowseResult
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 || page.Entries[0].ID != album.Public {
			t.Fatalf("public unmatched source %+v", page)
		}
	}
	for _, path := range []string{"/v1/admin/attention", "/v1/admin/libraries", "/v1/libraries/music/metadata/attention", "/v1/admin/metadata/attention/counts"} {
		w := homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", path, "")
		if w.Code != 403 {
			t.Fatalf("private owner route widened %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// This fixture has no metadata service. Its existing registration fence
	// remains unavailable for both viewers; public browse never bypasses it.
	for _, token := range []string{f.token, f.kids.AccessToken} {
		w := homeAPIRequest(t, f.h, token, "GET", "/v1/libraries/music/metadata/agent", "")
		if w.Code != 404 {
			t.Fatalf("absent metadata service exposed: %d %s", w.Code, w.Body.String())
		}
	}
	f.c.Exec(`UPDATE library_metadata_agents SET agent='local',revision=revision+1 WHERE library_id='music'`)
	w := homeAPIRequest(t, f.h, f.kids.AccessToken, "GET", "/v1/libraries/music/browse-capabilities?pivot=albums", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var caps catalog.BrowseCapabilities
	if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	for _, field := range caps.Fields {
		if field.ID == "match" {
			t.Fatal("local-only source still offers match")
		}
	}
}
