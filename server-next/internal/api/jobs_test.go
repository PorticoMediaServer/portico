package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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

// A job does not outlive its starter's role: an admin's job still waiting when the admin is
// made a member is cancelled when its turn comes and never runs. And a failed job's reason
// names a title to the owner alone: an admin is given the count, or a plain sentence.
func TestAJobDoesNotOutliveARoleAndItsReasonNamesTitlesToTheOwnerOnly(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ann := addMember(t, s, owner, "ann", "admin")
	admin := signInAs(t, s, "ann", testPassword, 5).AccessToken

	release, first := make(chan struct{}), make(chan struct{})
	ranLater := false
	s.jobs.Register(jobs.Kind{Name: "first", Access: jobs.Admins, Lane: "one", Run: func(context.Context, jobs.Work) error {
		close(first)
		<-release
		return nil
	}})
	s.jobs.Register(jobs.Kind{Name: "later", Access: jobs.Admins, Lane: "one", Run: func(context.Context, jobs.Work) error {
		ranLater = true
		return nil
	}})
	s.jobs.Register(jobs.Kind{Name: "counted", Access: jobs.Admins, Run: func(context.Context, jobs.Work) error {
		return &jobs.Failure{Shown: "1 title could not be matched.", Detail: "Secret Film (1999) could not be matched."}
	}})
	s.jobs.Register(jobs.Kind{Name: "plain", Access: jobs.Admins, Run: func(context.Context, jobs.Work) error {
		return errors.New("Secret Film (1999) has no file")
	}})
	start := func(kind string) Job {
		t.Helper()
		var job Job
		if w := call(t, s, "POST", "/v1/jobs", admin, JobStart{Kind: kind, Target: JobTarget{Library: "lib_a"}}, &job); w.Code != 202 {
			t.Fatalf("starting %s answered %d: %s", kind, w.Code, w.Body)
		}
		return job
	}

	// What a failed job says, to the admin who started it and to the owner.
	for kind, forAdmin := range map[string]string{"counted": "1 title could not be matched.", "plain": "This did not finish. The owner can see why."} {
		job := start(kind)
		if seen := waitForJob(t, s, admin, job.ID); seen.State != "failed" || seen.Error != forAdmin {
			t.Errorf("%s: the admin was told %q (%s); want %q", kind, seen.Error, seen.State, forAdmin)
		}
		if seen := waitForJob(t, s, owner, job.ID); !strings.Contains(seen.Error, "Secret Film (1999)") {
			t.Errorf("%s: the owner was told %q, without the title", kind, seen.Error)
		}
	}

	// A job waits behind another; meanwhile its starter stops being an admin.
	start("first")
	<-first
	waiting := start("later")
	member := "member"
	if code := status(t, s, "PATCH", "/v1/admin/members/"+ann.ID, owner, MemberEdit{Role: &member}); code != 200 {
		t.Fatalf("demoting the admin answered %d", code)
	}
	close(release)
	if ended := waitForJob(t, s, owner, waiting.ID); ended.State != "cancelled" || ranLater {
		t.Fatalf("the demoted admin's job ended %s, ran: %v; want cancelled and never run", ended.State, ranLater)
	}
}
