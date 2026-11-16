package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A whole-view edit reaches beyond its first page even when the edit removes titles from
// the filter. Refresh uses that view too; explicit selections and role checks still work.
func TestMetadataJobsOnAWholeView(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv", "Heat (1995).mkv")
	ctx := context.Background()
	libs, err := sh.s.db.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lib := libs[0]
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}}}
	for n := range 205 {
		name := fmt.Sprintf("Film %03d", n)
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindMovie, Title: name, Year: 2001,
			Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}})
	}
	if _, err := sh.s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	view := "library=" + sh.library + "&kind=movie&year=2001&sort=year&limit=1&at=200"
	job := sh.startJob(JobStart{Kind: jobEditItems, Target: JobTarget{Filter: view}, Params: JSON(`{"year":2002}`)})
	if job.State != db.JobDone || job.Progress == nil || job.Progress.Done != 205 {
		t.Fatalf("whole-view edit: %+v", job)
	}
	left := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+sh.library+"&kind=movie&year=2001&total=true")
	if *left.Total != 0 {
		t.Fatalf("edit left %d titles from its filter", *left.Total)
	}
	changed := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+sh.library+"&kind=movie&year=2002&total=true")
	if *changed.Total != 205 {
		t.Fatalf("edited %d titles", *changed.Total)
	}
	// Only the two matched films are in this refresh view; the provider is an in-process fake.
	asked := sh.tmdb.Requests.Load()
	job = sh.startJob(JobStart{Kind: jobRefreshItems, Target: JobTarget{Filter: "library=" + sh.library + "&kind=movie&year=1995,1999"}})
	if job.State != db.JobDone || job.Progress == nil || job.Progress.Done != 2 || sh.tmdb.Requests.Load()-asked < 2 {
		t.Fatalf("whole-view refresh: %+v, provider requests %d", job, sh.tmdb.Requests.Load()-asked)
	}
	// A real admin's main-profile limits also apply to every page and its progress count.
	admin := addMember(t, sh.s, sh.owner, "held-admin", "admin")
	sh.do("PATCH", "/v1/admin/members/"+admin.ID, map[string]any{"allLibraries": true,
		"limits": map[string]any{"maxRatingAge": 13, "allowUnrated": true}}, nil, 200)
	token := signInAs(t, sh.s, "held-admin", testPassword, 8).AccessToken
	var held Job
	start := JobStart{Kind: jobEditItems, Target: JobTarget{Filter: "library=" + sh.library + "&kind=movie"}, Params: JSON(`{"edition":"Shared"}`)}
	if w := call(t, sh.s, "POST", "/v1/jobs", token, start, &held); w.Code != 202 {
		t.Fatalf("admin view: %d %s", w.Code, w.Body)
	}
	if held = waitForJob(t, sh.s, token, held.ID); held.State != db.JobDone || held.Progress == nil || held.Progress.Done != 205 {
		t.Fatalf("limited admin's view: %+v", held)
	}
	sh.do("POST", "/v1/jobs", JobStart{Kind: jobEditItems, Target: JobTarget{Filter: "library=" + sh.library}, Params: JSON(`{"year":2002}`)}, nil, 400)
	if code := status(t, sh.s, "POST", "/v1/jobs", sh.member,
		JobStart{Kind: jobEditItems, Target: JobTarget{Filter: view}, Params: JSON(`{"year":2002}`)}); code != 403 {
		t.Fatalf("member edited a view: %d", code)
	}
	if code := status(t, sh.s, "POST", "/v1/jobs", sh.limitedAdmin,
		JobStart{Kind: jobRefreshItems, Target: JobTarget{Filter: "library=lib_absent&kind=movie"}}); code != 404 {
		t.Fatalf("hidden library view: %d", code)
	}
}
