package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"sync"
	"time"
)

type Job struct {
	ScanMode  string   `json:"scanMode,omitempty"`
	ID        string   `json:"id"`
	SourceID  string   `json:"sourceId,omitempty"`
	Status    string   `json:"status"`
	Phase     string   `json:"phase,omitempty"`
	Processed int      `json:"processed"`
	Analyzed  int      `json:"analyzed"`
	Warnings  int      `json:"warnings"`
	Error     string   `json:"error,omitempty"`
	Jobs      []string `json:"jobs,omitempty"`
}
type Service struct {
	// remoteListings is where remote listing retention has got to per folder.
	remoteListings remoteListingCursors
	// Memory-only fair scheduling cursor; durable progress remains per source.
	verificationAfter    string
	verificationHasAfter bool

	// The episode stage publishes artwork on this same durable inventory job.
	EpisodeThumbnails func(context.Context, string, string, string, string, int64) (bool, string, error)

	Admission     func(context.Context, *sql.Tx, string) (bool, error)
	Subtitles     *subtitles.Service
	DeepAnalysis  *mediaanalysis.Service
	LocalMetadata *localmetadata.Service
	AnalyzeSTRM   func(context.Context, string, string) (assets.Facts, error)

	strictIntentAfter   string
	strictIntentRetry   time.Time
	strictIntentFolders map[string]string

	db            *sql.DB
	catalog       *catalog.Service
	probe         assets.Probe
	storage       *storage.Client
	mu            sync.Mutex
	active        map[string]context.CancelFunc
	activeSources map[string]bool
	activityFiles map[string]*activityFileObservation
	// analysingSince is when this process first ran a basic-analysis quantum for
	// a job; reports waiting for its source take over the source only after it
	// has analysed this long (reportHandoverAfter).
	analysingSince map[string]time.Time
	listingMu      sync.Mutex
	listings       map[string]*scanListing
	ignoreScopes   map[string]map[string]remoteIgnoreScope
}

func New(db *sql.DB, c *catalog.Service, p assets.Probe) *Service {
	return &Service{db: db, catalog: c, probe: p, active: map[string]context.CancelFunc{}, activeSources: map[string]bool{}}
}
func (s *Service) SetStorage(c *storage.Client) { s.storage = c }
func (s *Service) Queue(library string) (Job, error) {
	return s.QueueContext(context.Background(), library, "", nil)
}
func (s *Service) QueueContext(ctx context.Context, library, sourceID string, authorize func(*sql.Tx) error) (Job, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return Job{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return Job{}, err
		}
	}
	j, err := s.queueTx(ctx, tx, library, sourceID)
	if err != nil {
		return Job{}, err
	}
	if err = gated.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}

// QueueTx lets the console commit its receipt and the real durable source jobs
// atomically. It shares source-level deduplication with manual/interval scans.
func (s *Service) QueueTx(ctx context.Context, tx *sql.Tx, library string) (Job, error) {
	return s.queueTx(ctx, tx, library, "")
}

