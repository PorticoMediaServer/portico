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
	// unwatchedEvery is how often a local folder is scanned that the owner wants watched and
	// that cannot be, and networkEvery the same for a folder on a network share, unless the
	// owner set an interval.
	unwatchedEvery = time.Hour
	networkEvery   = 6 * time.Hour
	// maxWatched is the most folders of one library that are watched. A larger library is
	// scanned on the interval instead.
	maxWatched = 100_000
)

// Run starts scans by themselves until ctx ends, as the owner chose for each library: when
// one of its watched folders changes, when its rescan interval has passed, both or neither;
// and a remote folder when its source is due. Starting the server scans nothing by itself,
// except a library whose interval passed while the server was off.
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
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	libraries, _ := m.scanner.DB.AllLibraries(ctx)
	for _, lib := range libraries {
		m.rewatch(ctx, lib, true)
	}
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	m.startDue(ctx, time.Now())
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

// FolderSchedule says how a folder of a library on this machine's disks is kept up to date
// by itself: whether it is watched, and how often it is scanned (0 for never). Where the
// owner wants the library watched and it cannot be, the scan every hour, or every six on a
// network share, that stands in is the interval reported. ok is false until the schedule is
// running and the library has been looked at, and for a folder on remote storage, which
// follows its source.
func (m *Manager) FolderSchedule(lib db.Library, p db.LibraryPath) (watched bool, every time.Duration, ok bool) {
	m.mu.Lock()
	watched, known := m.watched[lib.PublicID]
	m.mu.Unlock()
	// Nothing is said about a library whose folders have not been looked at for watching
	// yet: "not watched" would be a guess, and a wrong one a moment later.
	watched, ok = watched && !lib.Options.NoWatch, m.running && known && p.RemoteID == 0
	every = time.Duration(lib.Options.RescanMinutes) * time.Minute
	if every == 0 && !watched && !lib.Options.NoWatch {
		if every = unwatchedEvery; networkShare(p.Path) {
			every = networkEvery
		}
	}
	return watched, every, ok
}

// due reports whether a folder is due a scan by itself. A local folder follows the library's
// rescan interval. With none set it is never due, with one exception: where the owner wants
// the library watched and its folders cannot be, a scan every hour, or every six on a network
// share, stands in for the watching. A remote folder follows its source.
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
	m.mu.Lock()
	watched := m.watched[lib.PublicID]
	m.mu.Unlock()
	every := time.Duration(lib.Options.RescanMinutes) * time.Minute
	if every == 0 && !watched && !lib.Options.NoWatch {
		if every = unwatchedEvery; networkShare(p.Path) {
			every = networkEvery
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
		// Where one watch covers a folder and everything under it, the library's own folders
		// are all that is watched; elsewhere every folder the last scan stored is.
		var folders []string
		if watchesSubfolders {
			for _, p := range lib.Paths {
				folders = append(folders, p.Path)
			}
		} else {
			stored, err := m.scanner.DB.LibraryDirectories(ctx, lib.ID, maxWatched+1)
			watched = err == nil && len(stored) <= maxWatched
			// The library's own folders are watched from the start, before any scan has
			// stored them, so a new library is watched as soon as it is made.
			for _, p := range lib.Paths {
				folders = append(folders, p.Path)
			}
			folders = append(folders, stored...)
		}
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
// the ones whose days are up. It is hourly housekeeping, so that "after 30 days" does not wait
// for the next scan.
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
