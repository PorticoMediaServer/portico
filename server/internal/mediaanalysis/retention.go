package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// RunRetention pages metadata and physical objects. No full cache directory is
// materialized. A read holds mediaartifact.Reader through physical completion.
func (s *Service) RunRetention(ctx context.Context) {
	// Retention is the lowest class there is: it never holds up a viewer, and a
	// sweep it skips this minute simply happens next minute.
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	// A retention sweep only ever has work after somebody wrote something, so it
	// is woken by commits rather than by a clock. On a server nobody is using
	// that is no queries at all, instead of a delete attempt every thirty
	// seconds for as long as the machine is switched on.
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "analysis_*", "inventory_objects", "library_sources")
	defer unregister()
	worker.Run(ctx, "mediaanalysis.retention", wake, func(ctx context.Context) time.Duration {
		if !dbwork.Yield(ctx) || !dbwork.WaitBackground(ctx) {
			return 0
		}
		ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, s.db))
		started := time.Now()
		if err := s.Sweep(ctx); err != nil {
			// A failed sweep is not an emergency — the rows are still there next
			// time — but it should not wait out the full safety tick either.
			return 30 * time.Second
		}
		if !dbwork.PaceBackground(ctx, started) {
			return 0
		}
		return 0
	})
}

// retirePageSize bounds the live results one retire pass examines.
const retirePageSize = 128

