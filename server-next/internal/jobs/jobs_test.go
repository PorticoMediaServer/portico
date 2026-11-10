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
	quick := func(context.Context, Work) error { return nil }
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
	wait(t, d, mine.PublicID, db.JobDone)
	wait(t, d, yours.PublicID, db.JobDone)
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
}
