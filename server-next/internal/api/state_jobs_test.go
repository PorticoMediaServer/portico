package api

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// Marking a whole view watched runs as a job for the profile that asked: every title that
// profile sees in the view is marked, nothing of another profile changes, and running the job
// again (as after a restart) does not undo what the person changed since.
func TestMarkAWholeViewWatched(t *testing.T) {
	s, token, limited := seeded(t)
	films := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	view := url.Values{"library": {films}, "kind": {"movie"}}.Encode()
	list := "/v1/items?" + view + "&limit=200&total=true"
	seen := get[ItemPage](t, s, limited, list)
	if *seen.Total == 0 || *seen.Total >= 90 {
		t.Fatalf("the limited profile sees %d of 90 films", *seen.Total)
	}

	start := JobStart{Kind: stateJobKind, Target: JobTarget{Filter: view}, Params: JSON(`{"watched": true}`)}
	r := newRequest("POST", "/v1/jobs", limited, start)
	r.Header.Set("Idempotency-Key", "mark-films")
	w := serve(s, r)
	var job Job
	if w.Code != 202 || jsonInto(w, &job) != nil {
		t.Fatalf("starting the job answered %d: %s", w.Code, w.Body)
	}
	if job = waitForJob(t, s, limited, job.ID); job.State != db.JobDone || job.Progress == nil || job.Progress.Done != int64(*seen.Total) {
		t.Fatalf("the job ended %s with %+v: %s", job.State, job.Progress, job.Error)
	}
	if left := get[ItemPage](t, s, limited, list+"&watched=false"); *left.Total != 0 {
		t.Fatalf("%d films in the view are still unwatched", *left.Total)
	}
	if mine := get[ItemPage](t, s, token, "/v1/items?"+view+"&total=true&watched=true"); *mine.Total != 0 {
		t.Fatalf("another profile's films were marked: %d", *mine.Total)
	}

	// The person unmarks one film; the job is then run again from the start, as after a
	// restart. The film stays as the person left it.
	film := seen.Items[0].ID
	status(t, s, "PUT", "/v1/items/"+film+"/state", limited, map[string]any{"watched": false})
	stored, err := s.db.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var again jobs.Request
	if err := json.Unmarshal([]byte(stored.Payload), &again); err != nil {
		t.Fatal(err)
	}
	again.UserID = stored.OwnerID
	if err := s.runStateJob(context.Background(), jobs.Work{Job: stored, Request: again, Progress: func(int64, int64) {}}); err != nil {
		t.Fatal(err)
	}
	if get[Item](t, s, limited, "/v1/items/"+film).State.Watched {
		t.Fatal("running the job again undid what the person changed since")
	}

	// Music has play counts, not watched: every album is left as it was, and the profile is
	// told why in words that name no title.
	var music string
	for _, lib := range get[Libraries](t, s, token, "/v1/libraries").Items {
		if lib.Kind == db.LibraryMusic {
			music = lib.ID
		}
	}
	albums := url.Values{"library": {music}, "kind": {"album"}}.Encode()
	var refused Job
	call(t, s, "POST", "/v1/jobs", token, JobStart{Kind: stateJobKind, Target: JobTarget{Filter: albums}, Params: JSON(`{"watched": true}`)}, &refused)
	if refused = waitForJob(t, s, token, refused.ID); refused.State != db.JobFailed || !strings.Contains(refused.Error, "titles were left as they were") ||
		strings.Contains(refused.Error, "The first was") {
		t.Fatalf("marking albums watched ended %s: %q", refused.State, refused.Error)
	}

	for name, bad := range map[string]JobStart{
		"no state":  {Kind: stateJobKind, Target: JobTarget{Filter: view}},
		"rating 0":  {Kind: stateJobKind, Target: JobTarget{Filter: view}, Params: JSON(`{"rating": 0}`)},
		"no view":   {Kind: stateJobKind, Target: JobTarget{Library: films}, Params: JSON(`{"watched": true}`)},
		"bad query": {Kind: stateJobKind, Target: JobTarget{Filter: "kind=spaceship"}, Params: JSON(`{"watched": true}`)},
	} {
		if code := status(t, s, "POST", "/v1/jobs", limited, bad); code != 400 {
			t.Errorf("%s: answered %d", name, code)
		}
	}
}

