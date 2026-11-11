package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// The owner starts a scan through the one creating route; a repeat of the request with its
// Idempotency-Key starts nothing; a member is refused the owner's kinds but starts, reads and
// stops a job on their own view, which nobody else's account sees.
func TestStartingReadingAndCancellingJobs(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	root := filepath.Join(t.TempDir(), "films")
	writeFile(t, filepath.Join(root, "Heat (1995).mkv"), 1)
	var lib AdminLibrary
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, LibraryCreate{Name: "Films", Kind: "movies", Folders: []string{root}}, &lib); w.Code != 200 {
		t.Fatalf("creating the library answered %d: %s", w.Code, w.Body)
	}
	scanNow(t, s, owner, lib.ID)

	start := JobStart{Kind: "scan", Target: JobTarget{Library: lib.ID}, Params: JSON(`{"allFiles":true}`)}
	send := func(token, key string, body JobStart) (int, Job) {
		t.Helper()
		r := newRequest("POST", "/v1/jobs", token, body)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := serve(s, r)
		var job Job
		jsonInto(w, &job)
		return w.Code, job
	}
	code, job := send(owner, "one", start)
	if code != 202 || job.Kind != "scan" {
		t.Fatalf("starting a scan answered %d %+v", code, job)
	}
	if done := waitForJob(t, s, owner, job.ID); done.State != "done" {
		t.Fatalf("the scan ended %s: %s", done.State, done.Error)
	}
	// The same request again is the job that already ran, not a new scan.
	if code, again := send(owner, "one", start); code != 202 || again.ID != job.ID || again.State != "done" {
		t.Fatalf("the repeat answered %d %+v", code, again)
	}
	other := start
	other.Params = nil
	if code, _ := send(owner, "one", other); code != 422 {
		t.Errorf("the key with another request answered %d; want 422", code)
	}
	for want, bad := range map[int]JobStart{
		400: {Kind: "polish", Target: JobTarget{Library: lib.ID}},
		404: {Kind: "scan", Target: JobTarget{Library: "lib_nowhere"}},
	} {
		if code, _ := send(owner, "", bad); code != want {
			t.Errorf("%+v answered %d; want %d", bad, code, want)
		}
	}
	for _, bad := range []JobStart{{Kind: "delete_library", Target: JobTarget{Library: lib.ID}}, {Kind: "scan"},
		{Kind: "scan", Target: JobTarget{Library: lib.ID, View: "view_1"}}} {
		if code, _ := send(owner, "", bad); code != 400 {
			t.Errorf("%+v answered %d; want 400", bad, code)
		}
	}

	member := memberToken(t, s, owner)
	if code, _ := send(member, "", start); code != 403 {
		t.Errorf("a member starting a scan answered %d; want 403", code)
	}
	if code := status(t, s, "GET", "/v1/jobs/"+job.ID, member, nil); code != 404 {
		t.Errorf("a member reading the owner's job answered %d; want 404", code)
	}

	// A kind any profile may start, as another area would register it.
	running := make(chan struct{})
	s.jobs.Register(jobs.Kind{Name: "mark_view", Access: jobs.AnyProfile, Run: func(ctx context.Context, work jobs.Work) error {
		if work.ProfileID == 0 || work.Target.View != "view_1" {
			t.Errorf("the job ran with %+v", work.Request)
		}
		close(running)
		<-ctx.Done()
		return ctx.Err()
	}})
	code, mine := send(member, "", JobStart{Kind: "mark_view", Target: JobTarget{View: "view_1"}})
	if code != 202 {
		t.Fatalf("a member's own job answered %d", code)
	}
	<-running
	// Another profile of the same account neither sees the job nor stops it. The owner sees it.
	var second Profile
	if w := call(t, s, "POST", "/v1/profiles", member, ProfileCreate{Name: "Second"}, &second); w.Code != 200 {
		t.Fatalf("adding a profile answered %d: %s", w.Code, w.Body)
	}
	var in SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "sam", Password: testPassword, Device: device(8)}, &in); w.Code != 200 {
		t.Fatalf("signing in again answered %d", w.Code)
	}
	var onSecond Session
	if w := call(t, s, "POST", "/v1/profiles/"+second.ID+"/select", in.Session.AccessToken, ProfileSelect{}, &onSecond); w.Code != 200 {
		t.Fatalf("choosing the second profile answered %d", w.Code)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if code := status(t, s, method, "/v1/jobs/"+mine.ID, onSecond.AccessToken, nil); code != 404 {
			t.Errorf("%s of a job by another profile of the account answered %d; want 404", method, code)
		}
	}
	if code := status(t, s, "GET", "/v1/jobs/"+mine.ID, owner, nil); code != 200 {
		t.Errorf("the owner reading a member's job answered %d", code)
	}
	if code := status(t, s, "DELETE", "/v1/jobs/"+mine.ID, member, nil); code != 204 {
		t.Fatalf("stopping the job answered %d", code)
	}
	if ended := waitForJob(t, s, member, mine.ID); ended.State != "cancelled" {
		t.Fatalf("the stopped job is %s", ended.State)
	}
}
