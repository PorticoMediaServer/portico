package jobs

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func wait(t *testing.T, d *db.DB, id, state string) db.Job {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		job, err := d.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == state {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is %s; want %s", id, job.State, state)
		}
	}
}

// Jobs in one lane run one at a time in order, a job in another lane runs beside them, asking
// twice joins, and a job that was running when the server stopped is run again at the next start.
func TestLanesJoiningAndResume(t *testing.T) {
	ctx, stopServer := context.WithCancel(context.Background())
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	release := make(chan struct{})
	started := make(chan string, 8)
	held := func(ctx context.Context, work Work) error {
		started <- work.Job.Kind + " " + work.Target.Library
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ranAsNobody := false
	quick := func(_ context.Context, work Work) error {
		ranAsNobody = ranAsNobody || work.ProfileID != 0
		return nil
	}
	r := New(ctx, d, log)
	r.Register(Kind{Name: "slow", Access: Admins, Lane: "library", Run: held})
	r.Register(Kind{Name: "also_slow", Access: Admins, Lane: "library", Run: held})
	r.Register(Kind{Name: "quick", Access: AnyProfile, Run: quick})

	first, err := r.Start(ctx, "slow", Request{Target: Target{Library: "lib_a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "slow lib_a" {
		t.Fatal(got)
	}
	joined, _ := r.Start(ctx, "slow", Request{Target: Target{Library: "lib_a"}})
	second, _ := r.Start(ctx, "also_slow", Request{Target: Target{Library: "lib_a"}})
	if joined.PublicID != first.PublicID || second.PublicID == first.PublicID {
		t.Fatalf("asking twice made %s beside %s; another kind made %s", joined.PublicID, first.PublicID, second.PublicID)
	}
	// The other lane is not held up, and two profiles' jobs on one view are two jobs.
	mine, _ := r.Start(ctx, "quick", Request{Target: Target{View: "view_1"}, ProfileID: 1})
	yours, err := r.Start(ctx, "quick", Request{Target: Target{View: "view_1"}, ProfileID: 2})
	if err != nil || mine.PublicID == yours.PublicID {
		t.Fatalf("two profiles: %v %s %s", err, mine.PublicID, yours.PublicID)
	}
	// Neither profile exists here, so neither job is run as anybody: both end cancelled,
	// at once, although the other lane is busy.
	wait(t, d, mine.PublicID, db.JobCancelled)
	wait(t, d, yours.PublicID, db.JobCancelled)
	if job := wait(t, d, second.PublicID, db.JobWaiting); job.StartedAt != 0 {
		t.Fatal("the second job in the lane started before the first ended")
	}
	if _, err := r.Start(ctx, "slow", Request{Target: Target{Library: "a", View: "b"}}); err == nil {
		t.Fatal("two targets were accepted")
	}

	// The server stops with one job running and one waiting. The next start runs both.
	stopServer()
	time.Sleep(50 * time.Millisecond)
	ctx = context.Background()
	again := New(ctx, d, log)
	again.Register(Kind{Name: "slow", Lane: "library", Run: quick})
	again.Register(Kind{Name: "also_slow", Lane: "library", Run: quick})
	if err := again.Resume(); err != nil {
		t.Fatal(err)
	}
	wait(t, d, first.PublicID, db.JobDone)
	wait(t, d, second.PublicID, db.JobDone)
	if ranAsNobody {
		t.Fatal("a job of a profile that is not there was run")
	}
}

// A job that ends says how far it really got: the last count is written although the one
// before it was written a moment ago.
func TestAJobThatEndsShowsItsLastCount(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := New(ctx, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Register(Kind{Name: "count", Access: Admins, Run: func(_ context.Context, work Work) error {
		for done := int64(100); done <= 229; done += 43 {
			work.Progress(done, 229)
		}
		work.Progress(229, 229)
		return nil
	}})
	job, err := r.Start(ctx, "count", Request{Target: Target{Library: "lib_a"}})
	if err != nil {
		t.Fatal(err)
	}
	if done := wait(t, d, job.PublicID, db.JobDone); done.Done != 229 || done.Total != 229 {
		t.Fatalf("the finished job shows %d of %d; want 229 of 229", done.Done, done.Total)
	}
}

// A job that was cut short when the server died is run again once. Cut short a second time it
// ends as failed and is left alone, so a job that kills the server cannot do so at every start.
func TestAJobCutShortTwiceIsNotRunAgain(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	started := make(chan struct{}, 4)
	forever := make(chan struct{})
	defer close(forever)
	// Each runner stands for one life of the server that dies with the job running: nothing
	// stops it in good order, the next one just starts.
	life := func() *Runner {
		r := New(ctx, d, log)
		r.Register(Kind{Name: "deadly", Access: Admins, Run: func(context.Context, Work) error {
			started <- struct{}{}
			<-forever
			return nil
		}})
		return r
	}
	job, err := life().Start(ctx, "deadly", Request{Target: Target{Library: "lib_a"}})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := life().Resume(); err != nil {
		t.Fatal(err)
	}
	<-started // run again, once
	if err := life().Resume(); err != nil {
		t.Fatal(err)
	}
	if ended := wait(t, d, job.PublicID, db.JobFailed); ended.Error != Interrupted {
		t.Fatalf("the job ended with %q", ended.Error)
	}
	select {
	case <-started:
		t.Fatal("the job was started a third time")
	case <-time.After(200 * time.Millisecond):
	}
}

// A fault in a kind's code ends that job as failed. The server carries on, and so does the
// next job in the same lane.
func TestAPanicEndsTheJobAndNothingElse(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := New(ctx, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Register(Kind{Name: "faulty", Access: Admins, Lane: "one", Run: func(context.Context, Work) error {
		var nothing map[string]int
		nothing["x"] = 1
		return nil
	}})
	r.Register(Kind{Name: "fine", Access: Admins, Lane: "one", Run: func(context.Context, Work) error { return nil }})
	faulty, err := r.Start(ctx, "faulty", Request{Target: Target{Library: "lib_a"}})
	if err != nil {
		t.Fatal(err)
	}
	fine, err := r.Start(ctx, "fine", Request{Target: Target{Library: "lib_a"}})
	if err != nil {
		t.Fatal(err)
	}
	if ended := wait(t, d, faulty.PublicID, db.JobFailed); ended.Error == "" {
		t.Fatal("the job that panicked gives no reason")
	}
	wait(t, d, fine.PublicID, db.JobDone)
}
