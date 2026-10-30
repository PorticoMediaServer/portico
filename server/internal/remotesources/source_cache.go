package remotesources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A WebDAV source's own read cache keeps the beginning of a file it has just
// read on this server's disk, one immutable prefix per object version. It is
// private to the source, bounded by that source's saved cache size and floor,
// and never shared with another source or another version of the same object.

type rangeCache struct {
	service    *Service
	source     string
	key        string
	file       *os.File
	size       int64
	cached     int64
	configured int64
	floor      int64
	committed  bool
	guard      *cacheGuard
	budget     *sourceCacheBudget
	fileInfo   os.FileInfo
	generation string
	binding    binding
	release    func()
}

func cacheDirectory(private, source string) (string, error) {
	if source == "" || len(source) > 128 {
		return "", storage.ErrRemoteConfig
	}
	return filepath.Join(private, "source-cache", source), nil
}

func cacheKey(generation string, snapshot storage.Snapshot) string {
	sum := sha256.Sum256([]byte(generation + "\x00" + snapshot.Path + "\x00" + snapshot.Revision + "\x00" + strconv.FormatInt(snapshot.Size, 10)))
	return hex.EncodeToString(sum[:])
}

// sourceCacheConfigTx reads the saved WebDAV cache size and free-space floor.
func sourceCacheConfigTx(ctx context.Context, tx *sql.Tx, source string) (int64, int64, error) {
	var bytes, floor int64
	err := tx.QueryRowContext(ctx, `SELECT cache_bytes,cache_floor_bytes FROM remote_source_settings WHERE source_id=?`, source).Scan(&bytes, &floor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return bytes, floor, err
}

// openRangeCache pins one object version's prefix file for a source. It
// returns nil when this source has no cache configured.
func (s *Service) openRangeCache(ctx context.Context, b binding, generation string, snapshot storage.Snapshot) (*rangeCache, error) {
	if s == nil || s.db == nil || s.private == "" {
		return nil, nil
	}
	// Publication and admission cannot cross a source-cache cleanup cut. The
	// lifecycle lock is always taken before an entry guard, never beneath one.
	s.cacheLife.RLock()
	defer s.cacheLife.RUnlock()
	snapshotTx, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	bytes, floor, err := sourceCacheConfigTx(ctx, snapshotTx.Tx(), b.ID)
	snapshotTx.Rollback()
	if err != nil || bytes <= 0 {
		return nil, err
	}
	directory, err := cacheDirectory(s.private, b.ID)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, storage.ErrRemoteLimit
	}
	key := cacheKey(generation, snapshot)
	file := filepath.Join(directory, key+".range")
	guard := s.guardForOpen(b.ID, key)
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if s.cacheGuardRetired(guard) {
		return nil, storage.ErrRemoteChanged
	}
	if err := s.check(ctx, b, generation); err != nil {
		return nil, err
	}
	handle, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, storage.ErrRemoteLimit
	}
	info, err := handle.Stat()
	if err != nil {
		handle.Close()
		return nil, err
	}
	if guard.loaded && (guard.fileInfo == nil || !os.SameFile(guard.fileInfo, info) || info.Size() < guard.cached) {
		// A shared prefix cannot be rebound or truncated under another acquisition.
		// In particular, an old descriptor must never publish a replacement inode.
		handle.Close()
		return nil, storage.ErrRemoteChanged
	}
	if !guard.loaded {
		cached := int64(0)
		var used int64
		rowErr := s.db.QueryRowContext(ctx, `SELECT used_bytes FROM remote_source_cache_entries WHERE source_id=? AND cache_key=?`, b.ID, key).Scan(&used)
		if rowErr != nil && !errors.Is(rowErr, sql.ErrNoRows) {
			handle.Close()
			return nil, rowErr
		}
		if rowErr == nil && used >= 0 && used <= snapshot.Size {
			if info, statErr := handle.Stat(); statErr == nil && info.Size() >= used {
				cached = used
			}
		}
		if err = handle.Truncate(cached); err != nil {
			handle.Close()
			return nil, err
		}
		guard.cached = cached
		guard.loaded = true
		guard.fileInfo = info
	}
	guard.pins++
	return &rangeCache{service: s, source: b.ID, key: key, file: handle, fileInfo: info, size: snapshot.Size, configured: bytes, floor: floor, guard: guard, budget: guard.budget, generation: generation, binding: b}, nil
}

