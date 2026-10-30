package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"sort"
	"time"
)

const monitorDebounce = 2 * time.Second
const monitorMaxDelay = 10 * time.Second

type monitorSource struct {
	storage.WatchRoot
	library, classification string
	generation              int64
	// remoteOwned is the scheduler-ownership fact: a bound remote source or a
	// root the storage adapter handles. Either one is enough, so an
	// inconsistent pair fails closed instead of becoming a local fallback walk.
	remoteOwned bool
}
type pendingFolder struct{ first, last time.Time }

// Seams for the monitor's clock and watch helper. Production keeps the real
// clock and the real storage watcher.
var (
	monitorClock = time.Now
	monitorWatch = func(s *Service, ctx context.Context, request storage.WatchRequest, emit func(storage.InventoryChange)) error {
		return s.storage.WatchInventory(ctx, request, emit)
	}
)

// The monitor has one bounded native helper, one debounce map, and a periodic
// fallback. It never stats a library in the server process or judges absence.
func (s *Service) runMonitor(ctx context.Context) {
	configurationChanged := worker.NewSignal()
	unregister := dbwork.WakeOnTables(configurationChanged, "library_sources", "libraries", "admin_documents", "dvr_private_libraries")
	defer unregister()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	events := make(chan storage.InventoryChange, 512)
	ended := make(chan error, 1)
	pending := map[string]map[string]pendingFolder{}
	sources := map[string]monitorSource{}
	fallback := map[string]time.Time{}
	var cancel context.CancelFunc
	var watchDone chan struct{}
	defer func() {
		if cancel != nil {
			cancel()
			<-watchDone
		}
	}()
	digest := ""
	ready := false
	nextConfig := time.Time{}
	add := func(source, relative string, now time.Time) {
		owner, ok := sources[source]
		if !ok || owner.remoteOwned || !filepath.IsLocal(relative) {
			return
		}
		if pending[source] == nil {
			pending[source] = map[string]pendingFolder{}
		}
		if len(pending[source]) >= maxPendingNotices {
			relative = "."
			pending[source] = map[string]pendingFolder{}
		}
		p := pending[source][relative]
		if p.first.IsZero() {
			p.first = now
		}
		p.last = now
		pending[source][relative] = p
	}
	for {
		select {
		case <-ctx.Done():
			return
		case change := <-events:
			if change.Ready {
				ready = true
				continue
			}
			add(change.SourceID, change.RelativePath, monitorClock())
		case <-ended:
			ready = false
			digest = ""
			nextConfig = monitorClock().Add(30 * time.Second)
		case <-ticker.C:
			now := monitorClock()
			if configurationChanged.Take() {
				nextConfig = time.Time{}
			}
			if !now.Before(nextConfig) {
				nextConfig = now.Add(5 * time.Second)
				current, request, err := s.monitorConfiguration(ctx)
				if err == nil {
					// Real configuration commits invalidate immediately; the
					// shared worker backstop repairs a missed signal without
					// polling SQL every five seconds on an idle server.
					nextConfig = now.Add(worker.SafetyTick)
					sources = current
					for id := range fallback {
						if owner, ok := sources[id]; !ok || owner.remoteOwned {
							delete(fallback, id)
						}
					}
					for id := range pending {
						if owner, ok := sources[id]; !ok || owner.remoteOwned {
							delete(pending, id)
						}
					}
					raw, _ := json.Marshal(request)
					sum := sha256.Sum256(raw)
					next := fmt.Sprintf("%x", sum)
					if next != digest {
						if cancel != nil {
							cancel()
							<-watchDone
							cancel = nil
							watchDone = nil
						}
						// Remove old completion signals before starting the replacement helper.
						select {
						case <-ended:
						default:
						}
						digest = next
						ready = false
						if s.storage != nil && len(request.Roots) > 0 {
							work, stop := context.WithCancel(ctx)
							cancel = stop
							watchDone = make(chan struct{})
							done := watchDone
							supervise.Go("ingestion.folder-monitor", func() {
								defer close(done)
								err := monitorWatch(s, work, request, func(change storage.InventoryChange) {
									select {
									case events <- change:
									case <-work.Done():
									}
								})
								if work.Err() == nil {
									select {
									case ended <- err:
									case <-ctx.Done():
									}
								}
							})
						}
					}
				}
			}
			for id, source := range sources {
				if source.remoteOwned {
					continue
				}
				interval := 15 * time.Minute
				if source.classification == "network" || !ready {
					interval = 5 * time.Minute
				}
				due, known := fallback[id]
				if !known {
					fallback[id] = now.Add(interval)
					_, _ = s.QueueContext(ctx, source.library, id, nil)
				} else if !now.Before(due) {
					add(id, ".", now)
					fallback[id] = now.Add(interval)
				}
			}
			paths := []string{}
			ids := make([]string, 0, len(pending))
			for id := range pending {
				if owner, ok := sources[id]; !ok || owner.remoteOwned {
					delete(pending, id)
					continue
				}
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				for relative, p := range pending[id] {
					if now.Sub(p.last) < monitorDebounce && now.Sub(p.first) < monitorMaxDelay {
						continue
					}
					paths = append(paths, filepath.Join(sources[id].Root, relative))
					if len(paths) == MaxReportedPaths {
						break
					}
				}
				if len(paths) == MaxReportedPaths {
					break
				}
			}
			if len(paths) > 0 {
				result, err := s.ReportPaths(ctx, paths, nil)
				if err == nil {
					for _, path := range result.Paths {
						if path.Status == "queued" {
							if source, ok := sources[path.SourceID]; ok {
								relative, e := filepath.Rel(source.Root, path.Path)
								if e == nil {
									delete(pending[path.SourceID], relative)
								}
							}
						}
					}
				}
			}
		}
	}
}

