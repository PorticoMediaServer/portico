package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// Adding a whole view to a playlist adds exactly the titles the adding profile may see, in
// the view's own order, and says how many; taken up again after a restart it adds nothing
// twice.
func TestAddAWholeViewToAPlaylist(t *testing.T) {
	s, token, limited := seeded(t)
	films := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	view := url.Values{"library": {films}, "kind": {"movie"}, "sort": {"title"}, "order": {"desc"}}.Encode()
	seen := get[ItemPage](t, s, limited, "/v1/items?"+view+"&limit=200&total=true")
	all := get[ItemPage](t, s, token, "/v1/items?"+view+"&limit=200&total=true")
	if *seen.Total == 0 || *seen.Total >= *all.Total {
		t.Fatalf("the limited profile sees %d of %d films", *seen.Total, *all.Total)
	}
	var playlist List
	if w := call(t, s, "POST", "/v1/lists", limited, ListCreate{Kind: db.ListPlaylist, Name: "Everything"}, &playlist); w.Code != 201 {
		t.Fatalf("making the playlist answered %d: %s", w.Code, w.Body)
	}
	start := JobStart{Kind: listViewJobKind, Target: JobTarget{Filter: view}, Params: JSON(`{"list": "` + playlist.ID + `"}`)}
	var job Job
	if w := call(t, s, "POST", "/v1/jobs", limited, start, &job); w.Code != 202 {
		t.Fatalf("starting the job answered %d: %s", w.Code, w.Body)
	}
	job = waitForJob(t, s, limited, job.ID)
	wantResult := JobListResult{Added: int64(*seen.Total)}
	if job.State != db.JobDone || job.Result == nil || *job.Result != wantResult {
		t.Fatalf("the job result: %+v", job)
	}
	entries := func() []string {
		page := get[ItemPage](t, s, limited, "/v1/items?list="+playlist.ID+"&limit=200")
		var out []string
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out
	}
	got := entries()
	if len(got) != len(seen.Items) {
		t.Fatalf("the playlist has %d entries, the view %d", len(got), len(seen.Items))
	}
	for n, it := range seen.Items {
		if got[n] != it.ID {
			t.Fatalf("entry %d is %s, the view's %s: not in the view's order", n, got[n], it.ID)
		}
	}

	// Taken up again, as after a restart: no page is added twice.
	stored, err := s.db.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var again jobs.Request
	if err := json.Unmarshal([]byte(stored.Payload), &again); err != nil {
		t.Fatal(err)
	}
	again.UserID = stored.OwnerID
	s.runListViewJob(context.Background(), jobs.Work{Job: stored, Request: again, Progress: func(int64, int64) {}})
	if n := len(entries()); n != len(seen.Items) {
		t.Fatalf("taken up again the playlist has %d entries, not %d", n, len(seen.Items))
	}
	if result := get[Job](t, s, limited, "/v1/jobs/"+job.ID).Result; result == nil || *result != wantResult {
		t.Fatalf("replay changed the result: %+v", result)
	}

	// A separate collection job distinguishes new members from ones already there.
	var collection List
	call(t, s, "POST", "/v1/lists", limited, ListCreate{Kind: db.ListCollection, Name: "Films"}, &collection)
	start.Params = JSON(`{"list":"` + collection.ID + `"}`)
	for attempt := 0; attempt < 2; attempt++ {
		call(t, s, "POST", "/v1/jobs", limited, start, &job)
		job = waitForJob(t, s, limited, job.ID)
		want := JobListResult{Added: int64(*seen.Total)}
		if attempt == 1 {
			want = JobListResult{AlreadyPresent: int64(*seen.Total)}
		}
		if job.Result == nil || *job.Result != want {
			t.Fatalf("collection attempt %d result: %+v", attempt, job.Result)
		}
	}

	// A list the profile may not change is refused before anything is stored.
	var theirs List
	call(t, s, "POST", "/v1/lists", token, ListCreate{Kind: db.ListPlaylist, Name: "Not yours"}, &theirs)
	start.Params = JSON(`{"list": "` + theirs.ID + `"}`)
	if code := status(t, s, "POST", "/v1/jobs", limited, start); code < 400 {
		t.Fatalf("adding to someone else's playlist answered %d", code)
	}
}

