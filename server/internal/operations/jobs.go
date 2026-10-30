package operations

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"sort"
	"strconv"
	"sync"
	"time"
	_ "time/tzdata"
)

// Adapter registers an EXISTING domain worker, not a competing executor.
// StartTx must only enqueue durable work in the supplied transaction. Observe
// and Cancel must be bounded and cancellation must await publication fencing.
// Maintenance callbacks are idempotent cleanup hooks, never arbitrary shell/paths.
type Adapter struct {
	// Step advances durable domain work in bounded transactions; the scheduler owns its lifecycle.
	Step func(context.Context, string) (JobObservation, error)
	// AsyncStep allows cancellable filesystem work outside the control loop.
	AsyncStep   bool
	Kind        string
	Lane        string
	ValidateTx  func(context.Context, *sql.Tx, string) error
	StartTx     func(context.Context, *sql.Tx, string, string) (string, error)
	Observe     func(context.Context, string) (JobObservation, error)
	Cancel      func(context.Context, string) error
	Maintenance func(context.Context, string) error
	// Resource names the physical lane this job competes in (see lanes.go).
	// Empty keeps the default derived from Lane, so an existing registration is
	// unchanged.
	Resource         string
	ResourceRequired bool
	ControlTx        func(context.Context, *sql.Tx, string, string) error
	// CancelTx fences domains that support cancellation but not pause/resume.
	CancelTx  func(context.Context, *sql.Tx, string) error
	Interrupt func(context.Context, string)
}
type JobObservation struct {
	State     string
	Phase     string
	Processed *int64
	Total     *int64
	ErrorCode string
}
type Job struct {
	ID               string          `json:"id"`
	Sequence         int64           `json:"-"`
	Kind             string          `json:"kind"`
	Resource         string          `json:"resource"`
	Trigger          string          `json:"trigger"`
	State            string          `json:"state"`
	Phase            string          `json:"phase"`
	Revision         int64           `json:"revision"`
	Attempt          int             `json:"attempt"`
	CreatedAt        apiwire.Instant `json:"createdAt"`
	UpdatedAt        apiwire.Instant `json:"updatedAt"`
	NextAt           apiwire.Instant `json:"nextAt"`
	DomainID         string          `json:"domainId,omitempty"`
	ErrorCode        string          `json:"errorCode,omitempty"`
	Predecessor      string          `json:"predecessor,omitempty"`
	SettingsRevision int64           `json:"settingsRevision"`
	Lane             string          `json:"lane"`
	Actions          []string        `json:"actions"`
	Processed        *int64          `json:"processed,omitempty"`
	Total            *int64          `json:"total,omitempty"`
}

const jobColumns = `id,sequence,kind,resource,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision,processed,(SELECT total FROM console_operation_totals WHERE operation_id=console_operations.id)`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	e := row.Scan(&j.ID, &j.Sequence, &j.Kind, &j.Resource, &j.Trigger, &j.State, &j.Phase, &j.Revision, &j.Attempt, &j.CreatedAt, &j.UpdatedAt, &j.NextAt, &j.DomainID, &j.ErrorCode, &j.Predecessor, &j.SettingsRevision, &j.Processed, &j.Total)
	j.Actions = []string{}
	return j, e
}

type Scheduler struct {
	Store    *Store
	mu       sync.RWMutex
	adapters map[string]Adapter
	running  map[string]context.CancelFunc
	stepWG   sync.WaitGroup
	lanes    *laneSet
}

func NewScheduler(s *Store) *Scheduler {
	return &Scheduler{Store: s, adapters: map[string]Adapter{}, running: map[string]context.CancelFunc{}, lanes: newLaneSet()}
}

// LaneStates reports per-lane occupancy and backlog for diagnostics.
func (s *Scheduler) LaneStates(ctx context.Context) []LaneState {
	states := s.laneSet().snapshot()
	backlog := map[string]int{}
	rows, e := s.Store.DB.QueryContext(ctx, `SELECT kind,count(*) FROM console_operations WHERE state IN('queued','running','reconciling') GROUP BY kind`)
	if e == nil {
		for rows.Next() {
			var kind string
			var count int
			if rows.Scan(&kind, &count) == nil {
				if a, ok := s.adapter(kind); ok {
					backlog[resourceLane(a)] += count
				}
			}
		}
		rows.Close()
	}
	for index := range states {
		states[index].Backlog = backlog[states[index].Lane]
	}
	return states
}

