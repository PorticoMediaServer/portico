package api

import (
	"context"
	"encoding/json"
	"net/url"
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
	if job = waitForJob(t, s, limited, job.ID); job.State != db.JobDone {
		t.Fatalf("the job ended %s: %s", job.State, job.Error)
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

	for name, bad := range map[string]JobStart{
		"no state":  {Kind: stateJobKind, Target: JobTarget{Filter: view}},
		"no view":   {Kind: stateJobKind, Target: JobTarget{Library: films}, Params: JSON(`{"watched": true}`)},
		"bad query": {Kind: stateJobKind, Target: JobTarget{Filter: "kind=spaceship"}, Params: JSON(`{"watched": true}`)},
	} {
		if code := status(t, s, "POST", "/v1/jobs", limited, bad); code != 400 {
			t.Errorf("%s: answered %d", name, code)
		}
	}
}
