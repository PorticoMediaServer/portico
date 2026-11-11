// Package jobs runs slow work in the background: a scan, matching a library, marking a whole
// view watched. Each area of the server registers its kinds of job here; one route starts any
// of them and one reads how it is getting on.
//
// A job is a row in the database, so work asked for before a restart is taken up after it,
// and its id still answers. That is why a kind's Run must be safe to run again from the start:
// do the work in pages, each its own small Background write, and skip what is already done.
package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Access is who may start a kind of job through the API.
type Access int

const (
	// Internal kinds are started only by the server's own code, behind a route of their own.
	Internal Access = iota
	// Owner kinds are started by the owner alone.
	Owner
	// Admins kinds are started by the owner and admins: work on a library.
	Admins
	// AnyProfile kinds are started by any signed-in profile, for work on what that profile
	// itself sees: the job runs as that profile and never for another.
	AnyProfile
)

// MaxItems is the most item ids one job may be given.
const MaxItems = 1000

// Target is what a job works on: exactly one of a library, a saved filter, a view, or a list
// of items, each by its public id.
type Target struct {
	Library string   `json:"library,omitempty"`
	Filter  string   `json:"filter,omitempty"`
	View    string   `json:"view,omitempty"`
	Items   []string `json:"items,omitempty"`
}

// key is the target as one short text. Two requests with the same key are the same work.
func (t Target) key() (string, error) {
	set := 0
	for _, has := range []bool{t.Library != "", t.Filter != "", t.View != "", len(t.Items) > 0} {
		if has {
			set++
		}
	}
	switch {
	case set != 1:
		return "", fmt.Errorf("%w: a job needs one target: a library, a filter, a view or a list of items", db.ErrInvalid)
	case len(t.Items) > MaxItems:
		return "", fmt.Errorf("%w: a job takes at most %d items", db.ErrInvalid, MaxItems)
	case t.Library != "":
		return t.Library, nil
	case t.Filter != "":
		return "filter:" + t.Filter, nil
	case t.View != "":
		return "view:" + t.View, nil
	}
	ids := slices.Clone(t.Items)
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return "items:" + hex.EncodeToString(sum[:16]), nil
}

// Request is what was asked for: the target, the kind's own parameters, and who asked.
type Request struct {
	Target Target          `json:"target"`
	Params json.RawMessage `json:"params,omitempty"`
	// ProfileID is the profile that asked, kept for AnyProfile kinds; 0 otherwise.
	ProfileID int64 `json:"profile,omitempty"`
	// UserID is the account that asked, or 0 for the server itself.
	UserID int64 `json:"-"`
	// IdempotencyKey, when the request came with one, makes a repeat of the same request
	// answer with the job the first one started, even after that job has ended.
	IdempotencyKey string `json:"-"`
}

// Work is one run of a job.
type Work struct {
	Job db.Job
	Request
	// Progress records how far the work has got; total is 0 when its size is not known. It
	// may be called as often as is convenient: it writes at most every two seconds.
	Progress func(done, total int64)
}

// Kind is one kind of job, registered by the area that does the work.
type Kind struct {
	// Name is the kind as apps send it: "scan", "match_library".
	Name string
	// Access is who may start it through the API.
	Access Access
	// Lane names a queue. Jobs in one lane run one at a time, in the order asked for; lanes
	// run beside each other. With none, the kind has a lane of its own.
	Lane string
	// Check, if set, refuses a target or parameters the kind cannot work on, before anything
	// is stored: db.ErrNotFound for a target that is not there (or not the asker's to see),
	// db.ErrInvalid for the rest.
	Check func(ctx context.Context, req Request) error
	// Run does the work. It returns when ctx ends (the job was cancelled, or the server is
	// stopping and will run it again at the next start) and must be safe to run again.
	Run func(ctx context.Context, work Work) error
}

// Runner keeps the registered kinds and runs their jobs.
type Runner struct {
	db  *db.DB
	ctx context.Context
	log *slog.Logger

	mu    sync.Mutex
	kinds map[string]Kind
	lanes map[string]*lane
}