// currentFileLocked verifies the pathname still names this acquisition's inode.
// Callers hold the entry guard; no cleanup can unlink a successor by name alone.
func (c *rangeCache) currentFileLocked() bool {
	info, err := os.Lstat(c.file.Name())
	return err == nil && info.Mode().IsRegular() && c.fileInfo != nil && os.SameFile(c.fileInfo, info)
}

// append fills the cache from the current end to end. Growth is checked
// against the settings in force right now, reclaimed from this source's least
// recently used entries when the budget is full, and reserved against both the
// source budget and the host's outstanding growth before a byte is copied.
func (c *rangeCache) append(ctx context.Context, stream io.Reader, end int64) error {
	if c == nil || c.file == nil || end <= c.guard.cached || end > c.size {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.service.cacheGuardRetired(c.guard) || !c.currentFileLocked() {
		return storage.ErrRemoteChanged
	}
	if err := c.service.check(ctx, c.binding, c.generation); err != nil {
		return err
	}
	var configured, floor int64
	if err := c.service.db.QueryRowContext(ctx, `SELECT cache_bytes,cache_floor_bytes FROM remote_source_settings WHERE source_id=?`, c.source).Scan(&configured, &floor); err != nil || configured <= 0 {
		return storage.ErrRemoteLimit
	}
	if c.guard.cached+(end-c.guard.cached) > configured {
		return storage.ErrRemoteLimit
	}
	growth := end - c.guard.cached
	directory := filepath.Dir(c.file.Name())
	volume, err := mounts.CacheVolumeKey(directory)
	if err != nil {
		return storage.ErrRemoteLimit
	}
	disk := reserveVolumeGrowth(volume, floor, growth, func(floor, total int64) bool {
		return mounts.CacheSpaceFor(directory, floor, total)
	})
	if disk == nil {
		return storage.ErrRemoteLimit
	}
	defer disk.settle(growth)
	budget := c.budget
	if err := budget.load(ctx, c.service, c.source, configured); err != nil {
		return err
	}
	if !budget.reserve(ctx, c.service, c.source, configured, floor, growth) {
		c.service.evictSourceCache(ctx, c.source, configured, c.key, growth)
		if !budget.reserve(ctx, c.service, c.source, configured, floor, growth) {
			return storage.ErrRemoteLimit
		}
	}
	if _, err := c.file.Seek(c.guard.cached, io.SeekStart); err != nil {
		budget.release(growth)
		return err
	}
	n, err := io.CopyN(c.file, stream, growth)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && (c.service.cacheGuardRetired(c.guard) || !c.currentFileLocked()) {
		err = storage.ErrRemoteChanged
	}
	if err != nil || n != growth {
		budget.release(growth)
		_ = c.file.Truncate(c.guard.cached)
		return mapError(err)
	}
	budget.accept(growth)
	c.guard.cached = end
	return nil
}

// Disk growth reservations share the actual filesystem identity, including
// source caches and captures whose owned storage has a different directory.
var diskGrowth = struct {
	sync.Mutex
	byVolume map[string]*volumeGrowth
}{byVolume: map[string]*volumeGrowth{}}

type volumeGrowth struct {
	pending int64
	floors  map[int64]int
}

// A reservation settles against its original volume and floor. Capture
// consumers settle only successfully persisted bytes, never bytes just read.
type diskGrowthReservation struct {
	volume    string
	floor     int64
	remaining int64 // protected by diskGrowth
}

// available rechecks the original floor and every outstanding growth on the
// volume under the same gate as admission. It does not renew the reservation.
func (r *diskGrowthReservation) available(spaceFor func(int64, int64) bool) bool {
	if r == nil || spaceFor == nil {
		return false
	}
	diskGrowth.Lock()
	defer diskGrowth.Unlock()
	floor, pending := r.floor, int64(0)
	if state := diskGrowth.byVolume[r.volume]; state != nil {
		pending = state.pending
		for activeFloor := range state.floors {
			floor = max(floor, activeFloor)
		}
	}
	return spaceFor(floor, pending)
}

// spaceFor checks the already-validated destination while this shared growth
// gate is held. The callback supplies no source permission or quota decision.
func reserveVolumeGrowth(volume string, floor, growth int64, spaceFor func(int64, int64) bool) *diskGrowthReservation {
	if volume == "" || floor < 0 || growth <= 0 || spaceFor == nil {
		return nil
	}
	diskGrowth.Lock()
	defer diskGrowth.Unlock()
	state := diskGrowth.byVolume[volume]
	if state == nil {
		state = &volumeGrowth{floors: make(map[int64]int)}
	}
	maximumFloor := floor
	for activeFloor := range state.floors {
		maximumFloor = max(maximumFloor, activeFloor)
	}
	if state.pending < 0 || growth > math.MaxInt64-state.pending || !spaceFor(maximumFloor, growth+state.pending) {
		return nil
	}
	state.pending += growth
	state.floors[floor]++
	diskGrowth.byVolume[volume] = state
	return &diskGrowthReservation{volume: volume, floor: floor, remaining: growth}
}

// Release always names the originally admitted volume, even if its path has
// disappeared or subsequently refers to a replacement filesystem.
func (r *diskGrowthReservation) settle(growth int64) {
	if r == nil || growth <= 0 {
		return
	}
	diskGrowth.Lock()
	defer diskGrowth.Unlock()
	if r.remaining == 0 {
		return
	}
	settled := min(growth, r.remaining)
	state := diskGrowth.byVolume[r.volume]
	state.pending -= settled
	r.remaining -= settled
	if r.remaining == 0 {
		state.floors[r.floor]--
		if state.floors[r.floor] == 0 {
			delete(state.floors, r.floor)
		}
	}
	if state.pending == 0 {
		delete(diskGrowth.byVolume, r.volume)
	}
}

// commit publishes the prefix only inside one statement that also checks the
// source is still the same generation with the same cache size and floor, so a
// reconfigure cannot slip between a read and a write. A stale or retired entry
// discards its file and never returns to the table.
func (c *rangeCache) commit(ctx context.Context) error {
	if c == nil || c.file == nil || c.committed {
		return nil
	}
	c.service.cacheLife.RLock()
	defer c.service.cacheLife.RUnlock()
	c.committed = true
	guard := c.guard
	guard.mu.Lock()
	defer guard.mu.Unlock()
	name := filepath.Base(c.file.Name())
	if err := c.file.Sync(); err != nil {
		_ = c.file.Close()
		return err
	}
	if err := c.file.Close(); err != nil {
		return err
	}
	if c.service.cacheGuardRetired(guard) || !c.currentFileLocked() {
		return c.discardLocked(ctx)
	}
	now := time.Now().UnixMilli()
	result, err := dbwork.ExecWrite(ctx, c.service.db, dbwork.ClassBackgroundMedia, `INSERT INTO remote_source_cache_entries(source_id,cache_key,file_name,used_bytes,object_size,accessed_ms)
SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM remote_sources r JOIN remote_source_settings st ON st.source_id=r.id WHERE r.id=? AND r.removed=0 AND r.generation=? AND st.cache_bytes=? AND st.cache_floor_bytes=?)
ON CONFLICT(source_id,cache_key) DO UPDATE SET used_bytes=excluded.used_bytes,object_size=excluded.object_size,accessed_ms=excluded.accessed_ms`,
		c.source, c.key, name, guard.cached, c.size, now, c.source, mustInt(c.generation), c.configured, c.floor)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return c.discardLocked(ctx)
	}
	return c.service.evictSourceCache(ctx, c.source, c.configured, c.key, 0)
}

