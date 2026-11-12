package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// The owner lists the tasks running and those that ended in the last 30 days with their
// reason, and runs a failed one again; a person's own task and one that did not fail are not
// run again from here. The tidy-up removes tasks that ended over 30 days ago. An
// administrator and a member read none of it.
func TestOwnersTasks(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	addMember(t, s, owner, "max", "member")
	for _, token := range []string{signInAs(t, s, "ada", testPassword, 1).AccessToken, signInAs(t, s, "max", testPassword, 2).AccessToken} {
		if code := status(t, s, "GET", "/v1/admin/jobs", token, nil); code != 403 {
			t.Errorf("an administrator or member read the tasks: %d", code)
		}
	}
	folder := filepath.Join(t.TempDir(), "Films")
	os.MkdirAll(folder, 0o755)
	var films AdminLibrary
	// A books library, which uses local metadata only: its scan starts no lookup, so the tasks
	// listed are the test's own and the scan.
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, LibraryCreate{Name: "Films", Kind: "books", Folders: []string{folder}}, &films); w.Code != 200 {
		t.Fatalf("adding a library answered %d: %s", w.Code, w.Body)
	}
	var failing atomic.Bool
	failing.Store(true)
	work := func(context.Context, jobs.Work) error {
		if failing.Load() {
			return errors.New("the test's work failed")
		}
		return nil
	}
	s.jobs.Register(jobs.Kind{Name: "test_owners", Access: jobs.Owner, Run: work})
	s.jobs.Register(jobs.Kind{Name: "test_mine", Access: jobs.AnyProfile, Run: work})
	start := func(kind string, target JobTarget) string {
		t.Helper()
		var job Job
		if w := call(t, s, "POST", "/v1/jobs", owner, JobStart{Kind: kind, Target: target}, &job); w.Code != 202 {
			t.Fatalf("starting %s answered %d: %s", kind, w.Code, w.Body)
		}
		return job.ID
	}
	failed := start("test_owners", JobTarget{Library: films.ID})
	waitForJob(t, s, owner, failed)
	mine := start("test_mine", JobTarget{Items: []string{"itm_test"}})
	waitForJob(t, s, owner, mine)
	scanNow(t, s, owner, films.ID)

	var list Page[AdminJob]
	if w := call(t, s, "GET", "/v1/admin/jobs", owner, nil, &list); w.Code != 200 || len(list.Items) != 3 {
		t.Fatalf("the tasks answered %d: %s", w.Code, w.Body)
	}
	byID := map[string]AdminJob{}
	for _, j := range list.Items {
		byID[j.ID] = j
	}
	if j := byID[failed]; j.State != "failed" || !strings.Contains(j.Error, "the test's work failed") || !j.CanRetry || j.LibraryName != "Films" || j.FinishedAt == nil {
		t.Errorf("the failed task reads %+v", j)
	}
	if byID[mine].CanRetry {
		t.Errorf("a person's own task is offered Run again")
	}
	var scan AdminJob
	for _, j := range list.Items {
		if j.Kind == "scan" {
			scan = j
		}
	}
	if scan.State != "done" || scan.CanRetry {
		t.Errorf("the scan reads %+v", scan)
	}
	for id, code := range map[string]string{scan.ID: "job_not_failed", mine: "job_not_retryable"} {
		if w := call(t, s, "POST", "/v1/admin/jobs/"+id+"/retry", owner, nil, nil); w.Code != 409 || !strings.Contains(w.Body.String(), code) {
			t.Errorf("running %s again answered %d: %s", id, w.Code, w.Body)
		}
	}
	failing.Store(false)
	var again Job
	if w := call(t, s, "POST", "/v1/admin/jobs/"+failed+"/retry", owner, nil, &again); w.Code != 202 || again.ID != failed {
		t.Fatalf("running the failed task again answered %d: %s", w.Code, w.Body)
	}
	if done := waitForJob(t, s, owner, failed); done.State != "done" {
		t.Errorf("the task run again ended %s", done.State)
	}
	if code := status(t, s, "GET", "/v1/admin/jobs?cursor=nonsense", owner, nil); code < 400 || code >= 500 {
		t.Errorf("a made-up cursor answered %d", code)
	}

	// The ended tasks page a few at a time, the latest end first, without repeats.
	ctx := context.Background()
	first, _ := s.db.EndedJobs(ctx, time.Now().Add(-time.Hour), 0, 0, 2)
	rest, _ := s.db.EndedJobs(ctx, time.Now().Add(-time.Hour), first[1].FinishedAt, first[1].ID, 10)
	if len(first) != 2 || len(rest) != 1 || rest[0].ID == first[0].ID || rest[0].ID == first[1].ID || first[0].FinishedAt < first[1].FinishedAt {
		t.Errorf("paging the ended tasks: %+v then %+v", first, rest)
	}
	// Thirty-one days on, the tidy-up removes them.
	if n, err := s.db.RemoveEndedJobs(ctx, time.Now().Add(31*24*time.Hour-jobsKept)); err != nil || n != 3 {
		t.Errorf("the tidy-up removed %d: %v", n, err)
	}
	if call(t, s, "GET", "/v1/admin/jobs", owner, nil, &list); len(list.Items) != 0 {
		t.Errorf("tasks left after the tidy-up: %+v", list.Items)
	}
}