// QueueSourceTx commits one attached source's initial scan with its
// configuration, sharing source-level deduplication with manual scans.
func (s *Service) QueueSourceTx(ctx context.Context, tx *sql.Tx, library, sourceID string) (Job, error) {
	return s.queueTx(ctx, tx, library, sourceID)
}
func (s *Service) queueTx(ctx context.Context, tx *sql.Tx, library, sourceID string) (Job, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM library_sources WHERE library_id=? AND enabled=1 AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=library_sources.library_id) AND (?='' OR id=?) ORDER BY CASE WHEN id=library_id THEN 0 ELSE 1 END,id`, library, sourceID, sourceID)
	if err != nil {
		return Job{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Job{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Job{}, err
	}
	if len(ids) == 0 {
		return Job{}, sql.ErrNoRows
	}
	var first Job
	all := []string{}
	for _, id := range ids {
		j, e := queueSourceTx(ctx, tx, id, false)
		if e != nil {
			return Job{}, e
		}
		if first.ID == "" {
			first = j
		}
		all = append(all, j.ID)
	}
	first.Jobs = all
	return first, nil
}
func queueSourceTx(ctx context.Context, tx *sql.Tx, source string, analysisOnly bool) (Job, error) {
	var j Job
	j.SourceID = source
	err := tx.QueryRowContext(ctx, `SELECT j.id,j.status,j.processed,r.phase,r.analyzed,r.warnings FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id JOIN inventory_runs r ON r.job_id=j.id WHERE a.source_id=? AND j.status IN('queued','running','paused')`, source).Scan(&j.ID, &j.Status, &j.Processed, &j.Phase, &j.Analyzed, &j.Warnings)
	if err == nil {
		return j, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return j, err
	}
	var library, incarnation, rootIdentity string
	var generation, policy int64
	err = tx.QueryRowContext(ctx, `SELECT s.library_id,s.generation,s.incarnation,s.root_identity,p.revision FROM library_sources s JOIN library_scan_policies p ON p.library_id=s.library_id WHERE s.id=? AND s.enabled=1 AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id)`, source).Scan(&library, &generation, &incarnation, &rootIdentity, &policy)
	if err != nil {
		return j, err
	}
	j.ID, j.Status, j.Phase = identity.Token(), "queued", "inventory"
	if analysisOnly {
		j.Phase = "analysis_enqueue"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'queued',?)`, j.ID, library, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return j, err
	}
	mode, err := scanModeTx(ctx, tx, source)
	if err != nil {
		return j, err
	}
	j.ScanMode = mode
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode) VALUES(?,?,?,?,?,?,?,?)`, j.ID, source, generation, incarnation, rootIdentity, policy, j.Phase, mode); err != nil {
		return j, err
	}
	if !analysisOnly {
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,'.')`, j.ID); err != nil {
			return j, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET job_id=excluded.job_id`, source, j.ID); err != nil {
		return j, err
	}
	return j, nil
}
func (s *Service) Get(id string) (Job, error) {
	var j Job
	err := s.db.QueryRow(`SELECT j.id,j.status,j.processed,j.error,COALESCE(r.source_id,''),COALESCE(r.phase,''),COALESCE(r.analyzed,0),COALESCE(r.warnings,0) FROM jobs j LEFT JOIN inventory_runs r ON r.job_id=j.id WHERE j.id=?`, id).Scan(&j.ID, &j.Status, &j.Processed, &j.Error, &j.SourceID, &j.Phase, &j.Analyzed, &j.Warnings)
	if j.Error != "" && j.Error != sourceUnavailable {
		j.Error = "Scan needs attention. Existing inventory was retained."
	}
	return j, err
}
func (s *Service) Cancel(id string) error { return s.Control(context.Background(), id, "cancel", nil) }
func (s *Service) Control(ctx context.Context, id, action string, authorize func(*sql.Tx) error) error {
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	if err = controlJobTx(ctx, tx, id, action); err != nil {
		return err
	}
	if err = gated2.Commit(); err != nil {
		return err
	}
	if action != "resume" {
		s.stopActive(id)
	}
	if action == "cancel" {
		s.forgetListing(id)
	}
	return nil
}
func controlJobTx(ctx context.Context, tx *sql.Tx, id, action string) error {
	var status string
	var err error
	if err = tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id).Scan(&status); err != nil {
		return err
	}
	switch action {
	case "pause":
		if status == "queued" || status == "running" {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET status='paused' WHERE id=?`, id)
		} else if status != "paused" {
			return catalog.ErrAdminQuery
		}
	case "resume":
		if status == "paused" {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET status='queued' WHERE id=?`, id)
		} else if status != "queued" && status != "running" {
			return catalog.ErrAdminQuery
		}
	case "cancel":
		if status == "queued" || status == "running" || status == "paused" {
			if _, err = tx.ExecContext(ctx, `UPDATE jobs SET status='cancelled' WHERE id=?`, id); err == nil {
				_, err = tx.ExecContext(ctx, `DELETE FROM inventory_source_active WHERE job_id=?`, id)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE library_sources SET health='cancelled' WHERE id IN(SELECT source_id FROM inventory_runs WHERE job_id=?) AND health='inventory_running'`, id)
			}
		}
	default:
		return catalog.ErrAdminQuery
	}
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) Retry(ctx context.Context, id string, authorize func(*sql.Tx) error) (Job, error) {
	var library, source, status string
	err := s.db.QueryRowContext(ctx, `SELECT j.library_id,r.source_id,j.status FROM jobs j JOIN inventory_runs r ON r.job_id=j.id WHERE j.id=?`, id).Scan(&library, &source, &status)
	if err != nil {
		return Job{}, err
	}
	if status != "failed" && status != "cancelled" && status != "complete" && status != "complete_with_warnings" {
		return Job{}, catalog.ErrAdminQuery
	}
	return s.QueueContext(ctx, library, source, authorize)
}
func (s *Service) stopActive(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.active[id]; c != nil {
		c()
	}
}