// discardLocked runs under commit's guard. Reacquiring it would deadlock the
// acquisition's Close before it can unpin the cache and release its source slot.
func (c *rangeCache) discardLocked(ctx context.Context) error {
	if c.guard.pins > 1 {
		// The bytes and their budget belong to the shared file, not this writer.
		return nil
	}
	var published string
	err := c.service.db.QueryRowContext(ctx, `SELECT file_name FROM remote_source_cache_entries WHERE source_id=? AND cache_key=?`, c.source, c.key).Scan(&published)
	if err == nil {
		// Another acquisition may have published this shared prefix under newer
		// settings. A refused old commit must not unlink or unaccount its bytes.
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if c.currentFileLocked() {
		if err = os.Remove(c.file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Settle against the exact bucket this allocation reserved from.
	if c.budget != nil {
		c.budget.subtract(c.guard.cached)
	}
	c.guard.cached, c.guard.loaded, c.guard.fileInfo = 0, false, nil
	return nil
}

func mustInt(value string) int64 {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func (s *Service) cacheGuardRetired(g *cacheGuard) bool {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return g.retired
}

// evictSourceCache deletes the least recently used entries of one source,
// except the entry just read, until their total is at or below the configured
// size. Rows are removed first, files after, and only inside this source.
func (s *Service) evictSourceCache(ctx context.Context, source string, limit int64, keep string, need int64) error {
	if limit <= 0 {
		return nil
	}
	budget := s.budgetFor(source)
	total, err := s.chargedCache(ctx, source)
	if err != nil {
		return err
	}
	if total+need <= limit {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT cache_key,file_name,used_bytes FROM remote_source_cache_entries WHERE source_id=? AND cache_key<>? ORDER BY accessed_ms, cache_key LIMIT 64`, source, keep)
	if err != nil {
		return err
	}
	type victim struct {
		key, file string
		bytes     int64
	}
	victims := []victim{}
	for rows.Next() && total+need > limit {
		var v victim
		if err = rows.Scan(&v.key, &v.file, &v.bytes); err != nil {
			break
		}
		victims = append(victims, v)
		total -= v.bytes
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	directory, dirErr := cacheDirectory(s.private, source)
	if dirErr != nil {
		return dirErr
	}
	for _, v := range victims {
		if filepath.Base(v.file) != v.file {
			continue
		}
		g := s.guardFor(source, v.key)
		// Never block while another key's guard is held by this caller: a pair
		// of growths would otherwise wait on each other. Skipping is safe; the
		// next attempt reclaims.
		if !g.mu.TryLock() {
			continue
		}
		if g.readers > 0 || g.pins > 0 {
			g.mu.Unlock()
			continue
		}
		info, _ := os.Lstat(filepath.Join(directory, v.file))
		result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM remote_source_cache_entries WHERE source_id=? AND cache_key=? AND file_name=? AND used_bytes=?`, source, v.key, v.file, v.bytes)
		if err != nil {
			g.mu.Unlock()
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			g.mu.Unlock()
			continue
		}
		if g.fileInfo != nil {
			info = g.fileInfo
		}
		removeCacheFile(filepath.Join(directory, v.file), info)
		// The row is gone; a later acquisition cannot reuse its remembered prefix.
		freed := v.bytes
		if g.loaded && g.cached > freed {
			freed = g.cached
		}
		g.cached, g.loaded, g.fileInfo = 0, false, nil
		ownedBudget := g.budget
		g.mu.Unlock()
		if ownedBudget == nil {
			ownedBudget = budget
		}
		ownedBudget.subtract(freed)
	}
	return nil
}

// clearSourceCache removes every cached prefix of one source, used when its
// configuration or generation changes so no old credential's bytes survive.
func (s *Service) clearSourceCache(ctx context.Context, source string) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.cacheLife.Lock()
	defer s.cacheLife.Unlock()
	s.retireSourceCacheGuards(source)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	directory, err := cacheDirectory(s.private, source)
	if err != nil {
		return err
	}
	type owned struct{ key, file string }
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT cache_key,file_name FROM remote_source_cache_entries WHERE source_id=? ORDER BY cache_key LIMIT 64`, source)
		if err != nil {
			return err
		}
		files := []owned{}
		for rows.Next() {
			var one owned
			if err = rows.Scan(&one.key, &one.file); err != nil {
				break
			}
			files = append(files, one)
		}
		readErr := rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if readErr != nil {
			return readErr
		}
		if len(files) == 0 {
			return nil
		}
		for _, one := range files {
			// Never delete unseen rows beyond the bounded page. An interrupted
			// cleanup retains their ownership records for a later attempt.
			result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM remote_source_cache_entries WHERE source_id=? AND cache_key=? AND file_name=?`, source, one.key, one.file)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n != 1 || filepath.Base(one.file) != one.file || one.key == "" {
				continue
			}
			g := s.guardFor(source, one.key)
			if !g.mu.TryLock() {
				continue
			}
			if g.pins == 0 && g.readers == 0 {
				info := g.fileInfo
				if info == nil {
					info, _ = os.Lstat(filepath.Join(directory, one.file))
				}
				removeCacheFile(filepath.Join(directory, one.file), info)
				g.cached, g.loaded, g.fileInfo = 0, false, nil
			}
			g.mu.Unlock()
		}
	}
}

