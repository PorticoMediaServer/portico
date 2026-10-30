package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Retained watch history of a removed profile is the owner's main profile's to
// read, marked with the name the profile had when it was removed. Every other
// principal is refused before the projection runs.
func TestRetainedRemovedProfileHistoryIsOwnerMainOnly(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, e := f.deps.DB.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES('owner-child','owner','Child',0,1),('owner-kid','owner','Kids',0,2)`); e != nil {
		t.Fatal(e)
	}
	// Logical retirement keeps the final display name and removes every private
	// resource; library media and the owner's play history stay.
	if _, e := f.deps.DB.Exec(`UPDATE direct_profiles SET deleted=1,pin_hash=NULL,art='slate',allowed_libraries=NULL,position=0 WHERE id='owner-kid'`); e != nil {
		t.Fatal(e)
	}
	// until is the last completed quarter; the play starts a minute before it so
	// the report bound never depends on the second the test runs.
	now := time.Now().UTC().Truncate(15 * time.Minute)
	started := now.Add(-time.Minute)
	if _, e := f.deps.DB.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms,completed)
	 VALUES('p-kid','local','owner','owner-kid','owner','Kids',1,'movies',1,'Movie',?,?,1)`, started.UnixMilli(), started.UnixMilli()); e != nil {
		t.Fatal(e)
	}
	context := "timeZone=UTC&until=" + url.QueryEscape(now.Format(time.RFC3339Nano))
	paths := []string{
		"/v1/admin/play-history?" + context,
		"/v1/admin/play-history/summary?period=all&" + context,
		"/v1/admin/play-history/dashboard?period=all&" + context,
		"/v1/admin/play-history/people?period=all&" + context,
		"/v1/admin/dashboard/report?period=all&" + context,
	}

	response := f.call("GET", paths[0], f.owner, "")
	if response.Code != http.StatusOK {
		t.Fatalf("owner history: %d %s", response.Code, response.Body)
	}
	var page struct {
		Total   *int64 `json:"total"`
		Entries []struct {
			Profile string `json:"profile"`
			Title   string `json:"title"`
		} `json:"entries"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if page.Total == nil || *page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].Profile != "Kids (removed)" {
		t.Fatalf("owner history body: %+v", page)
	}
	response = f.call("GET", paths[1], f.owner, "")
	if response.Code != http.StatusOK {
		t.Fatalf("owner summary: %d %s", response.Code, response.Body)
	}
	var summary struct {
		Plays      int64 `json:"plays"`
		MostActive []struct {
			Name string `json:"name"`
		} `json:"mostActive"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &summary); e != nil {
		t.Fatal(e)
	}
	if summary.Plays != 1 || len(summary.MostActive) != 1 || summary.MostActive[0].Name != "owner · Kids (removed)" {
		t.Fatalf("owner summary body: %+v", summary)
	}

	// The owner's child profile is a live member session, not the main profile.
	child, e := f.deps.Identity.Issue("owner", "owner-child", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	for name, token := range map[string]string{"admin main": f.admin, "member main": f.member, "owner child": child.AccessToken} {
		for _, path := range paths {
			response := f.call("GET", path, token, "")
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s read %s: %d %s", name, path, response.Code, response.Body)
			}
		}
	}
}
