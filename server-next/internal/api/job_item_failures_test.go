package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// A real state job reports every failed title, including past the first page, and keeps
// that report when the database is closed and reopened.
func TestStateJobFailureResultsSurviveRestart(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 2)
	var job Job
	var music string
	for _, lib := range get[Libraries](t, s, owner, "/v1/libraries").Items {
		if lib.Kind == db.LibraryMusic {
			music = lib.ID
		}
	}
	start := JobStart{Kind: stateJobKind, Target: JobTarget{Filter: "library=" + music + "&kind=track"}, Params: JSON(`{"watched":true}`)}
	if w := call(t, s, "POST", "/v1/jobs", owner, start, &job); w.Code != 202 {
		t.Fatalf("starting: %d %s", w.Code, w.Body)
	}
	job = waitForJob(t, s, owner, job.ID)
	if job.State != db.JobFailed || job.Error != "Some titles could not be changed. The rest is done." {
		t.Fatalf("summary: %+v", job)
	}
	read := func() []string {
		t.Helper()
		var ids []string
		path := "/v1/jobs/" + job.ID + "/failures"
		for cursor := ""; ; {
			page := get[Page[JobItemFailure]](t, s, owner, path+"?cursor="+url.QueryEscape(cursor))
			if len(page.Items) > 50 || cursor == "" && len(page.Items) != 50 {
				t.Fatalf("default page size: %d", len(page.Items))
			}
			for _, f := range page.Items {
				if f.ItemID == "" || f.Title == "" || f.Error.Code != "unsupported_state" || f.Error.Message != "Music has play counts, not a watched state." {
					t.Fatalf("failure: %+v", f)
				}
				if slices.Contains(ids, f.ItemID) {
					t.Fatalf("repeated title: %s", f.ItemID)
				}
				ids = append(ids, f.ItemID)
			}
			if cursor = page.NextCursor; cursor == "" {
				return ids
			}
		}
	}
	before := read()
	tracks := get[ItemPage](t, s, owner, "/v1/items?library="+music+"&kind=track&total=true&limit=1")
	if len(before) != *tracks.Total || len(before) <= 50 {
		t.Fatalf("%d failures for %d tracks", len(before), *tracks.Total)
	}
	path := filepath.Join(s.dataDir, "main.db")
	stopServer(s)
	closeDB(s.db)
	s = openTestServer(t, path)
	owner = onMain(t, s, signIn(t, s, 3)).AccessToken
	if after := read(); !slices.Equal(before, after) {
		t.Fatalf("restart changed the results: %d before, %d after", len(before), len(after))
	}
	if got := get[Job](t, s, owner, "/v1/jobs/"+job.ID); got.Error != job.Error || got.State != job.State {
		t.Fatalf("restart changed the summary: %+v", got)
	}
	// A title (here, the whole source library) can disappear during the job, after its
	// outcome committed. Those cascaded rows must not leave a failed summary behind.
	stored, err := s.db.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := s.db.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := CallerOf(t, s, owner)
	err = s.runStateJob(context.Background(), jobs.Work{Job: stored,
		Request: jobs.Request{Target: jobs.Target(start.Target), Params: []byte(start.Params), UserID: c.UserID, ProfileID: c.ProfileID},
		Progress: func(done, total int64) {
			if done != total {
				return
			}
			for _, lib := range libraries {
				if lib.PublicID == music {
					if err := s.db.DeleteLibrary(context.Background(), lib.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
		}})
	if err != nil {
		t.Fatalf("disappeared titles failed the job: %v", err)
	}
	if got := get[Job](t, s, owner, "/v1/jobs/"+job.ID); got.State != db.JobDone || got.Error != "" {
		t.Fatalf("empty results left a failed summary: %+v", got)
	}
	if page := get[Page[JobItemFailure]](t, s, owner, "/v1/jobs/"+job.ID+"/failures"); len(page.Items) != 0 {
		t.Fatalf("deleted title results: %+v", page)
	}
	for _, got := range get[Page[AdminJob]](t, s, owner, "/v1/admin/jobs").Items {
		if got.ID == job.ID && (got.State != db.JobDone || got.Error != "") {
			t.Fatalf("owner's list kept a failed summary: %+v", got)
		}
	}
}

// Job access and title access are separate: even an administrator must read the results
// through their selected profile's current restrictions, before the page limit.
func TestJobFailuresUseJobAccessAndCurrentTitleVisibility(t *testing.T) {
	s, owner, members := seededWith(t, 600, 2)
	ctx := context.Background()
	addMember(t, s, owner, "failure-starter", "member")
	starter := signInAs(t, s, "failure-starter", testPassword, 4).AccessToken
	c := CallerOf(t, s, starter)
	library := get[Libraries](t, s, starter, "/v1/libraries").Items[0].ID
	v := db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}
	raw, _ := json.Marshal(jobs.Request{ProfileID: c.ProfileID, Target: jobs.Target{Filter: "kind=movie"}})
	j, err := s.db.StartJob(ctx, db.NewJob{Kind: stateJobKind, Target: "test-failures", Payload: string(raw), OwnerID: c.UserID})
	if err != nil {
		t.Fatal(err)
	}
	films, err := s.db.Items(ctx, v, db.ItemQuery{Library: library, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	var edits []db.ItemEdit
	for _, it := range films.Items {
		edits = append(edits, db.ItemEdit{ItemID: it.PublicID, StateEdit: db.StateEdit{Watched: &yes, Ago: 36 * 24 * time.Hour}})
	}
	if _, _, err := s.db.SetJobStates(ctx, v, edits, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.FinishJob(ctx, j.ID, db.JobFailed, selectionFailureSummary); err != nil {
		t.Fatal(err)
	}
	path := "/v1/jobs/" + j.PublicID + "/failures"
	if page := get[Page[JobItemFailure]](t, s, starter, path+"?limit=999999999"); len(page.Items) != len(films.Items) {
		t.Fatalf("starter sees %d of %d failures", len(page.Items), len(films.Items))
	}
	// Another profile of the same account, and another account, see exactly a missing job.
	var second Profile
	call(t, s, "POST", "/v1/profiles", starter, ProfileCreate{Name: "Second"}, &second)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+second.ID+"/select", starter, ProfileSelect{}, &selected)
	for _, token := range []string{selected.AccessToken, members[1]} {
		for _, suffix := range []string{"", "?cursor=bad&limit=bad"} {
			for _, id := range []string{j.PublicID, "job_missing"} {
				w := serve(s, newRequest("GET", "/v1/jobs/"+id+"/failures"+suffix, token, nil))
				if w.Code != 404 || strings.Contains(w.Body.String(), "cursor") || strings.Contains(w.Body.String(), "limit") {
					t.Fatalf("hidden/missing job: %d %s", w.Code, w.Body)
				}
			}
		}
	}
	addMember(t, s, owner, "failure-reader", "admin")
	admin := signInAs(t, s, "failure-reader", testPassword, 5).AccessToken
	ac := CallerOf(t, s, admin)
	p, err := s.db.ProfileByID(ctx, ac.UserID, ac.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	// Hold a cursor, then change access before its continuation is read.
	first := get[Page[JobItemFailure]](t, s, admin, path+"?limit=1")
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) == 0 {
		t.Fatalf("labels: %v", err)
	}
	age := 13
	limits := []db.Restrictions{
		{AllLibraries: true, MaxRatingAge: &age},
		{AllLibraries: true, BlockedLabels: []string{labels[0].PublicID}},
		{AllLibraries: false},
	}
	for _, restriction := range limits {
		if _, err := s.db.SetProfileRestrictions(ctx, ac.UserID, p.PublicID, restriction, s.now()); err != nil {
			t.Fatal(err)
		}
		visible, err := s.db.Items(ctx, db.Viewer{UserID: ac.UserID, ProfileID: ac.ProfileID}, db.ItemQuery{Library: library, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200})
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			t.Fatal(err)
		}
		allowed := map[string]bool{}
		for _, it := range visible.Items {
			allowed[it.PublicID] = true
		}
		var got []string
		for cursor := ""; ; {
			page := get[Page[JobItemFailure]](t, s, admin, path+"?limit=3&cursor="+cursor)
			for _, f := range page.Items {
				if !allowed[f.ItemID] || f.Error.Code != "edit_too_old" {
					t.Fatalf("hidden failure or unsafe reason: %+v", f)
				}
				got = append(got, f.ItemID)
			}
			if cursor = page.NextCursor; cursor == "" {
				break
			}
		}
		if len(got) != len(allowed) || len(allowed) >= len(films.Items) {
			t.Fatalf("filtered page count %d, visible %d, all %d", len(got), len(allowed), len(films.Items))
		}
		summary := get[Job](t, s, admin, "/v1/jobs/"+j.PublicID)
		if len(allowed) == 0 {
			if summary.State != db.JobDone || summary.Error != "" {
				t.Fatalf("hidden failures left a failed summary: %+v", summary)
			}
		} else if summary.State != db.JobFailed || summary.Error != selectionFailureSummary {
			t.Fatalf("visible failures lost their summary: %+v", summary)
		}
		continued := get[Page[JobItemFailure]](t, s, admin, path+"?limit=3&cursor="+first.NextCursor)
		for _, f := range continued.Items {
			if !allowed[f.ItemID] {
				t.Fatalf("old cursor leaked %+v", f)
			}
		}
	}
}