func (s *Scheduler) laneSet() *laneSet {
	s.mu.Lock()
	if s.lanes == nil {
		s.lanes = newLaneSet()
	}
	lanes := s.lanes
	s.mu.Unlock()
	return lanes
}
func (s *Scheduler) Register(a Adapter) error {
	if !validID(a.Kind) || a.ValidateTx == nil || a.Lane != "maintenance" && a.Lane != "background-media" {
		return ErrInvalid
	}
	if a.Step == nil && a.Maintenance == nil && (a.StartTx == nil || a.Observe == nil) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.adapters[a.Kind]; ok {
		return ErrConflict
	}
	s.adapters[a.Kind] = a
	return nil
}
func (s *Scheduler) adapter(kind string) (Adapter, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.adapters[kind]
	return a, ok
}

type JobKind struct {
	Kind             string `json:"kind"`
	Lane             string `json:"lane"`
	ResourceRequired bool   `json:"resourceRequired"`
}

func (s *Scheduler) Kinds() []JobKind {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []JobKind{}
	for _, a := range s.adapters {
		out = append(out, JobKind{a.Kind, a.Lane, a.ResourceRequired || a.Maintenance == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
func (s *Scheduler) decorate(j *Job) {
	a, ok := s.adapter(j.Kind)
	j.Actions = []string{}
	if !ok {
		return
	}
	j.Lane = a.Lane
	if a.Step != nil {
		return
	} // Domain receipts own state; generic controls cannot fence these jobs.
	if j.State == "queued" || j.State == "running" || j.State == "reconciling" || j.State == "paused" {
		if a.Cancel != nil || a.Maintenance != nil || j.DomainID == "" {
			j.Actions = append(j.Actions, "cancel")
		}
	}
	if a.ControlTx != nil {
		if j.State == "paused" {
			j.Actions = append(j.Actions, "resume")
		} else if j.State == "queued" || j.State == "running" || j.State == "reconciling" {
			j.Actions = append(j.Actions, "pause")
		}
	}
	if j.State == "failed" || j.State == "cancelled" {
		j.Actions = append(j.Actions, "retry")
	}
}

type RunJob struct {
	Kind           string `json:"kind"`
	Resource       string `json:"resource"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type JobCommand struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

// Active-operation bounds per admission class (see enqueue).
const (
	maxActiveConsoleOperations = 200
	maxActiveDomainOperations  = 10_000
)

func (s *Scheduler) enqueue(ctx context.Context, tx *sql.Tx, kind, resource, actor, trigger, predecessor string) (Job, error) {
	a, ok := s.adapter(kind)
	if !ok {
		return Job{}, ErrInvalid
	}
	if resource != "" && !validID(resource) {
		return Job{}, ErrInvalid
	}
	if e := a.ValidateTx(ctx, tx, resource); e != nil {
		return Job{}, e
	}
	existing, e := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM console_operations WHERE kind=? AND resource=? AND state IN ('queued','running','cancellation-requested','reconciling','paused')`, kind, resource))
	if e == nil {
		s.decorate(&existing)
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Job{}, e
	}
	// Admission is bounded per class (PERF-S22): the owner's console work
	// (scans, refreshes, scheduled and retried runs) and viewers' domain work
	// (bulk personal-state, container resets) never crowd each other out. A
	// domain's own per-viewer limit decides fairness among viewers; this is a
	// resource bound on active rows, not a quota over time.
	class, limit := "trigger<>'domain'", maxActiveConsoleOperations
	if trigger == "domain" {
		class, limit = "trigger='domain'", maxActiveDomainOperations
	}
	var n int
	if e = tx.QueryRow(`SELECT count(*) FROM console_operations WHERE state IN ('queued','running','cancellation-requested','reconciling','paused') AND ` + class).Scan(&n); e != nil {
		return Job{}, e
	}
	if n >= limit {
		return Job{}, ErrCapacity
	}
	settings, e := readSettings(tx)
	if e != nil {
		return Job{}, e
	}
	id := identity.Token()
	now := s.Store.now()
	if _, e = tx.Exec(`INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision) VALUES(?,?,?,?,?,'queued','waiting',1,0,?,?,?,'','',?,?)`, id, kind, resource, Hash(actor), trigger, now, now, now, predecessor, settings.Revision); e != nil {
		return Job{}, e
	}
	if e = AppendOperationTx(tx, id); e != nil {
		return Job{}, e
	}
	if e = Audit(tx, now, actor, "job.admit", id, 1); e != nil {
		return Job{}, e
	}
	j, e := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM console_operations WHERE id=?`, id))
	s.decorate(&j)
	return j, e
}

// EnqueueDomainTx schedules an already authorized domain operation atomically with
// its one receipt. It does not apply the console's owner-only HTTP policy.
func (s *Scheduler) EnqueueDomainTx(ctx context.Context, tx *sql.Tx, kind, resource, actor string) (Job, error) {
	return s.enqueue(ctx, tx, kind, resource, actor, "domain", "")
}

func (s *Scheduler) Enqueue(ctx context.Context, p identity.Principal, auth Authorize, c RunJob) (out Job, e error) {
	e = s.Store.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		scope := "job:" + AccountKey(p)
		raw, digest, err := Receipt(tx, scope, c.IdempotencyKey, c, s.Store.now())
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		out, err = s.enqueue(ctx, tx, c.Kind, c.Resource, AccountKey(p), "owner", "")
		if err != nil {
			return err
		}
		return SaveReceipt(tx, scope, c.IdempotencyKey, digest, out, s.Store.now())
	})
	return
}
func (s *Scheduler) Jobs(ctx context.Context, auth Authorize, cursor string) (out Page[Job], e error) {
	before, e := Cursor(cursor)
	if e != nil {
		return out, e
	}
	out.Items = []Job{}
	e = s.Store.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT `+jobColumns+` FROM console_operations WHERE sequence<? ORDER BY sequence DESC LIMIT 41`, before)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				return err
			}
			s.decorate(&j)
			out.Items = append(out.Items, j)
		}
		if len(out.Items) > 40 {
			out.Items = out.Items[:40]
			out.NextCursor = strconv.FormatInt(out.Items[39].Sequence, 10)
		}
		return rows.Err()
	})
	return
}
func (s *Scheduler) Command(ctx context.Context, p identity.Principal, auth Authorize, id, action string, c JobCommand) (out Job, e error) {
	if !validID(id) || action != "cancel" && action != "retry" && action != "pause" && action != "resume" {
		return out, ErrInvalid
	}
	e = s.Store.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		scope := "job-control:" + AccountKey(p)
		raw, digest, err := Receipt(tx, scope, c.IdempotencyKey, []any{id, action, c}, s.Store.now())
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		j, err := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM console_operations WHERE id=?`, id))
		if err != nil {
			return err
		}
		if j.Revision != c.ExpectedRevision {
			return &ConflictError{j.Revision}
		}
		s.decorate(&j)
		allowed := false
		for _, v := range j.Actions {
			allowed = allowed || v == action
		}
		if !allowed {
			return ErrConflict
		}
		if action == "retry" {
			out, err = s.enqueue(ctx, tx, j.Kind, j.Resource, AccountKey(p), "retry", j.ID)
			if err != nil {
				return err
			}
		} else if action == "pause" || action == "resume" {
			a, ok := s.adapter(j.Kind)
			if !ok || a.ControlTx == nil {
				return ErrInvalid
			}
			if j.DomainID != "" {
				if err = a.ControlTx(ctx, tx, j.DomainID, action); err != nil {
					return err
				}
			}
			state := "paused"
			if action == "resume" {
				state = "queued"
				if j.DomainID != "" {
					state = "running"
				}
			}
			if _, err = updateOperationTx(tx, id, `UPDATE console_operations SET state=?,phase=?,revision=revision+1,updated_ms=? WHERE id=?`, state, state, s.Store.now(), id); err != nil {
				return err
			}
			out, err = scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM console_operations WHERE id=?`, id))
			if err != nil {
				return err
			}
			s.decorate(&out)
		} else {
			// Queued work with no producer can be synchronously fenced. Running work
			// stays cancellation-requested until the adapter or callback acknowledges.
			if j.DomainID != "" {
				if a, ok := s.adapter(j.Kind); ok {
					if a.CancelTx != nil {
						err = a.CancelTx(ctx, tx, j.DomainID)
					} else if a.ControlTx != nil {
						err = a.ControlTx(ctx, tx, j.DomainID, "cancel")
					}
					if err != nil {
						return err
					}
				}
			}
			state := "cancellation-requested"
			if (j.State == "queued" || j.State == "paused") && j.DomainID == "" {
				state = "cancelled"
			}
			if _, err = updateOperationTx(tx, id, `UPDATE console_operations SET state=?,phase=?,revision=revision+1,updated_ms=? WHERE id=?`, state, state, s.Store.now(), id); err != nil {
				return err
			}
			out, err = scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM console_operations WHERE id=?`, id))
			if err != nil {
				return err
			}
			s.decorate(&out)
		}
		if err = Audit(tx, s.Store.now(), AccountKey(p), "job."+action, id, out.Revision); err != nil {
			return err
		}
		return SaveReceipt(tx, scope, c.IdempotencyKey, digest, out, s.Store.now())
	})
	if e == nil && (action == "pause" || action == "cancel") && out.DomainID != "" {
		if a, ok := s.adapter(out.Kind); ok && a.Interrupt != nil {
			a.Interrupt(ctx, out.DomainID)
		}
	}
	if e == nil && action == "cancel" {
		s.mu.RLock()
		cancel := s.running[id]
		s.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
	}
	return
}

// Run has a single control loop. Domain work remains in registered producers.
// On restart, named producer IDs are reconciled, never blindly submitted twice.
func (s *Scheduler) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); s.stepWG.Wait() }()
	_ = dbwork.WithWriteTx(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM console_operations WHERE state='running'`)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err = updateOperationTx(tx, id, `UPDATE console_operations SET state='reconciling',phase='restart reconciliation',revision=revision+1 WHERE id=? AND state='running'`, id); err != nil {
				return err
			}
		}
		return nil
	})
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "console_*", "maintenance_*", "app_reports")
	defer unregister()
	progress := worker.NewProgress(dbwork.ChangingCommits)
	lastPrune := int64(0)
	worker.Run(ctx, "operations.scheduler", wake, func(ctx context.Context) time.Duration {
		progress.Reset()
		s.tick(ctx)
		if s.Store.now()-lastPrune >= 600000 || s.Store.appReportsDue(ctx) {
			lastPrune = s.Store.now()
			c, done := context.WithTimeout(ctx, 5*time.Second)
			e := s.Store.Prune(c)
			done()
			_ = s.Store.Alert(ctx, "retention-cleanup-failed", "warning", e != nil)
		}
		// Anything in flight, or anything that just changed, means come back
		// shortly. An idle scheduler waits to be told instead.
		if s.activeJobs() > 0 || progress.Moved() {
			return time.Second
		}
		return s.nextWake(ctx)
	})
}
func (s *Scheduler) tick(ctx context.Context) {
	// The scheduler's own bookkeeping is maintenance-class work; the jobs it
	// starts carry their own class into their own writes.
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	e := s.admitSchedules(ctx)
	_ = s.Store.Alert(ctx, "schedule-admission-failed", "warning", e != nil)
	jobs := s.dueJobsFair(ctx)
	if len(jobs) > 0 {
		// A debug record saying "nothing happened" cost a transaction and a commit
		// every second for the life of the process, and woke every loop that
		// listens for one.
		_ = s.Store.Record(ctx, "runtime", "debug", "scheduler", "control-tick", map[string]int64{"count": int64(len(jobs))})
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		s.advance(ctx, j)
	}
}

