package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"testing"
	"time"
)

func TestFailureMemoryIsOwnerOnlyAndCanBeCleared(t *testing.T) {
	f := newV1Fixture(t, 1)
	var page playback.RouteFailurePage
	f.call("GET", "/v1/admin/playback/route-failures", nil, nil, 200, &page)
	if page.Items == nil {
		t.Fatal("missing empty list")
	}
	f.call("DELETE", "/v1/admin/playback/route-failures/file", nil, nil, 204, nil)
	member, _ := f.member()
	for _, method := range []string{"GET", "DELETE"} {
		path := "/v1/admin/playback/route-failures"
		if method == "DELETE" {
			path += "/file"
		}
		w := f.raw(method, path, member, nil, nil)
		if w.Code != 403 && w.Code != 401 {
			t.Fatal("non-owner reached failure memory", method, w.Code, w.Body.String())
		}
	}
}

func TestFailureMemoryNamesTheTitle(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, item := f.member()
	_ = member
	var token string
	if err := f.db.QueryRow(`SELECT a.token FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id WHERE e.public_id=pid_blob(?)`, item).Scan(&token); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := f.db.QueryRow(`SELECT token_hash FROM authorization_family_tokens LIMIT 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO playback_route_failures(session_hash,asset_id,engine,route,code,reported_at_ms) VALUES(?,?,'native','original','decode_error',?)`, hash, token, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var page playback.RouteFailurePage
	f.call("GET", "/v1/admin/playback/route-failures", nil, nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].ItemID != item || page.Items[0].Title != "Kids Film" || page.Items[0].Kind != "movie" {
		t.Fatalf("failure row does not name its title: %+v", page.Items)
	}
}