// PolicyChanged preempts in-flight old-policy reads and queues only missing
// analysis for current revisions, without repeating unchanged inventory.
func (s *Service) PolicyChanged(ctx context.Context, library string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT a.job_id FROM inventory_source_active a JOIN library_sources s ON s.id=a.source_id WHERE s.library_id=?`, library)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.stopActive(id)
	}
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	// An active inventory naturally picks up the new policy at every page/read.
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='analysis_enqueue',reconcile_cursor='' WHERE job_id IN(SELECT id FROM jobs WHERE library_id=? AND status IN('queued','running','paused')) AND phase IN('basic_running','basic_pending','analysis_enqueue')`, library); err != nil {
		return err
	}
	sources, err := tx.QueryContext(ctx, `SELECT id FROM library_sources WHERE library_id=? AND enabled=1 AND EXISTS(SELECT 1 FROM inventory_objects o WHERE o.source_id=library_sources.id AND o.root_incarnation=library_sources.incarnation AND o.retired=0) AND NOT EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=library_sources.id)`, library)
	if err != nil {
		return err
	}
	ids = ids[:0]
	for sources.Next() {
		var id string
		if err = sources.Scan(&id); err != nil {
			sources.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = sources.Err()
	sources.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = queueSourceTx(ctx, tx, id, true); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_policy_pending WHERE library_id=? AND revision<=(SELECT revision FROM library_scan_policies WHERE library_id=?)`, library, library); err != nil {
		return err
	}
	return gated3.Commit()
}

// quantumPoll is how soon the loop looks again while a quantum is in flight
// that has not said it advanced: a long read, a refusal, a wait on playback.
var quantumPoll = 300 * time.Millisecond

// titleCatchUp is the longest one scan step waits for derived work to catch up
// with the titles it found (compactcatalog.AwaitTitles).
var titleCatchUp = time.Second

// Focused lifecycle seams. Production keeps the real monitor, the real quantum
// and a no-op iteration hook; tests use them to prove a Run owns its children so
// an iteration panic can reach the outer supervisor only after every child of
// this generation has stopped.
var (
	monitorRun = func(s *Service, ctx context.Context) { s.runMonitor(ctx) }
	quantumRun = func(s *Service, ctx context.Context, id, library string) bool {
		return s.processQuantum(ctx, id, library)
	}
	iterationHook = func(context.Context) {}
)

func (s *Service) Run(ctx context.Context) {
	// This Run owns a cancellable generation: the monitor, the worker loop and
	// every quantum derive from it. An iteration panic must stop all of them
	// before the outer supervisor restarts the scanner, so the supplied parent
	// is never cancelled and no child is detached.
	life, stopLife := context.WithCancel(ctx)
	// Everything this loop writes is bulk catalogue work, so it queues behind
	// playback, authentication and interactive edits at the write gate.
	life = dbwork.WithClass(life, dbwork.ClassBackgroundMedia)
	monitorDone := make(chan struct{})
	supervise.Go("ingestion.monitor", func() { defer close(monitorDone); monitorRun(s, life) })
	verificationDone := make(chan struct{})
	supervise.Go("ingestion.verify", func() { defer close(verificationDone); s.runSourceVerification(life) })
	var workers sync.WaitGroup
	defer func() {
		// One cleanup, in order: cancel this generation (which also releases the
		// monitor, the verification walk and the worker loop), cancel the active
		// quantums, then join them all. A panic continues to the supervisor only
		// after this returns.
		stopLife()
		s.mu.Lock()
		for _, cancel := range s.active {
			cancel()
		}
		s.mu.Unlock()
		workers.Wait()
		<-monitorDone
		<-verificationDone
	}()
	// A scan is enqueued by a write, so the loop is woken by one. With nothing
	// queued it asked the database three times a second for the life of the
	// process; now it waits.
	wake := workerloop.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "jobs", "inventory_*", "library_sources", "scan_policies", "maintenance_*", "server_settings", "console_settings_revision")
	defer unregister()
	// A quantum that moved its run forward asks for the next one itself, after
	// giving way to any foreground work. The commit wake above is shared and
	// floored, so on its own it paced every source at one bounded step per poll
	// however idle the machine was.
	advanced := workerloop.NewSignal()
	pruneAfter := ""
	nextPrune := time.Time{}
	workerloop.RunWith(life, "ingestion.scan", advanced, wake, func(ctx context.Context) time.Duration {
		iterationHook(ctx)
		pending := false
		intentPending := false
		{
			ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, s.db))
			s.recoverPolicyChanges(ctx)
			s.schedule(ctx)
			intentPending = s.startStrictIntents(ctx)
			s.startWaitingReports(ctx)
			if !time.Now().Before(nextPrune) {
				var source string
				if e := s.db.QueryRowContext(ctx, `SELECT id FROM library_sources WHERE id>? ORDER BY id LIMIT 1`, pruneAfter).Scan(&source); e == nil {
					more, _ := s.PruneSource(ctx, source)
					if more {
						advanced.Wake()
					} else {
						pruneAfter = source
					}
				} else {
					pruneAfter = ""
					nextPrune = time.Now().Add(time.Minute)
				}
			}
			rows, err := s.db.QueryContext(ctx, `SELECT j.id,j.library_id,r.source_id,r.phase FROM jobs j JOIN inventory_runs r ON r.job_id=j.id WHERE j.status IN('queued','running') ORDER BY r.last_turn,j.created_at,j.id LIMIT 16`)
			if err != nil {
				return time.Second
			}
			type candidate struct{ id, library, source, phase string }
			batch := []candidate{}
			for rows.Next() {
				var c candidate
				if rows.Scan(&c.id, &c.library, &c.source, &c.phase) == nil {
					batch = append(batch, c)
				}
			}
			rows.Close()
			pending = len(batch) > 0
			for _, c := range batch {
				if s.Admission != nil {
					task := "library-scan"
					if c.phase == "basic_pending" || c.phase == "basic_running" || c.phase == "analysis_enqueue" {
						task = "analysis"
					}
					allowed, err := s.Admission(ctx, nil, task)
					if err != nil || !allowed {
						continue
					}
				}
				s.mu.Lock()
				busy := len(s.active) >= dbwork.BackgroundConcurrency(ctx) || s.active[c.id] != nil || s.activeSources[c.source]
				if !busy {
					s.activeSources[c.source] = true
					work, cancel := context.WithCancel(ctx)
					s.active[c.id] = cancel
					workers.Add(1)
					supervise.Go("ingestion.quantum", func() {
						defer workers.Done()
						more := false
						defer func() {
							cancel()
							s.mu.Lock()
							delete(s.active, c.id)
							delete(s.activeSources, c.source)
							s.mu.Unlock()
							if more {
								advanced.Wake()
							}
						}()
						started := time.Now()
						more = quantumRun(s, work, c.id, c.library)
						// The next step follows at once only when nothing in the
						// foreground is in flight. Otherwise the scan first gives
						// way, for the bounded pause every continuous bulk loop
						// takes between batches: deprioritised, never stopped. The
						// step's transactions are closed by now, and the source
						// stays claimed, so nothing else runs it meanwhile.
						if more {
							more = dbwork.PaceBackground(work, started)
						}
						// Titles found must become browsable as the scan goes,
						// not after it: the scan lets derived work catch up
						// when too many titles wait for it.
						if more {
							more = compactcatalog.AwaitTitles(work, s.db, titleCatchUp)
						}
					})
				}
				s.mu.Unlock()
			}
		}
		// Anything in flight means come back and give it another quantum; an idle
		// scanner waits for the write that enqueues the next job.
		s.mu.Lock()
		active := len(s.active)
		s.mu.Unlock()
		if active > 0 {
			return quantumPoll
		}
		if intentPending {
			return 2 * time.Second
		}
		if pending {
			return time.Minute
		}
		// A real scheduled scan supplies its deadline; with none, wait for a
		// source/policy commit or the worker safety backstop. Polling the
		// database every minute on an idle server bought nothing.
		return s.nextScheduledScan(ctx)
	})
}

// Kept for focused local tests and existing package callers; the production
// worker uses a per-source bounded pool rather than a serial scan goroutine.
func (s *Service) process(ctx context.Context, id, library string) {
	for n := 0; n < 32 && ctx.Err() == nil; n++ {
		s.processQuantum(ctx, id, library)
		j, err := s.Get(id)
		if err != nil || j.Status != "running" && j.Status != "queued" {
			return
		}
	}
}

// scanPosition is where a run durably stands: its phase and cursors, its
// counters, and the head of its pending directory and analysis queues.
type scanPosition struct {
	status, phase, verify, reconcile, directory, cursor, object, sidecar, prune string
	processed, analyzed, warnings                                               int64
}

func (s *Service) scanPosition(ctx context.Context, id string) (scanPosition, error) {
	var p scanPosition
	err := s.db.QueryRowContext(ctx, `SELECT j.status,r.phase,r.verify_cursor,r.reconcile_cursor,j.processed,r.analyzed,r.warnings,COALESCE((SELECT d.relative_path FROM inventory_directories d WHERE d.job_id=r.job_id AND d.state='pending' ORDER BY d.relative_path LIMIT 1),''),COALESCE((SELECT d.cursor FROM inventory_directories d WHERE d.job_id=r.job_id AND d.state='pending' ORDER BY d.relative_path LIMIT 1),''),COALESCE((SELECT q.object_id FROM inventory_analysis_queue q WHERE q.job_id=r.job_id AND q.state='pending' ORDER BY q.object_id LIMIT 1),''),COALESCE((SELECT c.relative_path||':'||c.cursor FROM inventory_sidecar_changes c WHERE c.job_id=r.job_id ORDER BY c.relative_path LIMIT 1),''),COALESCE((SELECT k.job_id||':'||k.parent_path||':'||k.cursor_parent_revision||':'||k.cursor_relative_path FROM inventory_directory_prune k INDEXED BY inventory_directory_prune_due WHERE k.source_id=r.source_id AND k.incarnation=r.root_incarnation AND k.state='ready' ORDER BY k.job_id,k.parent_path LIMIT 1),'') FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id=?`, id).Scan(&p.status, &p.phase, &p.verify, &p.reconcile, &p.processed, &p.analyzed, &p.warnings, &p.directory, &p.cursor, &p.object, &p.sidecar, &p.prune)
	return p, err
}

// processQuantum reports whether the run durably advanced and is still
// running, which is the only case in which the next quantum is due at once. A
// refused, failed, preempted or waiting quantum reports false and is looked at
// again on the loop's own schedule.
func (s *Service) processQuantum(ctx context.Context, id, library string) bool {
	ctx = dbwork.WithCatalogIngest(ctx)
	if err := dbwork.WaitCatalogIngest(ctx, s.db, dbwork.ClassBackgroundMedia); err != nil {
		return false
	}
	if s.Admission != nil {
		var phase string
		if err := s.db.QueryRowContext(ctx, `SELECT phase FROM inventory_runs WHERE job_id=?`, id).Scan(&phase); err != nil {
			return false
		}
		task := "library-scan"
		if phase == "basic_pending" || phase == "basic_running" || phase == "analysis_enqueue" {
			task = "analysis"
		}
		allowed, err := s.Admission(ctx, nil, task)
		if err != nil || !allowed {
			return false
		}
	}

	// One quantum is one bounded page. Yielding here, before any transaction is
	// open, gives navigation and playback control an uncontested window and then
	// lets exactly one unit of scan progress through: throttle, never starve.
	if !dbwork.Yield(ctx) {
		return false
	}
	// A panic-injection point for a background batch: the scan must survive one
	// quantum failing, and the job must not be left claimed.
	supervise.Chaos("ingestion.quantum")
	// The claim is one transaction: the job becomes running and the run's
	// fairness turn moves together, or neither does. Grouping exactly these two
	// statements removes one writer admission per quantum without changing what
	// either means. The conditional status update is unchanged, so a pause or
	// cancel that committed first is still not overwritten and the read below
	// still sees the committed status. The turn is sampled immediately after the
	// status statement, inside the writer, so it records entry to this quantum
	// rather than time spent queued for the gate. If either statement fails both
	// roll back, which leaves the job truthfully queued instead of possibly
	// running with no work in flight; no work has begun at this point either way.
	if err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running' WHERE id=? AND status='queued'`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE inventory_runs SET last_turn=? WHERE job_id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	}); err != nil {
		return false
	}
	var sourceID, status, phase string
	if s.db.QueryRowContext(ctx, `SELECT r.source_id,j.status,r.phase FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id=?`, id).Scan(&sourceID, &status, &phase) != nil || status != "running" {
		return false
	}
	source, err := s.catalog.LibrarySource(ctx, sourceID)
	if err != nil {
		return false
	}
	if err = s.readySource(ctx, id, &source); err != nil {
		if !quantumRetryable(ctx, err) {
			s.fail(ctx, id, sourceID, err)
		}
		return false
	}
	before, err := s.scanPosition(ctx, id)
	if err != nil {
		return false
	}
	// One page or one analysis unit per turn; a hung mount consumes at most one
	// source slot and cannot hold the only worker or any database transaction.
	switch phase {
	case "inventory":
		err = s.inventoryPage(ctx, id, source)
	case "legacy_inventory":
		err = s.adoptLegacyPage(ctx, id, source)
	case "extras":
		err = s.attachExtrasPage(ctx, id, source)
	case "verifying":
		err = s.verifyPage(ctx, id, source)
	case "reconciling":
		err = s.reconcilePage(ctx, id, source)
	case "sidecars":
		err = s.reconcileSidecars(ctx, id, source)
	case "analysis_enqueue":
		err = s.enqueueAnalysisPage(ctx, id, source)
	case "basic_pending", "basic_running":
		var handed bool
		if handed, err = s.handOverToReports(ctx, id, source); err == nil && !handed {
			err = s.analyzeOne(ctx, id, source)
		}
	default:
		err = catalog.ErrAdminQuery
	}
	if err == nil {
		err = s.readingClosureQuantum(ctx, id, source)
	}
	if err != nil && !quantumRetryable(ctx, err) {
		log.Printf("library scan step %s failed: %v", phase, err)
		s.fail(ctx, id, sourceID, err)
	}
	if err != nil || ctx.Err() != nil {
		return false
	}
	after, err := s.scanPosition(ctx, id)
	if err == nil && after.status != "running" && after.status != "queued" && after.status != "paused" {
		s.forgetListing(id)
		s.forgetAnalysing(id)
	}
	return err == nil && after != before && after.status == "running"
}

