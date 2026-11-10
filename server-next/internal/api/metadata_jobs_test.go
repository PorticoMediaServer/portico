package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// startJob starts a job as the owner and waits for it to end.
func (sh *shelf) startJob(body JobStart) Job {
	sh.t.Helper()
	var job Job
	sh.do("POST", "/v1/jobs", body, &job, 202)
	return waitForJob(sh.t, sh.s, sh.owner, job.ID)
}

func (sh *shelf) item(id string) Item {
	sh.t.Helper()
	var item Item
	sh.do("GET", "/v1/items/"+id, nil, &item, 200)
	return item
}

func genres(item Item) (out []string) {
	for _, tag := range item.Tags {
		if tag.Kind == "genre" {
			out = append(out, tag.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Bulk edit adds and removes list entries and sets a field on every title of a selection and
// locks them; a refresh of the library keeps the edit; a provider that is down fails the job
// with a sentence; a scan's new film is looked up by itself; an admin whose profile may not
// see a title cannot start work on it.
func TestMetadataJobs(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv", "Heat (1995).mkv")
	var films ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &films, 200)
	ids := []string{films.Items[0].ID, films.Items[1].ID}

	rating := "NC-17"
	params, _ := json.Marshal(BulkEdit{ContentRating: &rating, Add: map[string][]string{"genres": {"Heist"}, "labels": {"Late night"}},
		Remove: map[string][]string{"genres": {"action"}}})
	if job := sh.startJob(JobStart{Kind: "edit_items", Target: JobTarget{Items: ids}, Params: params}); job.State != "done" {
		t.Fatalf("bulk edit: %+v", job)
	}
	for _, id := range ids {
		item := sh.item(id)
		if item.ContentRating != "NC-17" || !slices.Contains(genres(item), "Heist") || slices.Contains(genres(item), "Action") || len(genres(item)) < 2 {
			t.Fatalf("after bulk edit %s: rating %q, genres %v", item.Title, item.ContentRating, genres(item))
		}
	}
	bad, _ := json.Marshal(BulkEdit{Add: map[string][]string{"moods": {"Cosy"}}})
	sh.do("POST", "/v1/jobs", JobStart{Kind: "edit_items", Target: JobTarget{Items: ids}, Params: bad}, nil, 400)

	// A refresh of the whole library asks about both films again and keeps what was edited.
	asked := sh.tmdb.Requests.Load()
	if job := sh.startJob(JobStart{Kind: "refresh_library", Target: JobTarget{Library: sh.library}}); job.State != "done" {
		t.Fatalf("refresh: %+v", job)
	}
	if sh.tmdb.Requests.Load()-asked < 2 || sh.item(ids[0]).ContentRating != "NC-17" {
		t.Fatalf("the refresh asked %d times, or lost the edit", sh.tmdb.Requests.Load()-asked)
	}
	sh.tmdb.Down.Store(true)
	if job := sh.startJob(JobStart{Kind: "refresh_items", Target: JobTarget{Items: ids}}); job.State != "failed" || job.Error == "" {
		t.Fatalf("a refresh the provider did not answer: %+v", job)
	}
	sh.tmdb.Down.Store(false)

	// A new film found by a scan is looked up without anyone asking.
	if err := os.WriteFile(filepath.Join(sh.root, "Zzyzx Two (2012).mkv"), make([]byte, 77), 0o644); err != nil {
		t.Fatal(err)
	}
	sh.startJob(JobStart{Kind: "scan", Target: JobTarget{Library: sh.library}})
	var unmatched []UnmatchedEntry
	for range 500 {
		if unmatched = sh.unmatched(); len(unmatched) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(unmatched) != 1 || unmatched[0].Title != "Zzyzx Two" || unmatched[0].Reason != "no_results" {
		t.Fatalf("after a scan, Unmatched items: %+v", unmatched)
	}

	if w := call(t, sh.s, "POST", "/v1/jobs", sh.limitedAdmin, JobStart{Kind: "refresh_items", Target: JobTarget{Items: ids}}, nil); w.Code != 404 {
		t.Fatalf("an admin limited to 13 and under started work on R-rated films: %d", w.Code)
	}
	if w := call(t, sh.s, "POST", "/v1/jobs", sh.member, JobStart{Kind: "match_library", Target: JobTarget{Library: sh.library}}, nil); w.Code != 403 {
		t.Fatalf("a member started matching a library: %d", w.Code)
	}
}