func (s *Service) monitorConfiguration(ctx context.Context) (map[string]monitorSource, storage.WatchRequest, error) {
	sources := map[string]monitorSource{}
	request := storage.WatchRequest{}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.library_id,s.root,s.root_identity,s.incarnation,s.classification,s.generation,s.follow_symlinks,s.remote_source_id IS NOT NULL FROM library_sources s LEFT JOIN admin_documents d ON d.scope='library:'||s.library_id WHERE s.enabled=1 AND COALESCE(json_extract(d.body,'$.monitorChanges'),1)=1 AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries p WHERE p.library_id=s.library_id) ORDER BY s.id LIMIT 4096`)
	if err != nil {
		return sources, request, err
	}
	type sourceRow struct {
		monitorSource
		incarnation string
	}
	all := []sourceRow{}
	for rows.Next() {
		var source sourceRow
		var bound bool
		if err = rows.Scan(&source.SourceID, &source.library, &source.Root, &source.RootIdentity, &source.incarnation, &source.classification, &source.generation, &source.FollowSymlinks, &bound); err != nil {
			rows.Close()
			return sources, request, err
		}
		source.remoteOwned = bound || (s.storage != nil && s.storage.IsRemote(source.Root))
		all = append(all, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return sources, request, err
	}
	for _, source := range all {
		sources[source.SourceID] = source.monitorSource
	}
	watches := 0
	wireBytes := 256
	for _, source := range all {
		sources[source.SourceID] = source.monitorSource
		if watches >= storage.MaxInventoryWatches || s.storage == nil || source.remoteOwned || source.classification == "network" || source.RootIdentity == "" || len(request.Roots) >= storage.MaxWatchRoots {
			continue
		}
		root := source.WatchRoot
		root.Directories = []string{"."}
		raw, _ := json.Marshal(root)
		if wireBytes+len(raw)+8 > 60<<10 {
			continue
		}
		wireBytes += len(raw) + 8
		watches++
		dirs, err := s.db.QueryContext(ctx, `SELECT relative_path FROM inventory_directory_cache WHERE source_id=? AND incarnation=? AND relative_path<>'.' ORDER BY relative_path LIMIT ?`, source.SourceID, source.incarnation, storage.MaxInventoryWatches-watches)
		if err != nil {
			return sources, request, err
		}
		for dirs.Next() {
			var relative string
			if err = dirs.Scan(&relative); err != nil {
				dirs.Close()
				return sources, request, err
			}
			encoded, _ := json.Marshal(relative)
			if wireBytes+len(encoded)+1 > 60<<10 {
				break
			}
			wireBytes += len(encoded) + 1
			watches++
			root.Directories = append(root.Directories, relative)
		}
		err = dirs.Err()
		dirs.Close()
		if err != nil {
			return sources, request, err
		}
		request.Roots = append(request.Roots, root)

	}
	return sources, request, nil
}