// reconcileSourceCache drops entries whose file is missing or shorter than the
// bytes they claim. It pages by (source,key) across successive wakes, uses an
// exact key/bytes re-check and row count before removing a file, and never
// treats a file it did not see in a page as an orphan.
func (s *Service) reconcileSourceCachePage(ctx context.Context) {
	s.cacheMu.Lock()
	cursorSource, cursorKey := s.cacheReconcile.source, s.cacheReconcile.key
	s.cacheMu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT source_id,cache_key,file_name,used_bytes FROM remote_source_cache_entries WHERE (source_id>? OR (source_id=? AND cache_key>?)) ORDER BY source_id,cache_key LIMIT 64`, cursorSource, cursorSource, cursorKey)
	if err != nil {
		return
	}
	type entry struct {
		source, key, file string
		used              int64
	}
	page := []entry{}
	for rows.Next() {
		var e entry
		if rows.Scan(&e.source, &e.key, &e.file, &e.used) == nil {
			page = append(page, e)
		}
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return
	}
	if len(page) == 0 {
		s.cacheMu.Lock()
		s.cacheReconcile.source, s.cacheReconcile.key = "", ""
		s.cacheMu.Unlock()
		return
	}
	for _, e := range page {
		cursorSource, cursorKey = e.source, e.key
		if filepath.Base(e.file) != e.file {
			continue
		}
		directory, dirErr := cacheDirectory(s.private, e.source)
		if dirErr != nil {
			continue
		}
		g := s.guardFor(e.source, e.key)
		if !g.mu.TryLock() {
			continue
		}
		if g.readers > 0 || g.pins > 0 {
			g.mu.Unlock()
			continue
		}
		info, statErr := os.Lstat(filepath.Join(directory, e.file))
		if statErr == nil && info.Mode().IsRegular() && info.Size() >= e.used && (g.fileInfo == nil || os.SameFile(g.fileInfo, info)) {
			g.mu.Unlock()
			continue
		}
		budget := g.budget
		if budget == nil {
			budget = s.budgetFor(e.source)
		}
		result, delErr := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM remote_source_cache_entries WHERE source_id=? AND cache_key=? AND file_name=? AND used_bytes=?`, e.source, e.key, e.file, e.used)
		if delErr == nil {
			if n, _ := result.RowsAffected(); n == 1 {
				if g.fileInfo != nil {
					info = g.fileInfo
				}
				removeCacheFile(filepath.Join(directory, e.file), info)
				freed := e.used
				if g.loaded && g.cached > freed {
					freed = g.cached
				}
				budget.subtract(freed)
				g.cached, g.loaded, g.fileInfo = 0, false, nil
			}
		}
		g.mu.Unlock()
	}
	s.cacheMu.Lock()
	s.cacheReconcile.source, s.cacheReconcile.key = cursorSource, cursorKey
	s.cacheMu.Unlock()
}

