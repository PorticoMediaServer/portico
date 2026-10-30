package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withoutTimers leaves the scan loop nothing to run on but its own
// continuation: the in-flight poll and the shared commit floor are both pushed
// far past the life of a test.
func withoutTimers(t *testing.T) {
	t.Helper()
	poll, floor := quantumPoll, workerloop.MinSignalInterval
	quantumPoll, workerloop.MinSignalInterval = time.Hour, time.Hour
	t.Cleanup(func() { quantumPoll, workerloop.MinSignalInterval = poll, floor })
}

func TestScanLoopContinuesAsSoonAsAQuantumAdvances(t *testing.T) {
	withoutTimers(t)
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprint(i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, cat, assets.Probe{})
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(20 * time.Second)
	for {
		current, err := s.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "complete" {
			return
		}
		if current.Status == "failed" {
			t.Fatalf("scan failed: %+v", current)
		}
		select {
		case <-deadline:
			t.Fatalf("scan waited on a timer between steps: %+v", current)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestScanLoopDoesNotRespinAQuantumThatDidNotAdvance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	withoutTimers(t)
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	lib, err := cat.Create("Films", "movie", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, cat, assets.Probe{})
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The loop admits the job; the quantum itself is then refused, so it ends
	// without moving the run.
	var calls atomic.Int64
	s.Admission = func(context.Context, *sql.Tx, string) (bool, error) { return calls.Add(1)%2 == 1, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done
	if n := calls.Load(); n != 2 {
		t.Fatalf("a quantum that made no progress was started %d times", n/2)
	}
	if j, err := s.Get(job.ID); err != nil || j.Status != "queued" {
		t.Fatalf("refused quantum changed the job: %+v %v", j, err)
	}
}

func TestQuantumReportsWhetherTheRunAdvanced(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	lib, err := cat.Create("Films", "movie", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, cat, assets.Probe{})
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !s.processQuantum(context.Background(), job.ID, lib.ID) {
		t.Fatal("a committed inventory page was not reported as progress")
	}
	if err = s.Control(context.Background(), job.ID, "pause", nil); err != nil {
		t.Fatal(err)
	}
	if s.processQuantum(context.Background(), job.ID, lib.ID) {
		t.Fatal("a paused job was reported as progress")
	}
}

// While a foreground request is in flight the scan does not continue at once:
// after every step that advanced it gives way for the bounded pause every bulk
// loop uses, and it still finishes. With nothing in the foreground it is not
// held back at all.
func TestScanLoopGivesWayToForegroundWorkBetweenSteps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	withoutTimers(t)
	// Empty folders are listed a batch to a step, so the tree is sized for
	// at least `steps` listing steps.
	const steps = 24
	const directories = steps * storage.MaxInventoryBatch
	scan := func(t *testing.T) (elapsed time.Duration) {
		t.Helper()
		root := t.TempDir()
		for i := 0; i < directories; i++ {
			if err := os.Mkdir(filepath.Join(root, fmt.Sprint(i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		cat := catalog.New(db)
		lib, err := cat.Create("Movies", "movie", root)
		if err != nil {
			t.Fatal(err)
		}
		s := New(db, cat, assets.Probe{})
		job, err := s.Queue(lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		started := time.Now()
		go func() { s.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()
		deadline := time.After(60 * time.Second)
		for {
			current, err := s.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status == "complete" {
				return time.Since(started)
			}
			if current.Status == "failed" {
				t.Fatalf("scan failed: %+v", current)
			}
			select {
			case <-deadline:
				t.Fatalf("scan made no progress under foreground pressure: %+v", current)
			case <-time.After(5 * time.Millisecond):
			}
		}
	}

	// The probe is what the HTTP layer registers. It also notes when the scan
	// asks it from the pacing step, and when the scan next asks at all.
	var mu sync.Mutex
	var paced, followed, hurried, verificationPaces int
	var gaveWayAt time.Time
	pressure := false
	dbwork.RegisterForegroundProbe("scan-pressure-test", func() bool {
		pcs := make([]uintptr, 16)
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
		pacing, step, verification := false, false, false
		for {
			frame, more := frames.Next()
			pacing = pacing || strings.HasSuffix(frame.Function, "dbwork.PaceBackground")
			verification = verification || strings.HasSuffix(frame.Function, "ingestion.(*Service).verifyOneSource")
			step = step || strings.HasSuffix(frame.Function, "ingestion.(*Service).processQuantum")
			if !more {
				break
			}
		}
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if pressure {
			if pacing && verification {
				// The independent verifier has its own pause. It must not
				// overwrite the timestamp paired with the scan's next step.
				verificationPaces++
			} else if pacing {
				paced++
				gaveWayAt = now
			} else if step && !gaveWayAt.IsZero() {
				// The next step's own look at the foreground, after a pause.
				followed++
				if now.Sub(gaveWayAt) < 2*time.Millisecond {
					hurried++
				}
				gaveWayAt = time.Time{}
			}
		}
		return pressure
	})
	t.Cleanup(func() { dbwork.RegisterForegroundProbe("scan-pressure-test", nil) })

	idle := scan(t)
	mu.Lock()
	pressure = true
	mu.Unlock()
	busy := scan(t)
	mu.Lock()
	defer mu.Unlock()
	t.Logf("scan pauses=%d followed=%d early=%d; independent verifier pauses=%d", paced, followed, hurried, verificationPaces)
	if paced < steps {
		t.Fatalf("the scan continued without giving way to foreground work: %d pauses for %d folders", paced, directories)
	}
	if followed < steps || hurried != 0 {
		t.Fatalf("of %d steps that followed a pause, %d began before it had passed", followed, hurried)
	}
	if busy < idle {
		t.Logf("scan under pressure (%s) was not slower than idle (%s)", busy, idle)
	}
}