// dueJobsFair reads each physical lane separately, taking at most that lane's
// capacity, then orders the union by work class and age. A single global
// oldest-N window would let one busy lane fill the whole window and starve every
// other kind of work behind it; per-lane claiming cannot.
func (s *Scheduler) dueJobsFair(ctx context.Context) []Job {
	kinds := map[string][]string{}
	classes := map[string]dbwork.Class{}
	s.mu.RLock()
	for kind, a := range s.adapters {
		lane := resourceLane(a)
		kinds[lane] = append(kinds[lane], kind)
		classes[kind] = workClass(a)
	}
	s.mu.RUnlock()
	now := s.Store.now()
	jobs := []Job{}
	for _, lane := range Lanes() {
		members := kinds[lane]
		if len(members) == 0 {
			continue
		}
		sort.Strings(members)
		placeholders := ""
		args := []any{}
		for _, kind := range members {
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, kind)
		}
		// Ask for the lane's capacity plus a small allowance for work that is
		// already running, which is advanced rather than started.
		args = append(args, now, laneCapacity(lane)+8)
		rows, e := s.Store.DB.QueryContext(ctx, `SELECT `+jobColumns+` FROM console_operations WHERE state IN ('queued','running','cancellation-requested','reconciling','paused') AND NOT(state='paused' AND domain_id='') AND kind IN (`+placeholders+`) AND next_ms<=? ORDER BY CASE WHEN state='cancellation-requested' THEN 0 WHEN state IN('running','reconciling') THEN 1 ELSE 2 END,next_ms,created_ms LIMIT ?`, args...)
		if e != nil {
			continue
		}
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				break
			}
			jobs = append(jobs, j)
		}
		rows.Close()
	}
	// Cancellation first, then already-running work, then queued work ordered by
	// its declared class and age. The ladder decides which bulk kind goes first;
	// it never lets bulk work overtake a viewer, because the write gate does that.
	rank := func(j Job) int {
		if j.State == "cancellation-requested" {
			return 0
		}
		if j.State != "queued" {
			return 1
		}
		return 1 + classes[j.Kind].Priority()
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		if rank(jobs[i]) != rank(jobs[j]) {
			return rank(jobs[i]) < rank(jobs[j])
		}
		if jobs[i].NextAt != jobs[j].NextAt {
			return jobs[i].NextAt < jobs[j].NextAt
		}
		if jobs[i].CreatedAt != jobs[j].CreatedAt {
			return jobs[i].CreatedAt < jobs[j].CreatedAt
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs
}

func (s *Scheduler) finish(ctx context.Context, j Job, state, phase, code string) {
	updated := false
	e := dbwork.WithWriteTx(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		var err error
		updated, err = updateOperationTx(tx, j.ID, `UPDATE console_operations SET state=?,phase=?,error_code=?,processed=?,revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND state!='cancellation-requested'`, state, phase, code, j.Processed, s.Store.now(), j.ID, j.Revision)
		if err != nil || !updated {
			return err
		}
		if j.Total == nil {
			_, err = tx.Exec(`DELETE FROM console_operation_totals WHERE operation_id=?`, j.ID)
			return err
		}
		if j.Processed == nil || *j.Total < *j.Processed || *j.Total < 0 {
			return ErrInvalid
		}
		_, err = tx.Exec(`INSERT INTO console_operation_totals(operation_id,total) VALUES(?,?) ON CONFLICT(operation_id) DO UPDATE SET total=excluded.total`, j.ID, *j.Total)
		return err
	})
	if e != nil || !updated {
		return
	}
	if e == nil && state == "failed" {
		params := map[string]string{"reason": code, "time": time.UnixMilli(s.Store.now()).UTC().Format(time.RFC3339Nano)}
		var title string
		if e := s.Store.DB.QueryRowContext(ctx, `SELECT name FROM libraries WHERE id=?`, j.Resource).Scan(&title); e == nil && title != "" {
			params["title"] = title
		}
		_ = s.Store.Alert(ctx, "job-failed-"+j.Kind, "warning", true, params)
		_ = s.Store.Record(ctx, "runtime", "error", "scheduler", "job-failed", map[string]int64{"attempt": int64(j.Attempt)})
	}
}
func (s *Scheduler) advance(ctx context.Context, j Job) {
	defer func() {
		if ctx.Err() == nil {
			_, _ = dbwork.ExecWrite(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `UPDATE console_operations SET next_ms=? WHERE id=?`, s.Store.now()+1000, j.ID)
		}
	}()
	a, ok := s.adapter(j.Kind)
	if !ok {
		s.finish(ctx, j, "failed", "worker unavailable", "worker-unregistered")
		return
	}
	lanes := s.laneSet()
	lane := resourceLane(a)
	// Starting work needs a slot in its physical lane. Observing, cancelling and
	// finishing work never do: those must not be blocked by the very saturation
	// they are about to relieve.
	starting := j.State == "queued" && j.DomainID == ""
	if starting {
		if !lanes.tryAcquire(lane, j.ID) {
			return
		}
		defer func() {
			if !s.startedDomain(ctx, j.ID) {
				lanes.release(j.ID)
			}
		}()
	}
	if j.State == "cancellation-requested" {
		lanes.release(j.ID)
		if j.DomainID != "" && a.Cancel != nil {
			c, done := context.WithTimeout(ctx, 3*time.Second)
			e := a.Cancel(c, j.DomainID)
			done()
			if e != nil {
				return
			}
			c, done = context.WithTimeout(ctx, 3*time.Second)
			v, e := a.Observe(c, j.DomainID)
			done()
			if e != nil || v.State != "cancelled" && v.State != "succeeded" && v.State != "failed" {
				return
			}
		}
		s.mu.RLock()
		_, active := s.running[j.ID]
		s.mu.RUnlock()
		if active {
			return
		}
		_, _ = updateOperation(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), j.ID, `UPDATE console_operations SET state='cancelled',phase='producer quiescent',revision=revision+1,updated_ms=? WHERE id=? AND state='cancellation-requested'`, s.Store.now(), j.ID)
		return
	}
	if j.DomainID != "" && a.Observe != nil {
		c, done := context.WithTimeout(ctx, 3*time.Second)
		v, e := a.Observe(c, j.DomainID)
		done()
		if e != nil {
			return
		}
		switch v.State {
		case "running", "queued", "paused", "succeeded", "failed", "cancelled":
		default:
			return
		}
		switch v.State {
		case "succeeded", "failed", "cancelled":
			lanes.release(j.ID)
		}
		if v.State != j.State || v.Phase != j.Phase || v.ErrorCode != j.ErrorCode || !equalCount(v.Processed, j.Processed) || !equalCount(v.Total, j.Total) {
			j.Processed = v.Processed
			j.Total = v.Total
			s.finish(ctx, j, v.State, v.Phase, v.ErrorCode)
		}
		return
	}
	if a.Step != nil && a.AsyncStep {
		s.startAsyncStep(ctx, j, a)
		return
	}
	if a.Step != nil {
		if !starting && !lanes.tryAcquire(lane, j.ID) {
			return
		}
		defer lanes.release(j.ID)
		c, done := context.WithTimeout(dbwork.WithClass(ctx, workClass(a)), 750*time.Millisecond)
		v, err := a.Step(c, j.Resource)
		done()
		if err != nil {
			return
		} // A rolled-back batch is safe to retry, including after restart.
		j.Processed = v.Processed
		j.Total = v.Total
		s.finish(ctx, j, v.State, v.Phase, v.ErrorCode)
		return
	}
	if a.Maintenance != nil {
		s.startMaintenance(ctx, j, a)
		return
	}
	gated, e := dbwork.Begin(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var state string
	var revision int64
	if e = tx.QueryRow(`SELECT state,revision FROM console_operations WHERE id=?`, j.ID).Scan(&state, &revision); e != nil || revision != j.Revision || state == "cancellation-requested" {
		return
	}
	domain, e := a.StartTx(ctx, tx, j.ID, j.Resource)
	if e != nil {
		gated.Rollback()
		s.finish(ctx, j, "failed", "admission failed", "domain-admission-failed")
		return
	}
	if _, e = tx.Exec(`UPDATE console_operations SET domain_id=?,state='running',phase='domain worker',attempt=attempt+1,revision=revision+1,updated_ms=? WHERE id=?`, domain, s.Store.now(), j.ID); e != nil {
		return
	}
	if e = AppendOperationTx(tx, j.ID); e != nil {
		return
	}
	_ = gated.Commit()
}