// lane is one queue of jobs that run one at a time.
type lane struct {
	queue   []db.Job
	working bool
	current int64 // the job being run
	stop    context.CancelFunc
	// cancelled says the job being run was cancelled and is on its way out. Its row is
	// already marked, and asking for the same work again queues it afresh.
	cancelled bool
}

// New makes a runner whose work stops when ctx ends.
func New(ctx context.Context, d *db.DB, log *slog.Logger) *Runner {
	return &Runner{db: d, ctx: ctx, log: log, kinds: map[string]Kind{}, lanes: map[string]*lane{}}
}

// Register adds a kind. Every kind is registered while the server starts, before Resume.
func (r *Runner) Register(k Kind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, twice := r.kinds[k.Name]; twice || k.Name == "" || k.Run == nil {
		panic("jobs: kind " + k.Name + " is registered twice, or has no name or no Run")
	}
	if k.Lane == "" {
		k.Lane = k.Name
	}
	r.kinds[k.Name] = k
}

// Kind returns a registered kind.
func (r *Runner) Kind(name string) (Kind, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.kinds[name]
	return k, ok
}

// Start asks for a job. While the same work (kind and target, and for an AnyProfile kind the
// same profile) is waiting or running, asking again joins it.
func (r *Runner) Start(ctx context.Context, kind string, req Request) (db.Job, error) {
	k, ok := r.Kind(kind)
	if !ok {
		return db.Job{}, fmt.Errorf("%w: there is no job of kind %q", db.ErrInvalid, kind)
	}
	target, err := req.Target.key()
	if err != nil {
		return db.Job{}, err
	}
	if k.Access == AnyProfile {
		target = fmt.Sprintf("%s@%d", target, req.ProfileID)
	} else {
		req.ProfileID = 0
	}
	if k.Check != nil {
		if err := k.Check(ctx, req); err != nil {
			return db.Job{}, err
		}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return db.Job{}, err
	}
	sum := sha256.Sum256(append([]byte(kind+"\n"), payload...))
	job, err := r.db.StartJob(ctx, db.NewJob{Kind: kind, Target: target, Payload: string(payload), OwnerID: req.UserID,
		IdempotencyKey: req.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:])})
	if err == nil && job.State == db.JobWaiting {
		r.enqueue(k, job)
	}
	return job, err
}

// Interrupted is why a job ended that was cut short twice.
const Interrupted = "The server stopped unexpectedly twice while doing this, so it was not started again."

// Resume takes up the jobs that were waiting or running when the server last stopped. A job
// is marked as running before its work begins, and a server that stops in good order puts it
// back to waiting; so a job found running was cut short: by a crash, a kill or a power cut.
// It is run again once. Found cut short a second time it is ended as failed, because it may
// be the job that brings the server down, and a server must stay up. A job of a kind this
// server no longer has is ended as failed too.
func (r *Runner) Resume() error {
	unfinished, err := r.db.UnfinishedJobs(r.ctx)
	for _, job := range unfinished {
		k, ok := r.Kind(job.Kind)
		switch {
		case !ok:
			r.db.FinishJob(r.ctx, job.ID, db.JobFailed, "This server no longer does this kind of work.")
		case job.State == db.JobRunning && job.Attempts >= 2:
			r.log.Error("job not run again: it was cut short twice", "kind", job.Kind, "target", job.Target)
			r.db.FinishJob(r.ctx, job.ID, db.JobFailed, Interrupted)
		default:
			r.enqueue(k, job)
		}
	}
	return err
}

func (r *Runner) enqueue(k Kind, job db.Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.lanes[k.Lane]
	if l == nil {
		l = &lane{}
		r.lanes[k.Lane] = l
	}
	if l.current == job.ID && !l.cancelled || slices.ContainsFunc(l.queue, func(queued db.Job) bool { return queued.ID == job.ID }) {
		return
	}
	l.queue = append(l.queue, job)
	if !l.working {
		l.working = true
		go r.work(l)
	}
}