// The guard excludes in-process replacements; the inode check also refuses a
// pathname that changed while the database writer was accepting the deletion.
func removeCacheFile(path string, original os.FileInfo) {
	if original == nil || !original.Mode().IsRegular() {
		return
	}
	if current, err := os.Lstat(path); err == nil && os.SameFile(original, current) {
		_ = os.Remove(path)
	}
}

type sourceCacheBudget struct {
	mu       sync.Mutex
	used     int64
	reserved int64
	limit    int64
	loaded   bool
}

type cacheGuard struct {
	mu      sync.Mutex
	readers int
	retired bool
	// cached is the committed prefix both readers and writers share for this
	// source+key; it only moves under mu.
	cached   int64
	loaded   bool
	fileInfo os.FileInfo
	budget   *sourceCacheBudget
	// pins counts acquisitions holding this entry open, so eviction never
	// unlinks a file an acquisition may still publish from.
	pins int
}

// guardFor returns the per source+key ownership record, creating it lazily.
func (s *Service) guardFor(source, key string) *cacheGuard {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return s.guardForLocked(source, key, false)
}

// Only an admission after cleanup may replace a retired, completely unpinned
// guard. Cleanup itself retains the original guard and original budget owner.
func (s *Service) guardForOpen(source, key string) *cacheGuard {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return s.guardForLocked(source, key, true)
}