// quantumRetryable reports a refusal that changed nothing and ends on its own,
// which the loop revisits on its ordinary schedule instead of failing the run.
// These are: the quantum's own cancellation, a storage source busy with another
// operation, and database work the gate or a WAL reset turned away before it
// began — a read refused while readers are paused for recovery
// (dbwork.ErrReadPressure), a full writer ticket pool, or a lock the gate could
// not resolve (dbwork.Overloaded). None of them describes the source, so a
// failure would discard the run's durable cursor and pending work for a
// condition that clears without intervention. Corruption, constraints, a full
// volume, an expired snapshot deadline and unknown failures stay fatal.
func quantumRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrBusy) {
		return true
	}
	return errors.Is(err, dbwork.ErrReadPressure) || dbwork.Overloaded(err)
}

func (s *Service) InterruptSource(ctx context.Context, source string) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.active))
	for id := range s.active {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		var owner string
		if s.db.QueryRowContext(ctx, `SELECT source_id FROM inventory_runs WHERE job_id=?`, id).Scan(&owner) == nil && owner == source {
			s.stopActive(id)
		}
	}
}

func (s *Service) recoverPolicyChanges(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT library_id FROM inventory_policy_pending ORDER BY library_id LIMIT 8`)
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		_ = s.PolicyChanged(ctx, id)
	}
}

// InterruptLibrary stops every scan of a library that is running in this
// process (the library was removed).
func (s *Service) InterruptLibrary(ctx context.Context, library string) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.active))
	for id := range s.active {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		var owner string
		if s.db.QueryRowContext(ctx, `SELECT library_id FROM jobs WHERE id=?`, id).Scan(&owner) == nil && owner == library {
			s.stopActive(id)
		}
	}
}