// retirePage examines the next page of live results after s.retireCursor and
// retires the stale ones, dropping their heads in the same transaction (the
// other retire paths replace their head as they retire). The stale predicate
// runs only inside the page, so a library whose results are all current costs
// one indexed page per wake instead of a walk of every result (WJ2). The cursor
// returns to the start after a short page.
func (s *Service) retirePage(ctx context.Context, now time.Time) error {
	examined, last := 0, ""
	e := dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		examined, last = 0, ""
		e := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(id),'') FROM (SELECT id FROM analysis_results INDEXED BY analysis_results_retention WHERE retired_ms=0 AND id>? ORDER BY id LIMIT ?)`, s.retireCursor, retirePageSize).Scan(&examined, &last)
		if e != nil || examined == 0 {
			return e
		}
		rows, e := tx.QueryContext(ctx, `UPDATE analysis_results SET retired_ms=? WHERE id IN(SELECT r.id FROM analysis_results r INDEXED BY analysis_results_retention WHERE r.retired_ms=0 AND r.id>? AND r.id<=? AND NOT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.id=r.object_id AND o.revision=r.source_revision AND o.root_incarnation=r.root_incarnation AND o.retired=0 AND src.incarnation=r.root_incarnation AND src.generation=r.configuration_generation AND (r.evidence NOT LIKE 'remote:%' OR EXISTS(SELECT 1 FROM analysis_probe_bindings p WHERE p.object_id=o.id AND p.source_revision=o.revision AND p.evidence=r.evidence)))) RETURNING id`, now.UnixMilli(), s.retireCursor, last)
		if e != nil {
			return e
		}
		var retired []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			retired = append(retired, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range retired {
			if _, e = tx.ExecContext(ctx, `DELETE FROM analysis_heads WHERE result_id=?`, id); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if examined < retirePageSize {
		s.retireCursor = ""
	} else {
		s.retireCursor = last
	}
	return nil
}

func (s *Service) Sweep(ctx context.Context) error {
	s.publication.Lock()
	defer s.publication.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Duration(s.options.RetainDays) * 24 * time.Hour).UnixMilli()
	// A source outage is not deletion. Only authoritative retirement, a changed
	// inventory revision/incarnation/configuration, or removal invalidates a head.
	if e := s.retirePage(ctx, now); e != nil {
		return e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT a.id,a.digest,a.size FROM analysis_results r INDEXED BY analysis_results_retention JOIN analysis_artifacts a ON a.result_id=r.id WHERE r.retired_ms>0 AND r.retired_ms<? ORDER BY r.retired_ms,r.id,a.id LIMIT 32`, cutoff)
	if e != nil {
		return e
	}
	type expired struct {
		id     string
		object mediaartifact.Object
	}
	batch := []expired{}
	for rows.Next() {
		var a expired
		if e = rows.Scan(&a.id, &a.object.Digest, &a.object.Size); e != nil {
			break
		}
		batch = append(batch, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, a := range batch {
		e = s.pruneArtifact(ctx, a.id, a.object, cutoff)
		if e != nil && !errors.Is(e, livechannels.ErrPhysicalBusy) && !errors.Is(e, mediaartifact.ErrLeased) {
			return e
		}
	}

	if s.inventory == nil {
		s.inventory, e = s.artifacts.Inventory()
		if e != nil {
			return e
		}
	}
	objects, e := s.inventory.Next(ctx, 128)
	if e != nil && e != io.EOF {
		return e
	}
	if e == io.EOF {
		s.inventory.Close()
		s.inventory = nil
	}
	for _, v := range objects {
		if v.Modified.After(now.Add(-time.Hour)) {
			continue
		}
		e = s.pruneOrphan(ctx, v.Object)
		if e != nil && !errors.Is(e, livechannels.ErrPhysicalBusy) && !errors.Is(e, mediaartifact.ErrLeased) {
			return e
		}
	}

	_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM analysis_marker_receipts WHERE rowid IN(SELECT rowid FROM analysis_marker_receipts WHERE created_ms<? LIMIT 32)`, now.Add(-60*24*time.Hour).UnixMilli())
	if e != nil {
		return e
	}
	// Artifact custody has already removed expired bytes. Drop their summary
	// rows only after every head and artifact reference is gone, using the same
	// owner retention period and a bounded writer quantum.
	if _, e = s.pruneResultSummaries(ctx, cutoff); e != nil {
		return e
	}
	// This inherited lock is proof that an old decoder physically retired. Time,
	// job status, and a stale PID are deliberately insufficient for staging GC.
	lock, e := s.physical.Lock(livechannels.Allocation{ID: analysisLockID("analysis-producer"), Generation: 1})
	if errors.Is(e, livechannels.ErrPhysicalBusy) {
		return nil
	}
	if e != nil {
		return e
	}
	defer lock.Close()
	return s.cleanStaging()
}
func (s *Service) cleanStaging() error {
	directory := filepath.Join(s.options.Directory, "staging")
	f, e := os.Open(directory)
	if e != nil {
		return e
	}
	defer f.Close()
	entries, e := f.ReadDir(256)
	if e != nil && e != io.EOF {
		return e
	}
	for _, entry := range entries {
		if (!strings.HasPrefix(entry.Name(), "output-") && !strings.HasPrefix(entry.Name(), "capture-")) || !entry.Type().IsRegular() {
			continue
		}
		info, e := entry.Info()
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if e = os.Remove(filepath.Join(directory, entry.Name())); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return dirsync.Sync(directory)
}

// Publication and reader admission take the same gate before touching bytes.
// Recheck references while holding it, not against a pre-lock SQL snapshot.
func (s *Service) pruneArtifact(ctx context.Context, id string, object mediaartifact.Object, cutoff int64) error {
	gate, e := s.custody.exclusive(object.Digest)
	if e != nil {
		return e
	}
	defer gate.Close()
	var retained bool
	e = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM analysis_artifacts a JOIN analysis_results r ON r.id=a.result_id WHERE a.digest=? AND (r.retired_ms=0 OR r.retired_ms>=?))`, object.Digest, cutoff).Scan(&retained)
	if e != nil {
		return e
	}
	if !retained {
		if e = s.artifacts.Remove(object); e != nil {
			return e
		}
	}
	_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM analysis_artifacts WHERE id=? AND result_id IN(SELECT id FROM analysis_results WHERE retired_ms>0 AND retired_ms<?)`, id, cutoff)
	return e
}
func (s *Service) pruneOrphan(ctx context.Context, object mediaartifact.Object) error {
	gate, e := s.custody.exclusive(object.Digest)
	if e != nil {
		return e
	}
	defer gate.Close()
	var referenced bool
	if e = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM analysis_artifacts WHERE digest=?)`, object.Digest).Scan(&referenced); e != nil {
		return e
	}
	if !referenced {
		return s.artifacts.Remove(object)
	}
	return nil
}

func (s *Service) pruneResultSummaries(ctx context.Context, cutoff int64) (int64, error) {
	result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `DELETE FROM analysis_results WHERE id IN(SELECT r.id FROM analysis_results r WHERE r.retired_ms>0 AND r.retired_ms<? AND NOT EXISTS(SELECT 1 FROM analysis_heads h WHERE h.result_id=r.id) AND NOT EXISTS(SELECT 1 FROM analysis_artifacts a WHERE a.result_id=r.id) ORDER BY r.retired_ms,r.id LIMIT 32)`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