// One overfull show must not keep other shows from being added, and a server restart after
// that partial commit must neither duplicate their episodes nor lose the failed title.
func TestListJobKeepsPartialResultsAcrossRestart(t *testing.T) {
	s, owner, _ := seededWith(t, 60, 1)
	ctx := context.Background()
	var shows db.AdminLibrary
	libraries, err := s.db.AdminLibraries(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range libraries {
		if lib.Kind == db.LibraryShows {
			shows = lib
		}
	}
	key := db.ItemKey{Kind: db.KeyFolder, Key: "long-show"}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: shows.ID, PathID: shows.Folders[0].ID,
		Items: []db.ImportItem{{Kind: db.KindShow, Title: "A very long show", Keys: []db.ItemKey{key}}}}); err != nil {
		t.Fatal(err)
	}
	for first := 0; first <= db.QueueMax; first += 200 {
		var items []db.ImportItem
		for n := first; n < min(first+200, db.QueueMax+1); n++ {
			name := fmt.Sprintf("episode-%05d", n)
			items = append(items, db.ImportItem{Kind: db.KindEpisode, Title: name, Parent: &key, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: name}}})
		}
		if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: shows.ID, PathID: shows.Folders[0].ID, Items: items}); err != nil {
			t.Fatal(err)
		}
	}
	var list List
	call(t, s, "POST", "/v1/lists", owner, ListCreate{Kind: db.ListPlaylist, Name: "Shows"}, &list)
	c := CallerOf(t, s, owner)
	req := jobs.Request{Target: jobs.Target{Filter: "library=" + shows.PublicID + "&kind=show"},
		Params: []byte(`{"list":"` + list.ID + `"}`), UserID: c.UserID, ProfileID: c.ProfileID}
	payload, _ := json.Marshal(req)
	job, err := s.db.StartJob(ctx, db.NewJob{Kind: listViewJobKind, Target: "partial-restart", Payload: string(payload), OwnerID: c.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if result := get[Job](t, s, owner, "/v1/jobs/"+job.PublicID).Result; result == nil || *result != (JobListResult{}) {
		t.Fatalf("new job result: %+v", result)
	}
	stop, cancel := context.WithCancel(ctx)
	err = s.runListViewJob(stop, jobs.Work{Job: job, Request: req, Progress: func(int64, int64) { cancel() }})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("partial run: %v", err)
	}
	path := "/v1/jobs/" + job.PublicID + "/failures"
	failures := get[Page[JobItemFailure]](t, s, owner, path)
	if len(failures.Items) != 1 || failures.Items[0].Title != "A very long show" || failures.Items[0].Error.Code != "too_many_titles" {
		t.Fatalf("partial failures: %+v", failures)
	}
	before := get[ItemPage](t, s, owner, "/v1/items?list="+list.ID+"&total=true&limit=200")
	if *before.Total == 0 {
		t.Fatal("the other shows were not added")
	}
	wantResult := JobListResult{Added: int64(*before.Total), Failed: 1}
	if result := get[Job](t, s, owner, "/v1/jobs/"+job.PublicID).Result; result == nil || *result != wantResult {
		t.Fatalf("partial result: %+v, want %+v", result, wantResult)
	}
	database := filepath.Join(s.dataDir, "main.db")
	stopServer(s)
	closeDB(s.db)
	s = openTestServer(t, database)
	owner = onMain(t, s, signIn(t, s, 4)).AccessToken
	if ended := waitForJob(t, s, owner, job.PublicID); ended.State != db.JobFailed || ended.Error != selectionFailureSummary ||
		ended.Result == nil || *ended.Result != wantResult {
		t.Fatalf("resumed job: %+v", ended)
	}
	after := get[ItemPage](t, s, owner, "/v1/items?list="+list.ID+"&total=true&limit=200")
	if *after.Total != *before.Total {
		t.Fatalf("restart duplicated entries: %d before, %d after", *before.Total, *after.Total)
	}
	for n, it := range before.Items {
		if after.Items[n].ID != it.ID {
			t.Fatalf("restart changed entry order at %d", n)
		}
	}
	if kept := get[Page[JobItemFailure]](t, s, owner, path); len(kept.Items) != 1 || kept.Items[0].ItemID != failures.Items[0].ItemID {
		t.Fatalf("restart lost the failure: %+v", kept)
	}
	// Even another account's administrator sees only current visible source results.
	addMember(t, s, owner, "result-reader", "admin")
	admin := signInAs(t, s, "result-reader", testPassword, 5).AccessToken
	ac := CallerOf(t, s, admin)
	p, err := s.db.ProfileByID(ctx, ac.UserID, ac.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, all := range []bool{false, true} {
		if _, err := s.db.SetProfileRestrictions(ctx, ac.UserID, p.PublicID, db.Restrictions{AllLibraries: all}, s.now()); err != nil {
			t.Fatal(err)
		}
		got := get[Job](t, s, admin, "/v1/jobs/"+job.PublicID)
		want := wantResult
		if !all {
			want = JobListResult{}
			if got.State != db.JobDone || got.Error != "" {
				t.Fatalf("hidden failures left a partial-failure summary: %+v", got)
			}
		}
		if got.Result == nil || *got.Result != want {
			t.Fatalf("all libraries %t: result %+v, want %+v", all, got.Result, want)
		}
	}
	if err := s.db.DeleteLibrary(ctx, shows.ID); err != nil {
		t.Fatal(err)
	}
	if got := get[Job](t, s, owner, "/v1/jobs/"+job.PublicID); got.Result == nil || *got.Result != (JobListResult{}) || got.State != db.JobDone {
		t.Fatalf("deleted source titles still counted: %+v", got)
	}
}
