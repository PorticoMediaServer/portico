package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"testing"
)

func TestGuideServerHiddenOwnerReadAndFencedWrite(t *testing.T) {
	f := newGuideDirectoryHTTPFixture(t)
	f.d.Administration = administration.New(f.d.DB)
	f.d.liveAdministrationRoutes(f.mux)
	target := f.directory(t, f.owner.AccessToken, url.Values{"sourceId": {f.a.ID}, "limit": {"1"}}).Channels[0]
	path := "/v1/admin/live-sources/" + f.a.ID + "/channel-map"
	read := func() administration.ChannelMapPage {
		w := f.call(t, "GET", path+"?channelId="+url.QueryEscape(target.ID), f.owner.AccessToken, nil)
		var env struct {
			Result administration.ChannelMapPage `json:"result"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &env) != nil {
			t.Fatalf("lookup: %d %s", w.Code, w.Body)
		}
		return env.Result
	}
	write := func(token, op string, revision int64, hidden bool) int {
		w := f.call(t, "PUT", path, token, administration.ChannelMapChange{ExpectedRevision: revision, OperationID: op, Overrides: []administration.ChannelMapOverride{{ChannelID: target.ID, Number: "201", GuideChannelID: "guide-override", Hidden: hidden}}})
		return w.Code
	}
	before := read()
	if before.Revision < 1 {
		t.Fatal(before)
	}
	if code := write(f.owner.AccessToken, "hide-channel", before.Revision, true); code != 200 {
		t.Fatal("hide", code)
	}
	hidden := read()
	if hidden.Revision != before.Revision+1 || !hidden.Items[0].Hidden || hidden.Items[0].Number != "201" || hidden.Items[0].OverrideNumber != "201" || hidden.Items[0].GuideChannelID != "guide-override" {
		t.Fatal(hidden)
	}
	if code := write(f.owner.AccessToken, "stale-show", before.Revision, false); code != 409 {
		t.Fatal("stale show", code)
	}
	// Visibility flags never widen a non-owner's domain, including administrators.
	for _, role := range []string{"member", "admin"} {
		if _, err := f.d.DB.Exec(`UPDATE direct_memberships SET role=? WHERE account_id='live-member'`, role); err != nil {
			t.Fatal(err)
		}
		session, err := f.d.Identity.Issue("live-member", "member-profile", "local", role, 1)
		if err != nil {
			t.Fatal(err)
		}
		token := session.AccessToken
		if code := write(token, "forbidden-"+role, hidden.Revision, false); code != 403 {
			t.Fatalf("%s hide write: %d", role, code)
		}
		lookup := f.call(t, "GET", path+"?channelId="+url.QueryEscape(target.ID), token, nil)
		if lookup.Code != 403 {
			t.Fatalf("%s map lookup: %d", role, lookup.Code)
		}
		for _, route := range []string{"/v1/guide/channels?includeServerHidden=true", "/v1/guide/sources?includeServerHidden=true", "/v1/guide?kind=live-source&start=2026-09-05T12:00:00Z&end=2026-09-05T13:00:00Z&timezone=UTC&limit=50&includeServerHidden=true"} {
			if w := f.call(t, "GET", route, token, nil); w.Code != http.StatusForbidden {
				t.Fatalf("%s read %s: %d %s", role, route, w.Code, w.Body)
			}
		}
		page := f.directory(t, token, url.Values{"sourceId": {f.a.ID}, "limit": {"50"}})
		if page.Total != 59 {
			t.Fatalf("%s hidden total: %d", role, page.Total)
		}
		for _, row := range page.Channels {
			if row.ID == target.ID || row.ServerHidden {
				t.Fatalf("%s saw hidden row", role)
			}
		}
	}
	normal := f.directory(t, f.owner.AccessToken, url.Values{"sourceId": {f.a.ID}})
	page := f.directory(t, f.owner.AccessToken, url.Values{"sourceId": {f.a.ID}, "includeServerHidden": {"true"}, "sort": {"name"}})
	if normal.Total != 59 || page.Total != 60 || normal.Revision == page.Revision {
		t.Fatal(normal.Total, page.Total)
	}
	found := false
	for _, row := range page.Channels {
		if row.ID == target.ID {
			found = true
			if !row.ServerHidden || row.Hidden || row.Number != "201" || row.Mark == nil {
				t.Fatal(row)
			}
		}
	}
	if !found {
		t.Fatal("owner hidden row absent")
	}
	q := url.Values{"kind": {"live-source"}, "sourceId": {f.a.ID}, "channels": {target.ID}, "start": {"2026-09-05T12:00:00Z"}, "end": {"2026-09-05T13:00:00Z"}, "timezone": {"UTC"}, "limit": {"1"}, "includeServerHidden": {"true"}}
	w := f.call(t, "GET", "/v1/guide?"+q.Encode(), f.owner.AccessToken, nil)
	var guide struct {
		Guide livechannels.Guide `json:"guide"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &guide) != nil || len(guide.Guide.Channels) != 1 || !guide.Guide.Channels[0].ServerHidden {
		t.Fatalf("hidden programmes: %d %s", w.Code, w.Body)
	}
	if code := write(f.owner.AccessToken, "show-channel", hidden.Revision, false); code != 200 {
		t.Fatal("show", code)
	}
	shown := read()
	if shown.Items[0].Hidden || shown.Items[0].Number != "201" || shown.Items[0].GuideChannelID != "guide-override" {
		t.Fatal(shown)
	}
	if page := f.directory(t, f.owner.AccessToken, url.Values{"sourceId": {f.a.ID}}); page.Total != 60 {
		t.Fatal(page.Total)
	}
}
