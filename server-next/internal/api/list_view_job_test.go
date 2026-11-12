package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// Adding a whole view to a playlist adds exactly the titles the adding profile may see, in
// the view's own order, and says how many; taken up again after a restart it adds nothing
// twice; and past what a list holds it adds up to that and says how many were left out.
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
	if job.State != db.JobDone || !strings.HasPrefix(job.Error, "Added "+strconv.Itoa(*seen.Total)+".") {
		t.Fatalf("the job ended %s: %q", job.State, job.Error)
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

	// Past what a list holds: filled to it, and the rest is said.
	defer func(was int) { listViewMax = was }(listViewMax)
	listViewMax = 5
	var small List
	call(t, s, "POST", "/v1/lists", limited, ListCreate{Kind: db.ListPlaylist, Name: "Five"}, &small)
	start.Params = JSON(`{"list": "` + small.ID + `"}`)
	call(t, s, "POST", "/v1/jobs", limited, start, &job)
	job = waitForJob(t, s, limited, job.ID)
	if job.State != db.JobDone || !strings.Contains(job.Error, "Added 5.") || !strings.Contains(job.Error, strconv.Itoa(*seen.Total-5)+" were left out") {
		t.Fatalf("past the cap the job ended %s: %q", job.State, job.Error)
	}

	// A list the profile may not change is refused before anything is stored.
	var theirs List
	call(t, s, "POST", "/v1/lists", token, ListCreate{Kind: db.ListPlaylist, Name: "Not yours"}, &theirs)
	start.Params = JSON(`{"list": "` + theirs.ID + `"}`)
	if code := status(t, s, "POST", "/v1/jobs", limited, start); code < 400 {
		t.Fatalf("adding to someone else's playlist answered %d", code)
	}
}