// startedDomain reports whether a start attempt actually handed the job to its
// domain worker. A job that is now running keeps its lane slot until the worker
// reports a terminal state; one that failed admission gives the slot straight
// back so the lane is not held by nothing.
func (s *Scheduler) startedDomain(ctx context.Context, id string) bool {
	var domain, state string
	if s.Store.DB.QueryRowContext(ctx, `SELECT domain_id,state FROM console_operations WHERE id=?`, id).Scan(&domain, &state) != nil {
		return false
	}
	s.mu.RLock()
	_, active := s.running[id]
	s.mu.RUnlock()
	return active || domain != "" && (state == "running" || state == "reconciling")
}

type Schedule struct {
	ID            string          `json:"id"`
	Revision      int64           `json:"revision"`
	Kind          string          `json:"kind"`
	Resource      string          `json:"resource"`
	Enabled       bool            `json:"enabled"`
	Timezone      string          `json:"timezone"`
	StartMinute   int             `json:"startMinute"`
	WindowMinutes int             `json:"windowMinutes"`
	CatchUp       bool            `json:"catchUp"`
	LastSlot      string          `json:"lastSlot"`
	CreatedAt     apiwire.Instant `json:"createdAt"`
}
type ScheduleChange struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	IdempotencyKey   string   `json:"idempotencyKey"`
	Value            Schedule `json:"value"`
}

