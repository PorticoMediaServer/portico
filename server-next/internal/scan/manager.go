package scan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// Manager is the library work that runs as jobs: scans and library removals, one at a time
// in the order they were asked for. What a scan has counted so far is kept here, in memory.
type Manager struct {
	scanner *Scanner
	jobs    *jobs.Runner

	mu    sync.Mutex
	stats map[string]Stats // by library, for the scan running or last run since the server started
}

// library is the lane scans and library removals share, so that a library is never removed
// under a scan of it.
const library = "library"

// NewManager registers the scan kinds with the job runner. A scan is the owner's to start
// (Scan now, Scan all files); removing a library is started only by its own route, which
// asks for the library's name first.
func NewManager(runner *jobs.Runner, scanner *Scanner) *Manager {
	m := &Manager{scanner: scanner, jobs: runner, stats: map[string]Stats{}}
	exists := func(ctx context.Context, req jobs.Request) error {
		_, err := m.library(ctx, req.Target.Library)
		return err
	}
	runner.Register(jobs.Kind{Name: db.JobScan, Access: jobs.Owner, Lane: library, Check: exists, Run: m.scan})
	runner.Register(jobs.Kind{Name: db.JobDeleteLibrary, Access: jobs.Internal, Lane: library, Check: exists,
		Run: func(ctx context.Context, work jobs.Work) error {
			lib, err := m.library(ctx, work.Target.Library)
			if errors.Is(err, db.ErrNotFound) {
				return nil // already removed
			}
			if err != nil {
				return err
			}
			return scanner.DB.DeleteLibrary(ctx, lib.ID)
		}})
	return m
}

// ScanParams are the parameters of a scan job.
type ScanParams struct {
	// AllFiles reads every file again, changed or not.
	AllFiles bool `json:"allFiles,omitempty"`
}

// Scan asks for a scan of a library. While one is waiting or running, asking again joins it.
// With all set, the scan reads every file again, changed or not.
func (m *Manager) Scan(ctx context.Context, libraryID string, ownerID int64, all bool) (db.Job, error) {
	params, _ := json.Marshal(ScanParams{AllFiles: all})
	return m.jobs.Start(ctx, db.JobScan, jobs.Request{Target: jobs.Target{Library: libraryID}, Params: params, UserID: ownerID})
}

// DeleteLibrary asks for a library to be removed, after stopping its scan.
func (m *Manager) DeleteLibrary(ctx context.Context, libraryID string, ownerID int64) (db.Job, error) {
	if scan, err := m.scanner.DB.JobFor(ctx, db.JobScan, libraryID); err == nil {
		if err := m.jobs.Cancel(ctx, scan); err != nil {
			return db.Job{}, err
		}
	}
	return m.jobs.Start(ctx, db.JobDeleteLibrary, jobs.Request{Target: jobs.Target{Library: libraryID}, UserID: ownerID})
}

// Cancel stops a job. Everything a scan stored so far is kept.
func (m *Manager) Cancel(ctx context.Context, job db.Job) error { return m.jobs.Cancel(ctx, job) }

// Stats is what the scan of a library has counted: the one running, or the last one since
// the server started.
func (m *Manager) Stats(libraryID string) (Stats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats, ok := m.stats[libraryID]
	return stats, ok
}

func (m *Manager) library(ctx context.Context, publicID string) (db.Library, error) {
	libraries, err := m.scanner.DB.AllLibraries(ctx)
	if err != nil {
		return db.Library{}, err
	}
	for _, lib := range libraries {
		if lib.PublicID == publicID && publicID != "" {
			return lib, nil
		}
	}
	return db.Library{}, db.ErrNotFound
}

func (m *Manager) scan(ctx context.Context, work jobs.Work) error {
	lib, err := m.library(ctx, work.Target.Library)
	if errors.Is(err, db.ErrNotFound) {
		return nil // removed since the scan was asked for
	}
	if err != nil {
		return err
	}
	var params ScanParams
	if len(work.Params) > 0 {
		if err := json.Unmarshal(work.Params, &params); err != nil {
			return err
		}
	}
	if params.AllFiles {
		if err := m.scanner.DB.ForgetProbes(ctx, lib.ID); err != nil {
			return err
		}
	}
	scanner := *m.scanner
	scanner.Progress = func(stats Stats) {
		m.mu.Lock()
		m.stats[lib.PublicID] = stats
		m.mu.Unlock()
		work.Progress(int64(stats.Files), 0)
	}
	stats, err := scanner.Scan(ctx, lib)
	m.mu.Lock()
	m.stats[lib.PublicID] = stats
	m.mu.Unlock()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
