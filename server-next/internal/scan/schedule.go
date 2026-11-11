package scan

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// How scans start by themselves.
const (
	// settle is how often changes seen in watched folders are turned into scans: a change is
	// picked up within about ten seconds, and a burst of changes is one scan.
	settle = 5 * time.Second
	// unwatchedEvery is how often a local folder that cannot be watched is scanned, and
	// networkEvery the same for a folder on a network share, unless the owner set an interval.
	unwatchedEvery = time.Hour
	networkEvery   = 6 * time.Hour
	// maxWatched is the most folders of one library that are watched. A larger library is
	// scanned on the interval instead.
	maxWatched = 100_000
)

// Run starts scans by themselves until ctx ends: every library once when the server starts
// (what changed while it was away), a library when one of its watched folders changes, a
// library whose rescan interval has passed, and a remote folder when its source is due. Once
// an hour it also empties, from every library's trash, the titles whose days are up, so that
// "after 30 days" does not wait for the next scan.
func (m *Manager) Run(ctx context.Context) {
	w := newWatcher()
	changed := make(chan string, 256)
	if w != nil {
		defer w.close()
		go w.run(ctx, func(library string) { changed <- library })
	}
	m.mu.Lock()
	m.watcher, m.changed = w, changed
	m.mu.Unlock()

	libraries, _ := m.scanner.DB.AllLibraries(ctx)
	for _, lib := range libraries {
		m.rewatch(ctx, lib, true)
		m.scheduled(ctx, lib.PublicID)
	}
	dirty := map[string]bool{}
	tick := time.NewTicker(settle)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case library := <-changed:
			dirty[library] = true
			continue
		case <-tick.C:
		}
		if len(dirty) > 0 {
			// A library whose watching was turned off since its folders were watched is left alone.
			libraries, _ := m.scanner.DB.AllLibraries(ctx)
			still := map[string]bool{}
			for _, lib := range libraries {
				// A change heard while a scan of the library is running may be in a folder
				// that scan has already passed: it stays pending until a new scan is queued.
				if dirty[lib.PublicID] && !lib.Options.NoWatch && !m.scheduled(ctx, lib.PublicID) {
					still[lib.PublicID] = true
				}
			}
			dirty = still
		}
		if n%int(time.Minute/settle) == 0 {
			m.startDue(ctx, time.Now())
		}
		if n%int(time.Hour/settle) == 0 {
			m.emptyTrash(ctx, time.Now())
		}
	}
}

// scheduled asks for a scan nobody asked for. It joins a scan of the library already waiting
// or running, and reports whether a scan that has yet to start is now waiting.
func (m *Manager) scheduled(ctx context.Context, library string) bool {
	// A scan that was cut short twice by the server dying is not started again by itself:
	// it may be what brings the server down. The owner's Scan now starts it afresh.
	if last, err := m.scanner.DB.JobFor(ctx, db.JobScan, library); err == nil && last.State == db.JobFailed && last.Error == jobs.Interrupted {
		return true
	}
	params, _ := json.Marshal(ScanParams{Scheduled: true})
	job, err := m.jobs.Start(ctx, db.JobScan, jobs.Request{Target: jobs.Target{Library: library}, Params: params})
	if err != nil && ctx.Err() == nil {
		m.scanner.log().Warn("a scan could not be started by itself", "library", library, "error", err)
	}
	return err != nil || job.State == db.JobWaiting
}

// startDue starts a scan of every library that has a folder due.
func (m *Manager) startDue(ctx context.Context, now time.Time) {
	libraries, err := m.scanner.DB.AllLibraries(ctx)
	if err != nil {
		return
	}
	for _, lib := range libraries {
		for _, p := range lib.Paths {
			if m.due(ctx, lib, p, now) {
				m.scheduled(ctx, lib.PublicID)
				break
			}
		}
	}
}

// due reports whether a folder is due a scan by itself. A local folder follows the library's
// rescan interval; with none set, a watched folder needs no interval, and one that cannot be
// watched is scanned every hour, or every six on a network share. A remote folder follows
// its source.
func (m *Manager) due(ctx context.Context, lib db.Library, p db.LibraryPath, now time.Time) bool {
	var last time.Time
	if p.LastScanAt != 0 {
		last = time.UnixMilli(p.LastScanAt)
	}
	if p.RemoteID != 0 {
		if m.scanner.Remote == nil {
			return false
		}
		_, every, err := m.scanner.Remote(ctx, p.RemoteID)
		return err == nil && remote.Due(last, every, now)
	}
	every := time.Duration(lib.Options.RescanMinutes) * time.Minute
	if every == 0 {
		m.mu.Lock()
		watched := m.watched[lib.PublicID]
		m.mu.Unlock()
		switch {
		case watched:
			return false
		case networkShare(p.Path):
			every = networkEvery
		default:
			every = unwatchedEvery
		}
	}
	return remote.Due(last, every, now)
}

// rewatch watches every folder of a library the last scan stored. A library that is not to
// be watched, has a folder on a network share or on remote storage, or has more folders
// than can be watched, is left to its interval.
func (m *Manager) rewatch(ctx context.Context, lib db.Library, first bool) {
	m.mu.Lock()
	w, changed := m.watcher, m.changed
	m.mu.Unlock()
	if w == nil {
		return
	}
	heardLate := false
	watched := !lib.Options.NoWatch && len(lib.Paths) > 0
	for _, p := range lib.Paths {
		watched = watched && p.RemoteID == 0 && !networkShare(p.Path)
	}
	if watched {
		folders, err := m.scanner.DB.LibraryDirectories(ctx, lib.ID, maxWatched+1)
		watched = err == nil && len(folders) <= maxWatched
		for _, folder := range folders {
			if !watched {
				break
			}
			var fresh bool
			watched, fresh = w.add(lib.PublicID, folder)
			heardLate = heardLate || fresh
		}
	}
	if heardLate && watched && !first {
		// A folder watched only from now may have changed since the scan listed it: files
		// still arriving in a folder that is new. One more scan sees them.
		select {
		case changed <- lib.PublicID:
		default:
		}
	}
	m.mu.Lock()
	m.watched[lib.PublicID] = watched
	m.mu.Unlock()
}

// emptyTrash removes, from every library that removes missing titles after a number of days,
// the ones whose days are up.
func (m *Manager) emptyTrash(ctx context.Context, now time.Time) {
	libraries, err := m.scanner.DB.AllLibraries(ctx)
	if err != nil {
		return
	}
	for _, lib := range libraries {
		if mode := lib.Options.Trash.Mode; mode != "" && mode != db.TrashAfterDays {
			continue
		}
		if cutoff, ok := lib.Options.Trash.Cutoff(now); ok {
			if _, err := m.scanner.DB.EmptyTrash(ctx, lib.ID, cutoff); err != nil && ctx.Err() == nil {
				m.scanner.log().Warn("the trash was not emptied", "library", lib.Name, "error", err)
			}
		}
	}
}