const scheduleColumns = `id,revision,kind,resource,enabled,timezone,start_minute,window_minutes,catch_up,last_slot,created_ms`

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var v Schedule
	e := row.Scan(&v.ID, &v.Revision, &v.Kind, &v.Resource, &v.Enabled, &v.Timezone, &v.StartMinute, &v.WindowMinutes, &v.CatchUp, &v.LastSlot, &v.CreatedAt)
	return v, e
}
func (s *Scheduler) Schedules(ctx context.Context, auth Authorize) (out []Schedule, e error) {
	out = []Schedule{}
	e = s.Store.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT ` + scheduleColumns + ` FROM console_schedules ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanSchedule(rows)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return
}
func (s *Scheduler) SaveSchedule(ctx context.Context, p identity.Principal, auth Authorize, c ScheduleChange) (out Schedule, e error) {
	v := c.Value
	if v.Revision != 0 || v.CreatedAt != 0 || v.LastSlot != "" || !validID(v.ID) || v.StartMinute < 0 || v.StartMinute >= 1440 || v.WindowMinutes < 1 || v.WindowMinutes > 1440 || len(v.Timezone) > 80 {
		return out, ErrInvalid
	}
	if _, e = time.LoadLocation(v.Timezone); e != nil {
		return out, ErrInvalid
	}
	a, ok := s.adapter(v.Kind)
	if v.Enabled && !ok {
		return out, ErrInvalid
	}
	e = s.Store.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		scope := "schedule:" + AccountKey(p)
		raw, digest, err := Receipt(tx, scope, c.IdempotencyKey, c, s.Store.now())
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		if v.Enabled {
			if err = a.ValidateTx(ctx, tx, v.Resource); err != nil {
				return err
			}
		}
		current, err := scanSchedule(tx.QueryRow(`SELECT `+scheduleColumns+` FROM console_schedules WHERE id=?`, v.ID))
		if errors.Is(err, sql.ErrNoRows) {
			if c.ExpectedRevision != 0 {
				return ErrConflict
			}
			var n int
			if err = tx.QueryRow(`SELECT count(*) FROM console_schedules`).Scan(&n); err != nil {
				return err
			}
			if n >= 100 {
				return ErrCapacity
			}
			current.CreatedAt = apiwire.Instant(s.Store.now())
		} else if err != nil {
			return err
		} else if current.Revision != c.ExpectedRevision {
			return &ConflictError{current.Revision}
		}
		// An owner can disable an existing schedule after its library or runtime
		// adapter disappears, without admitting work against an invalid resource.
		if !v.Enabled && (!ok || v.Resource != "" && !validID(v.Resource)) && (current.ID == "" || current.Kind != v.Kind || current.Resource != v.Resource) {
			return ErrInvalid
		}
		// The last admitted local-day slot is retained across edits and DST folds.
		v.Revision = current.Revision + 1
		v.CreatedAt = current.CreatedAt
		v.LastSlot = current.LastSlot
		if _, err = tx.Exec(`INSERT INTO console_schedules VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,kind=excluded.kind,resource=excluded.resource,enabled=excluded.enabled,timezone=excluded.timezone,start_minute=excluded.start_minute,window_minutes=excluded.window_minutes,catch_up=excluded.catch_up`, v.ID, v.Revision, v.Kind, v.Resource, v.Enabled, v.Timezone, v.StartMinute, v.WindowMinutes, v.CatchUp, v.LastSlot, v.CreatedAt); err != nil {
			return err
		}
		if err = Audit(tx, s.Store.now(), AccountKey(p), "schedule.apply", v.ID, v.Revision); err != nil {
			return err
		}
		out = v
		return SaveReceipt(tx, scope, c.IdempotencyKey, digest, out, s.Store.now())
	})
	return
}