// Cancel stops a job. Whatever it has done so far stays done.
func (r *Runner) Cancel(ctx context.Context, job db.Job) error {
	r.mu.Lock()
	for _, l := range r.lanes {
		if l.current == job.ID && l.stop != nil {
			l.cancelled = true
			l.stop()
		}
		l.queue = slices.DeleteFunc(l.queue, func(queued db.Job) bool { return queued.ID == job.ID })
	}
	r.mu.Unlock()
	return r.db.CancelJob(ctx, job.ID)
}

func (r *Runner) work(l *lane) {
	for {
		r.mu.Lock()
		if len(l.queue) == 0 || r.ctx.Err() != nil {
			l.working = false
			r.mu.Unlock()
			return
		}
		job := l.queue[0]
		l.queue = l.queue[1:]
		ctx, stop := context.WithCancel(r.ctx)
		l.current, l.stop = job.ID, stop
		k := r.kinds[job.Kind]
		r.mu.Unlock()

		err := r.run(ctx, k, job)
		stopped := ctx.Err() != nil
		stop()
		r.mu.Lock()
		cancelled := l.cancelled
		l.current, l.stop, l.cancelled = 0, nil, false
		r.mu.Unlock()
		switch {
		case stopped && !cancelled:
			// The server is stopping in good order: the job waits for the next start.
			pause, done := context.WithTimeout(context.Background(), 2*time.Second)
			r.db.PauseJob(pause, job.ID)
			done()
		case stopped:
			// The job was cancelled, and Cancel has recorded that. Nothing is recorded here,
			// because the same work may already have been asked for again under the same row.
		case err != nil:
			r.log.Error("job failed", "kind", job.Kind, "target", job.Target, "error", err)
			r.db.FinishJob(r.ctx, job.ID, db.JobFailed, err.Error())
		default:
			r.db.FinishJob(r.ctx, job.ID, db.JobDone, "")
		}
	}
}

func (r *Runner) run(ctx context.Context, k Kind, job db.Job) (err error) {
	// The job is marked as running, in a write of its own, before any of its work begins.
	if ran, err := r.db.RunJob(ctx, job.ID); err != nil || !ran {
		return err
	}
	work := Work{Job: job}
	if err := json.Unmarshal([]byte(job.Payload), &work.Request); err != nil {
		return fmt.Errorf("the job's stored request cannot be read: %w", err)
	}
	work.UserID = job.OwnerID
	if k.Access == AnyProfile {
		// A job that runs as a profile does not outlive it: with the account or the profile
		// gone it is cancelled, never run as the server.
		there, err := r.db.ProfileOfUser(ctx, work.ProfileID, job.OwnerID)
		if err != nil {
			return err
		}
		if !there || job.OwnerID == 0 {
			return r.db.CancelJob(ctx, job.ID)
		}
	}
	// A fault in a kind's code ends that job as failed and takes nothing else down.
	defer func() {
		if fault := recover(); fault != nil {
			r.log.Error("job panicked", "kind", job.Kind, "target", job.Target, "panic", fault, "stack", string(debug.Stack()))
			err = errors.New("This stopped because of a fault in the server. The server's log has the details.")
		}
	}()
	var mu sync.Mutex
	var reported time.Time
	var last, written [2]int64
	work.Progress = func(done, total int64) {
		mu.Lock()
		defer mu.Unlock()
		last = [2]int64{done, total}
		if time.Since(reported) > 2*time.Second {
			reported, written = time.Now(), last
			r.db.JobProgress(ctx, job.ID, done, total)
		}
	}
	err = k.Run(ctx, work)
	// The last count is always written, so a job that ends shows how far it really got.
	mu.Lock()
	defer mu.Unlock()
	if last != written {
		r.db.JobProgress(context.WithoutCancel(ctx), job.ID, last[0], last[1])
	}
	return err
}

// ProfileOf is the profile a job runs as: the one that started it, for a kind any profile
// may start. It is 0 for every other job.
func ProfileOf(job db.Job) int64 {
	var req Request
	json.Unmarshal([]byte(job.Payload), &req)
	return req.ProfileID
}
