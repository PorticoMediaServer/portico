package scan

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Manager runs scans and library removals as jobs, one at a time, in the order they were
// asked for. The job rows are in the database, so work asked for before a restart is done
// after it; what a scan has counted so far is kept here, in memory.
type Manager struct {
	scanner *Scanner
	ctx     context.Context

	mu      sync.Mutex
	queue   []db.Job
	working bool
	current int64 // the job being run
	stop    context.CancelFunc
	stats   map[string]Stats // by library, for the scan running or last run since the server started
}

// NewManager makes a manager whose work stops when ctx ends.
func NewManager(ctx context.Context, scanner *Scanner) *Manager {
	return &Manager{scanner: scanner, ctx: ctx, stats: map[string]Stats{}}
}

// Resume takes up the jobs that were waiting or running when the server last stopped.
func (m *Manager) Resume() error {
	jobs, err := m.scanner.DB.UnfinishedJobs(m.ctx)
	for _, job := range jobs {
		if job.Kind == db.JobScan || job.Kind == db.JobDeleteLibrary {
			m.enqueue(job)
		}
	}
	return err
}

// allFiles is the payload of a scan that reads every file again, changed or not.
const allFiles = "all-files"

// Scan asks for a scan of a library. While one is waiting or running, asking again joins it.
// With all set, the scan reads every file again, changed or not.
func (m *Manager) Scan(ctx context.Context, libraryID string, ownerID int64, all bool) (db.Job, error) {
	payload := ""
	if all {
		payload = allFiles
	}
	return m.start(ctx, db.JobScan, libraryID, payload, ownerID)
}

// DeleteLibrary asks for a library to be removed, after stopping its scan.
func (m *Manager) DeleteLibrary(ctx context.Context, libraryID string, ownerID int64) (db.Job, error) {
	if scan, err := m.scanner.DB.JobFor(ctx, db.JobScan, libraryID); err == nil {
		if err := m.Cancel(ctx, scan); err != nil {
			return db.Job{}, err
		}
	}
	return m.start(ctx, db.JobDeleteLibrary, libraryID, "", ownerID)
}

func (m *Manager) start(ctx context.Context, kind, target, payload string, ownerID int64) (db.Job, error) {
	job, err := m.scanner.DB.StartJob(ctx, kind, target, payload, ownerID)
	if err == nil && job.State == db.JobWaiting {
		m.enqueue(job)
	}
	return job, err
}

func (m *Manager) enqueue(job db.Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, queued := range m.queue {
		if queued.ID == job.ID {
			return
		}
	}
	if m.current == job.ID {
		return
	}
	m.queue = append(m.queue, job)
	if !m.working {
		m.working = true
		go m.work()
	}
}

// Cancel stops a job. Everything a scan stored so far is kept.
func (m *Manager) Cancel(ctx context.Context, job db.Job) error {
	m.mu.Lock()
	if m.current == job.ID && m.stop != nil {
		m.stop()
	}
	for i, queued := range m.queue {
		if queued.ID == job.ID {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	return m.scanner.DB.FinishJob(ctx, job.ID, db.JobCancelled, "")
}

// Stats is what the scan of a library has counted: the one running, or the last one since
// the server started.
func (m *Manager) Stats(libraryID string) (Stats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats, ok := m.stats[libraryID]
	return stats, ok
}

func (m *Manager) work() {
	for {
		m.mu.Lock()
		if len(m.queue) == 0 || m.ctx.Err() != nil {
			m.working = false
			m.mu.Unlock()
			return
		}
		job := m.queue[0]
		m.queue = m.queue[1:]
		ctx, stop := context.WithCancel(m.ctx)
		m.current, m.stop = job.ID, stop
		m.mu.Unlock()

		err := m.run(ctx, job)
		stopped := ctx.Err() != nil
		stop()
		m.mu.Lock()
		m.current, m.stop = 0, nil
		m.mu.Unlock()
		switch {
		case stopped && m.ctx.Err() != nil:
			// The server is stopping: the job stays as it is and is run again at the next start.
		case stopped:
			m.scanner.DB.FinishJob(m.ctx, job.ID, db.JobCancelled, "")
		case err != nil:
			m.scanner.log().Error("job failed", "kind", job.Kind, "target", job.Target, "error", err)
			m.scanner.DB.FinishJob(m.ctx, job.ID, db.JobFailed, err.Error())
		default:
			m.scanner.DB.FinishJob(m.ctx, job.ID, db.JobDone, "")
		}
	}
}

func (m *Manager) run(ctx context.Context, job db.Job) error {
	d := m.scanner.DB
	if ran, err := d.RunJob(ctx, job.ID); err != nil || !ran {
		return err
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		return err
	}
	var lib *db.Library
	for i := range libraries {
		if libraries[i].PublicID == job.Target {
			lib = &libraries[i]
		}
	}
	if lib == nil {
		return nil // removed since the job was asked for
	}
	if job.Kind == db.JobDeleteLibrary {
		return d.DeleteLibrary(ctx, lib.ID)
	}
	if job.Payload == allFiles {
		if err := d.ForgetProbes(ctx, lib.ID); err != nil {
			return err
		}
	}
	scanner := *m.scanner
	var reported time.Time
	scanner.Progress = func(stats Stats) {
		m.mu.Lock()
		m.stats[lib.PublicID] = stats
		m.mu.Unlock()
		if time.Since(reported) > 2*time.Second {
			reported = time.Now()
			d.JobProgress(ctx, job.ID, int64(stats.Files), 0)
		}
	}
	stats, err := scanner.Scan(ctx, *lib)
	m.mu.Lock()
	m.stats[lib.PublicID] = stats
	m.mu.Unlock()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