func (s *Service) guardForLocked(source, key string, renew bool) *cacheGuard {
	if s.cacheGuards == nil {
		s.cacheGuards = map[string]*cacheGuard{}
	}
	id := source + "\x00" + key
	if g := s.cacheGuards[id]; g != nil {
		if !renew || !g.retired || !g.mu.TryLock() {
			return g
		}
		busy := g.pins > 0 || g.readers > 0
		g.mu.Unlock()
		if busy {
			return g
		}
	}
	g := &cacheGuard{budget: s.budgetForLocked(source)}
	s.cacheGuards[id] = g
	return g
}

func (s *Service) budgetFor(source string) *sourceCacheBudget {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return s.budgetForLocked(source)
}

func (s *Service) budgetForLocked(source string) *sourceCacheBudget {
	if s.cacheBudgets == nil {
		s.cacheBudgets = map[string]*sourceCacheBudget{}
	}
	if b := s.cacheBudgets[source]; b != nil {
		return b
	}
	b := &sourceCacheBudget{}
	s.cacheBudgets[source] = b
	return b
}

// load reads the committed total once and keeps it in memory under the budget
// lock, so concurrent writers reserve against a single number.
func (b *sourceCacheBudget) load(ctx context.Context, service *Service, source string, limit int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limit = limit
	if b.loaded {
		return nil
	}
	view, err := dbwork.BeginSnapshot(ctx, service.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	var used int64
	if err = view.Tx().QueryRowContext(ctx, `SELECT COALESCE(SUM(used_bytes),0) FROM remote_source_cache_entries WHERE source_id=?`, source).Scan(&used); err != nil {
		return err
	}
	b.used = used
	b.loaded = true
	return nil
}

// reserve accepts prefix growth only when the source's own budget, the host
// floor and the durable full capture charge all still hold. The capture scalar
// is read fresh under b.mu; it is never folded into b.used.
func (b *sourceCacheBudget) reserve(ctx context.Context, service *Service, source string, limit, floor, growth int64) bool {
	if growth <= 0 || limit <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	capture, err := captureQuotaRead(ctx, service, source)
	if err != nil {
		return false
	}
	if b.used < 0 || b.reserved < 0 || capture < 0 || b.used > limit || b.reserved > limit || capture > limit {
		return false
	}
	remaining := limit - b.used
	if b.reserved > remaining {
		return false
	}
	remaining -= b.reserved
	if capture > remaining {
		return false
	}
	remaining -= capture
	if growth > remaining {
		return false
	}
	b.reserved += growth
	return true
}

// captureQuotaRead reads the O(1) durable capture scalar on the background read
// handle, never on a writer connection. It is called with the source budget
// mutex held and closes its snapshot before returning.
func captureQuotaRead(ctx context.Context, service *Service, source string) (int64, error) {
	view, err := dbwork.BeginSnapshot(ctx, service.db)
	if err != nil {
		return 0, err
	}
	defer view.Rollback()
	var bytes int64
	if err = view.Tx().QueryRowContext(ctx, `SELECT COALESCE((SELECT capture_bytes FROM remote_source_capture_quota WHERE source_id=?),0)`, source).Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes < 0 {
		return 0, storage.ErrRemoteLimit
	}
	return bytes, nil
}

// sourceCacheChargedTx accounts for committed prefix plus full capture charges for
// one source without a SUM over capture receipts.
func sourceCacheChargedTx(ctx context.Context, tx *sql.Tx, source string) (int64, error) {
	var used int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(used_bytes) FROM remote_source_cache_entries WHERE source_id=?1),0)+COALESCE((SELECT capture_bytes FROM remote_source_capture_quota WHERE source_id=?1),0)`, source).Scan(&used)
	if err == nil && used < 0 {
		err = storage.ErrRemoteLimit
	}
	return used, err
}

func (b *sourceCacheBudget) accept(growth int64) {
	b.mu.Lock()
	b.reserved -= growth
	b.used += growth
	if b.reserved < 0 {
		b.reserved = 0
	}
	b.mu.Unlock()
}

func (b *sourceCacheBudget) release(growth int64) {
	b.mu.Lock()
	b.reserved -= growth
	if b.reserved < 0 {
		b.reserved = 0
	}
	b.mu.Unlock()
}

func (b *sourceCacheBudget) subtract(bytes int64) {
	b.mu.Lock()
	b.used -= bytes
	if b.used < 0 {
		b.used = 0
	}
	b.mu.Unlock()
}

// retireSourceCacheGuards stops any finishing writer of this source from
// publishing after its configuration or generation changed.
func (s *Service) retireSourceCacheGuards(source string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	prefix := source + "\x00"
	for id, g := range s.cacheGuards {
		if strings.HasPrefix(id, prefix) {
			g.retired = true
		}
	}
	// Guard creation captures its bucket under this same lock. A new source
	// incarnation cannot inherit a bucket that old file owners still settle.
	delete(s.cacheBudgets, source)
}

// cacheReadCloser keeps the acquisition lease and its cancellation live for the
// whole body, exactly like a provider range read.
type cacheReadCloser struct {
	reader  io.Reader
	ctx     context.Context
	release func()
	guard   *cacheGuard
	service *Service
	cache   *rangeCache
	once    sync.Once
}

func (r *cacheReadCloser) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.service.cacheGuardRetired(r.guard) {
		return 0, storage.ErrRemoteChanged
	}
	n, err := r.reader.Read(p)
	if stopped := r.ctx.Err(); stopped != nil {
		return 0, stopped
	}
	if r.service.cacheGuardRetired(r.guard) {
		return 0, storage.ErrRemoteChanged
	}
	return n, err
}

func (r *cacheReadCloser) Close() error {
	r.once.Do(func() {
		r.guard.mu.Lock()
		if r.guard.readers > 0 {
			r.guard.readers--
		}
		r.cache.discardUnownedLocked()
		r.guard.mu.Unlock()
		if r.release != nil {
			r.release()
		}
	})
	return nil
}

// holdReaderLocked is the atomic form for a caller already holding the guard:
// lookup and reader pin cannot be separated from the fill decision.
func (c *rangeCache) holdReaderLocked(ctx context.Context, offset, length int64, release func()) io.ReadCloser {
	c.guard.readers++
	return &cacheReadCloser{reader: io.NewSectionReader(c.file, offset, length), ctx: ctx, release: release, guard: c.guard, service: c.service, cache: c}
}

// unpin releases the acquisition's hold on this entry after its cache has been
// committed or discarded.
func (c *rangeCache) unpin() {
	if c == nil || c.guard == nil {
		return
	}
	c.guard.mu.Lock()
	if c.guard.pins > 0 {
		c.guard.pins--
	}
	c.discardUnownedLocked()
	c.guard.mu.Unlock()
}

// A cleanup may have skipped a busy file after deleting its exact row. The
// final original owner retries private cleanup, without selecting a successor.
func (c *rangeCache) discardUnownedLocked() {
	if c.guard.pins != 0 || c.guard.readers != 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.discardLocked(ctx)
}

func (s *Service) cacheUsed(ctx context.Context, source string) (int64, error) {
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer view.Rollback()
	var used int64
	err = view.Tx().QueryRowContext(ctx, `SELECT COALESCE(SUM(used_bytes),0) FROM remote_source_cache_entries WHERE source_id=?`, source).Scan(&used)
	return used, err
}

// chargedCache is quota accounting, not a measurement of physical cache bytes.
func (s *Service) chargedCache(ctx context.Context, source string) (int64, error) {
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer view.Rollback()
	return sourceCacheChargedTx(ctx, view.Tx(), source)
}