// A second, different action on the same view, asked for while the first is still waiting or
// running, must happen too.
func TestASecondActionOnTheSameViewIsNotSwallowed(t *testing.T) {
	s, _, limited := seeded(t)
	films := get[Libraries](t, s, limited, "/v1/libraries").Items[0].ID
	view := url.Values{"library": {films}, "kind": {"movie"}}.Encode()

	// Something else holds the set_state queue, so the first request is still waiting when
	// the second arrives: what a large view does for minutes.
	release := make(chan struct{})
	s.jobs.Register(jobs.Kind{Name: "hold", Lane: stateJobKind, Run: func(ctx context.Context, _ jobs.Work) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	}})
	if _, err := s.jobs.Start(context.Background(), "hold", jobs.Request{Target: jobs.Target{View: "hold"}}); err != nil {
		t.Fatal(err)
	}

	var watched, favourite Job
	call(t, s, "POST", "/v1/jobs", limited, JobStart{Kind: stateJobKind, Target: JobTarget{Filter: view}, Params: JSON(`{"watched": true}`)}, &watched)
	call(t, s, "POST", "/v1/jobs", limited, JobStart{Kind: stateJobKind, Target: JobTarget{Filter: view}, Params: JSON(`{"favorite": true}`)}, &favourite)
	close(release)
	if watched.ID == "" || favourite.ID == "" {
		t.Fatalf("the jobs were not started: %+v %+v", watched, favourite)
	}
	waitForJob(t, s, limited, watched.ID)
	waitForJob(t, s, limited, favourite.ID)

	list := "/v1/items?" + view + "&total=true&limit=1"
	if left := get[ItemPage](t, s, limited, list+"&watched=false"); *left.Total != 0 {
		t.Errorf("%d films were not marked watched", *left.Total)
	}
	if left := get[ItemPage](t, s, limited, list+"&favorite=false"); *left.Total != 0 {
		t.Errorf("the second request answered 202 (job %s, the first was %s) and %d films were never made favourites", favourite.ID, watched.ID, *left.Total)
	}
}

// The view is "the query of the list that shows it". A list opened at a position carries
// at=N, and the job must still cover the whole view (or refuse the request), as it does for
// sort, cursor and limit.
func TestAViewOpenedAtAPositionIsStillTheWholeView(t *testing.T) {
	s, _, limited := seeded(t)
	films := get[Libraries](t, s, limited, "/v1/libraries").Items[0].ID
	view := url.Values{"library": {films}, "kind": {"movie"}}.Encode()

	r := newRequest("POST", "/v1/jobs", limited, JobStart{Kind: stateJobKind, Target: JobTarget{Filter: view + "&at=5"}, Params: JSON(`{"watched": true}`)})
	w := serve(s, r)
	if w.Code == 400 {
		return // refusing it at the start is as good
	}
	var job Job
	if w.Code != 202 || jsonInto(w, &job) != nil {
		t.Fatalf("starting the job answered %d: %s", w.Code, w.Body)
	}
	if job = waitForJob(t, s, limited, job.ID); job.State != db.JobDone {
		t.Fatalf("the job ended %s: %s", job.State, job.Error)
	}
	if left := get[ItemPage](t, s, limited, "/v1/items?"+view+"&total=true&limit=1&watched=false"); *left.Total != 0 {
		t.Errorf("the job ended done, and %d films in the view are still unwatched", *left.Total)
	}
}

// A page that cannot be read must not end the job as done.
// A closed database stands in here for any failed read.
func TestAFailedReadDoesNotEndTheJobAsDone(t *testing.T) {
	s, _, limited := seeded(t)
	films := get[Libraries](t, s, limited, "/v1/libraries").Items[0].ID
	view := url.Values{"library": {films}, "kind": {"movie"}}.Encode()
	me := CallerOf(t, s, limited)

	req := jobs.Request{Target: jobs.Target{Filter: view}, Params: []byte(`{"watched": true}`), UserID: me.UserID, ProfileID: me.ProfileID}
	stopServer(s)
	closeDB(s.db)
	err := s.runStateJob(context.Background(), jobs.Work{Job: db.Job{CreatedAt: 1}, Request: req, Progress: func(int64, int64) {}})
	if err == nil {
		t.Fatal("the view could not be read and the job would end as done")
	}
}

// CallerOf reads who a token is.
func CallerOf(t *testing.T, s *Server, token string) Caller {
	t.Helper()
	claims, err := s.tokens.CheckAccess(token, s.now())
	if err != nil {
		t.Fatal(err)
	}
	return Caller{UserID: claims.UserID, ProfileID: claims.ProfileID}
}

// A write of a set_state job changes about stateWrite titles, counting the episodes a marked
// show holds: a page of shows is split, a show larger than that is a write of its own, and
// favourites on shows or a page of films are one write.
func TestStateWritesAreBoundedByTitlesWritten(t *testing.T) {
	yes := true
	shows := []db.Item{{Kind: db.KindShow, Leaves: 100}, {Kind: db.KindShow, Leaves: 150}, {Kind: db.KindShow, Leaves: 100},
		{Kind: db.KindShow, Leaves: stateWrite + 200}, {Kind: db.KindShow, Leaves: 10}}
	sizes := func(writes [][]db.ItemEdit) []int {
		var out []int
		for _, w := range writes {
			out = append(out, len(w))
		}
		return out
	}
	if got := sizes(stateWrites(shows, db.StateEdit{Watched: &yes})); !slices.Equal(got, []int{2, 1, 1, 1}) {
		t.Errorf("shows marked watched are written %v at a time", got)
	}
	if got := sizes(stateWrites(shows, db.StateEdit{Favorite: &yes})); !slices.Equal(got, []int{5}) {
		t.Errorf("shows made favourites are written %v at a time", got)
	}
	films := make([]db.Item, statePage)
	for n := range films {
		films[n].Kind = db.KindMovie
	}
	if got := sizes(stateWrites(films, db.StateEdit{Watched: &yes})); !slices.Equal(got, []int{statePage}) {
		t.Errorf("a page of films is written %v at a time", got)
	}
}