// ScheduleSlot uses local calendar days, not elapsed 24-hour increments. DST
// folds run once; nonexistent spring-forward times enter the remaining window.
// Catch-up coalesces missed days to one admission, never a backlog storm.
func ScheduleSlot(v Schedule, now time.Time) (string, bool) {
	loc, e := time.LoadLocation(v.Timezone)
	if e != nil || !v.Enabled {
		return "", false
	}
	return scheduleSlotLocal(v, now.In(loc))
}

func scheduleSlotLocal(v Schedule, local time.Time) (string, bool) {
	minute := local.Hour()*60 + local.Minute()
	day := local.Format("2006-01-02")
	start := v.StartMinute
	delta := minute - start
	if delta < 0 && start+v.WindowMinutes > 1440 {
		delta += 1440
		day = local.AddDate(0, 0, -1).Format("2006-01-02")
	}
	inWindow := delta >= 0 && delta < v.WindowMinutes
	if inWindow && (v.LastSlot == "" || v.LastSlot < day) {
		if v.CatchUp || delta == 0 {
			return day, true
		}
		return "", false
	}
	return "", false
}
func (s *Scheduler) admitSchedules(ctx context.Context) error {
	gated2, e := dbwork.Begin(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	rows, e := tx.Query(`SELECT ` + scheduleColumns + ` FROM console_schedules WHERE enabled=1 LIMIT 100`)
	if e != nil {
		return e
	}
	values := []Schedule{}
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var admissionErr error
	for _, v := range values {
		slot, ready := ScheduleSlot(v, s.Store.Now())
		if !ready {
			continue
		}
		// One stale/deleted resource must not starve other schedules. A savepoint
		// also prevents a failed admission from leaving a partial job or audit.
		if _, e = tx.Exec(`SAVEPOINT console_schedule_admission`); e != nil {
			return e
		}
		if _, e = s.enqueue(ctx, tx, v.Kind, v.Resource, "scheduler", "schedule", ""); e == nil {
			_, e = tx.Exec(`UPDATE console_schedules SET last_slot=? WHERE id=? AND revision=?`, slot, v.ID, v.Revision)
		}
		if e != nil {
			admissionErr = e
			if _, e = tx.Exec(`ROLLBACK TO console_schedule_admission`); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(`RELEASE console_schedule_admission`); e != nil {
			return e
		}
	}
	if e = gated2.Commit(); e != nil {
		return e
	}
	// The outer loop owns the safe deduplicated alert, outside this transaction.
	return admissionErr
}

func equalCount(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// activeJobs reports how many jobs this process is running. It is the "come back
// shortly" signal for the scheduler loop: a scheduler with nothing in flight and
// nothing newly written has nothing to poll for, and waits to be told.
func (s *Scheduler) activeJobs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// The scheduler owns admission, cancellation and shutdown for long filesystem
// steps. A slow copy never blocks the control loop or holds a SQLite writer.
// startMaintenance runs a cleanup hook on the same supervised lifecycle as an
// asynchronous step: it holds its lane and registers its cancel handle, and the
// control loop returns at once. A slow hook used to run inline, delaying every
// other lane's due work by up to its 30 s budget (WJ6).
func (s *Scheduler) startMaintenance(ctx context.Context, j Job, a Adapter) {
	lanes := s.laneSet()
	s.mu.Lock()
	if _, active := s.running[j.ID]; active {
		s.mu.Unlock()
		return
	}
	if !lanes.holds(j.ID) && !lanes.tryAcquire(resourceLane(a), j.ID) {
		s.mu.Unlock()
		return
	}
	c, done := context.WithTimeout(dbwork.WithClass(ctx, workClass(a)), 30*time.Second)
	s.running[j.ID] = done
	s.stepWG.Add(1)
	s.mu.Unlock()
	stop := func() {
		done()
		lanes.release(j.ID)
		s.mu.Lock()
		delete(s.running, j.ID)
		s.mu.Unlock()
	}
	// Cleanup hooks are required to be idempotent, so interrupted housekeeping
	// can reconcile by rerunning. No filesystem deletion primitive is exposed.
	updated, e := updateOperation(ctx, s.Store.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), j.ID, `UPDATE console_operations SET state='running',phase='maintenance',attempt=attempt+1,revision=revision+1,updated_ms=? WHERE id=? AND revision=?`, s.Store.now(), j.ID, j.Revision)
	if e != nil || !updated {
		stop()
		s.stepWG.Done()
		return
	}
	j.Revision++
	j.Attempt++
	supervise.Go("operations.maintenance", func() {
		defer s.stepWG.Done()
		e := a.Maintenance(c, j.ID)
		stop()
		if ctx.Err() != nil {
			return
		}
		// Domain cleanup may count committed catalog mutations atomically.
		_ = s.Store.DB.QueryRowContext(ctx, `SELECT processed FROM console_operations WHERE id=?`, j.ID).Scan(&j.Processed)
		if e != nil {
			s.finish(ctx, j, "failed", "maintenance failed", "maintenance-failed")
		} else {
			s.finish(ctx, j, "succeeded", "cleanup batch committed", "")
		}
	})
}

func (s *Scheduler) startAsyncStep(ctx context.Context, j Job, a Adapter) {
	s.mu.Lock()
	if _, active := s.running[j.ID]; active {
		s.mu.Unlock()
		return
	}
	if !s.lanes.holds(j.ID) && !s.lanes.tryAcquire(resourceLane(a), j.ID) {
		s.mu.Unlock()
		return
	}
	child, cancel := context.WithCancel(dbwork.WithClass(ctx, workClass(a)))
	s.running[j.ID] = cancel
	s.stepWG.Add(1)
	s.mu.Unlock()
	supervise.Go("operations.step", func() {
		defer s.stepWG.Done()
		defer func() { cancel(); s.laneSet().release(j.ID); s.mu.Lock(); delete(s.running, j.ID); s.mu.Unlock() }()
		v, e := a.Step(child, j.Resource)
		if e != nil {
			return
		}
		j.Processed = v.Processed
		j.Total = v.Total
		s.finish(child, j, v.State, v.Phase, v.ErrorCode)
	})
}
