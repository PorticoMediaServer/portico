package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This rail runs alongside inventory, never holds/paces inventory for Reading,
// and makes at most one raw observation, ancestor probe or parent-edge update
// per turn. All filesystem calls precede the writer. Remote adapters keep the
// ordinary source-wide barrier: their directory verifier is not a closure proof.
// All four write sites below use the background-media class default WritePlan:
// one raw object/absence observation, one directory/parent edge, one coverage
// row, or one renewal cursor. There is no indivisible bulk closure transaction.
func (s *Service) readingClosureQuantum(ctx context.Context, job string, source catalog.LibrarySource) error {
	if source.Kind != "local" || s.storage != nil && s.storage.IsRemote(source.ResolvedPath) || !source.IdentityConfirmed {
		return nil
	}
	var live bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_reading_jobs c JOIN inventory_runs r ON r.job_id=c.job_id JOIN reading_path_source_barrier b ON b.source_id=c.source_id
 WHERE c.job_id=? AND c.source_id=? AND c.incarnation=? AND c.generation=? AND c.root_identity=? AND c.root=? AND b.job_id=c.job_id AND b.phase='open' AND r.scan_mode='first-import')`, job, source.ID, source.Incarnation, source.Generation, source.RootIdentity, source.ResolvedPath).Scan(&live)
	if err != nil || !live {
		return err
	}
	var renewing bool
	if err = s.db.QueryRowContext(ctx, `SELECT renewing FROM inventory_reading_jobs WHERE job_id=?`, job).Scan(&renewing); err != nil {
		return err
	}
	if renewing {
		return s.renewReadingClosure(ctx, job)
	}
	type directory struct {
		path, parent, top, phase, identity, revision, afterPath, afterObject, fencePath, fenceObject, missing, missingPath, ancestor string
		reconciled, propagated                                                                                                       bool
		highwater, afterIssue, issueRevision                                                                                         int64
	}
	var d directory
	for _, phase := range []string{"seal", "reconcile", "coverage", "verify"} {
		err = s.db.QueryRowContext(ctx, `SELECT relative_path,parent_path,top_level,phase,identity,revision,after_path,after_object,fence_path,fence_object,missing_object,missing_path,absence_parent,issue_highwater,after_issue,issue_revision,reconciled,propagated
 FROM inventory_reading_directories WHERE job_id=? AND phase=? AND children=0 ORDER BY relative_path LIMIT 1`, job, phase).Scan(&d.path, &d.parent, &d.top, &d.phase, &d.identity, &d.revision, &d.afterPath, &d.afterObject, &d.fencePath, &d.fenceObject, &d.missing, &d.missingPath, &d.ancestor, &d.highwater, &d.afterIssue, &d.issueRevision, &d.reconciled, &d.propagated)
		if err == nil {
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Capture one raw object, or one durable ancestor subcursor, outside the
	// writer. No rejected/retired/seen object is hidden behind a filtered LIMIT.
	var object, path, seen, inc string
	var retired bool
	var absent bool
	if d.phase == "verify" || d.phase == "reconcile" && d.missing != "" {
		relative, identity, revision := d.path, d.identity, d.revision
		if d.missing != "" {
			relative = d.ancestor
			err = s.db.QueryRowContext(ctx, `SELECT identity,revision FROM inventory_directories WHERE job_id=? AND relative_path=? AND state='done'`, job, relative).Scan(&identity, &revision)
			if errors.Is(err, sql.ErrNoRows) {
				if relative == "." {
					return s.blockReadingClosure(ctx, job, d.path)
				}
				return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
					if _, e := catalog.InventoryFenceTx(ctx, tx, job); e != nil {
						return e
					}
					_, e := tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET absence_parent=? WHERE job_id=? AND relative_path=? AND missing_object=? AND absence_parent=?`, filepath.Dir(relative), job, d.path, d.missing, relative)
					return e
				})
			}
			if err != nil {
				return err
			}
		}
		req := storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, RelativePath: relative, FollowSymlinks: source.FollowSymlinks}
		var v storage.Snapshot
		if s.storage != nil {
			v, err = s.storage.InventoryDirectory(ctx, source.ID, req)
		} else {
			v, err = storage.LocalInventoryDirectory(req)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, storage.ErrBusy) {
			return err
		}
		if err != nil || v.ObjectIdentity != identity || storage.InventoryRevision(v) != revision {
			// A failed early proof grants nothing. Normal verification/reconciliation
			// will make its own later observation; do not convert uncertainty to absence.
			return s.blockReadingClosure(ctx, job, d.path)
		}
		absent = d.missing != ""
	} else if d.phase == "reconcile" {
		prefix := filepath.FromSlash(d.top) + string(filepath.Separator)
		upper := prefix[:len(prefix)-1] + string([]byte{prefix[len(prefix)-1] + 1})
		err = s.db.QueryRowContext(ctx, `SELECT id,relative_path,seen_job,root_incarnation,retired FROM inventory_objects INDEXED BY inventory_objects_reading_closure
 WHERE source_id=? AND relative_path>=? AND relative_path<? AND (relative_path,id)>(?,?) AND (relative_path,id)<=(?,?) ORDER BY relative_path,id LIMIT 1`, source.ID, prefix, upper, d.afterPath, d.afterObject, d.fencePath, d.fenceObject).Scan(&object, &path, &seen, &inc, &retired)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		current, e := catalog.InventoryFenceTx(ctx, tx, job)
		if e != nil {
			return e
		}
		// Recheck the full captured source tuple, not just a process-local source.
		if current.ID != source.ID || current.Incarnation != source.Incarnation || current.Generation != source.Generation || current.RootIdentity != source.RootIdentity || current.ResolvedPath != source.ResolvedPath {
			return nil
		}
		if ok, e := readingClosureFenceTx(ctx, tx, job, source); e != nil {
			return e
		} else if !ok {
			return nil
		}
		var phase, afterPath, afterObject, missing, ancestor string
		var afterIssue, issueRevision int64
		if e = tx.QueryRowContext(ctx, `SELECT phase,after_path,after_object,missing_object,absence_parent,after_issue,issue_revision FROM inventory_reading_directories WHERE job_id=? AND relative_path=?`, job, d.path).Scan(&phase, &afterPath, &afterObject, &missing, &ancestor, &afterIssue, &issueRevision); e != nil {
			return e
		}
		if phase != d.phase || afterPath != d.afterPath || afterObject != d.afterObject || missing != d.missing || ancestor != d.ancestor || afterIssue != d.afterIssue || issueRevision != d.issueRevision {
			return nil
		}
		if d.phase == "reconcile" || d.phase == "seal" {
			var revision int64
			if e = tx.QueryRowContext(ctx, `SELECT coverage_revision FROM inventory_reading_jobs WHERE job_id=?`, job).Scan(&revision); e != nil {
				return e
			}
			if revision != d.issueRevision {
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='coverage',after_issue=0,issue_revision=(SELECT coverage_revision FROM inventory_reading_jobs WHERE job_id=?1),issue_highwater=COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=?1 ORDER BY sequence DESC LIMIT 1),0) WHERE job_id=?1 AND relative_path=?2`, job, d.path)
				return e
			}
		}
		switch d.phase {
		case "verify":
			if d.path == d.top {
				prefix := filepath.FromSlash(d.top) + string(filepath.Separator)
				upper := prefix[:len(prefix)-1] + string([]byte{prefix[len(prefix)-1] + 1})
				var lastPath, lastObject string
				e = tx.QueryRowContext(ctx, `SELECT relative_path,id FROM inventory_objects INDEXED BY inventory_objects_reading_closure WHERE source_id=? AND relative_path>=? AND relative_path<? ORDER BY relative_path DESC,id DESC LIMIT 1`, source.ID, prefix, upper).Scan(&lastPath, &lastObject)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='coverage',fence_path=?,fence_object=?,issue_highwater=COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=?3 ORDER BY sequence DESC LIMIT 1),0),issue_revision=(SELECT coverage_revision FROM inventory_reading_jobs WHERE job_id=?3) WHERE job_id=?3 AND relative_path=?4`, lastPath, lastObject, job, d.path)
				return e
			}
			// Verification and one parent-edge completion commit together. Deep trees
			// cost one turn per edge, never an ancestor loop under the writer.
			if _, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='done',propagated=1 WHERE job_id=? AND relative_path=?`, job, d.path); e != nil {
				return e
			}
			if d.parent != "" && !d.propagated {
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET children=children-1 WHERE job_id=? AND relative_path=?`, job, d.parent)
			}
		case "reconcile":
			if absent {
				// Revalidate the candidate after IO; a new observation cannot be marked
				// missing using an older listing. The parent subcursor survives restart.
				var eligible bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE id=? AND source_id=? AND relative_path=? AND root_incarnation=? AND seen_job<>? AND retired=0)`, d.missing, source.ID, d.missingPath, source.Incarnation, job).Scan(&eligible); e != nil {
					return e
				}
				if eligible {
					if e = catalog.MarkInventoryMissingTx(ctx, tx, source.ID, job, d.missing, time.Now()); e != nil {
						return e
					}
				}
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET after_path=missing_path,after_object=missing_object,missing_object='',missing_path='',absence_parent='' WHERE job_id=? AND relative_path=?`, job, d.path)
				return e
			}
			if object != "" {
				if !retired && inc == source.Incarnation && seen != job {
					_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET missing_object=?,missing_path=?,absence_parent=? WHERE job_id=? AND relative_path=?`, object, path, filepath.Dir(path), job, d.path)
				} else {
					_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET after_path=?,after_object=? WHERE job_id=? AND relative_path=?`, path, object, job, d.path)
				}
				return e
			}
			_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='seal',reconciled=1 WHERE job_id=? AND relative_path=?`, job, d.path)
		case "coverage":
			var seq int64
			var issuePath string
			var protect bool
			e = tx.QueryRowContext(ctx, `SELECT sequence,relative_path,protect FROM inventory_scan_issues WHERE job_id=? AND sequence>? AND sequence<=? ORDER BY sequence LIMIT 1`, job, d.afterIssue, d.highwater).Scan(&seq, &issuePath, &protect)
			if e == nil {
				blocked := protect && (issuePath == "." || filepath.ToSlash(issuePath) == d.top || strings.HasPrefix(filepath.ToSlash(issuePath), d.top+"/"))
				next := "coverage"
				if blocked {
					next = "blocked"
				}
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase=?,after_issue=? WHERE job_id=? AND relative_path=?`, next, seq, job, d.path)
				return e
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			var revision int64
			if e = tx.QueryRowContext(ctx, `SELECT coverage_revision FROM inventory_reading_jobs WHERE job_id=?`, job).Scan(&revision); e != nil {
				return e
			}
			if revision != d.issueRevision {
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET after_issue=0,issue_highwater=COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=?1 ORDER BY sequence DESC LIMIT 1),0),issue_revision=?3 WHERE job_id=?1 AND relative_path=?2`, job, d.path, revision)
				return e
			}
			var reducing bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_dirty_work WHERE kind='blocks' AND work_key=?)`, job).Scan(&reducing); e != nil || reducing {
				return e
			}
			next := "reconcile"
			if d.reconciled {
				next = "seal"
			}
			_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase=? WHERE job_id=? AND relative_path=?`, next, job, d.path)
		case "seal":
			var reducing bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_dirty_work WHERE kind='blocks' AND work_key=?)`, job).Scan(&reducing); e != nil || reducing {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_reading_seals(job_id,top_level,source_id,incarnation,generation,root_identity,root,coverage_revision,issue_revision,issue_highwater)
 SELECT c.job_id,?,c.source_id,c.incarnation,c.generation,c.root_identity,c.root,b.coverage_revision,c.coverage_revision,? FROM inventory_reading_jobs c JOIN reading_path_source_barrier b ON b.source_id=c.source_id AND b.job_id=c.job_id WHERE c.job_id=? AND b.phase='open'`, d.top, d.highwater, job); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='done',propagated=1 WHERE job_id=? AND relative_path=?`, job, d.path); e != nil {
				return e
			}
			if !d.propagated {
				_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET children=children-1 WHERE job_id=? AND relative_path=?`, job, d.parent)
			}
		}
		return e
	})
}

func (s *Service) blockReadingClosure(ctx context.Context, job, path string) error {
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if _, e := catalog.InventoryFenceTx(ctx, tx, job); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='blocked' WHERE job_id=? AND relative_path=?`, job, path)
		return e
	})
}

// A late issue invalidates seals synchronously; this captured raw directory walk
// merely schedules fresh coverage proof. Already completed parent edges are not
// counted twice. Every rejected/non-top directory and the EOF probe cost a turn.
func (s *Service) renewReadingClosure(ctx context.Context, job string) error {
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if _, e := catalog.InventoryFenceTx(ctx, tx, job); e != nil {
			return e
		}
		var after, fence string
		if e := tx.QueryRowContext(ctx, `SELECT renew_after,renew_fence FROM inventory_reading_jobs WHERE job_id=? AND renewing=1`, job).Scan(&after, &fence); e != nil {
			return e
		}
		var path, top, phase string
		e := tx.QueryRowContext(ctx, `SELECT relative_path,top_level,phase FROM inventory_reading_directories WHERE job_id=? AND relative_path>? AND relative_path<=? ORDER BY relative_path LIMIT 1`, job, after, fence).Scan(&path, &top, &phase)
		if errors.Is(e, sql.ErrNoRows) {
			_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_jobs SET renewing=0 WHERE job_id=?`, job)
			return e
		}
		if e != nil {
			return e
		}
		if path == top && phase == "done" {
			if _, e = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET phase='coverage',after_issue=0,issue_revision=(SELECT coverage_revision FROM inventory_reading_jobs WHERE job_id=?1),issue_highwater=COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=?1 ORDER BY sequence DESC LIMIT 1),0) WHERE job_id=?1 AND relative_path=?2`, job, path); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE inventory_reading_jobs SET renew_after=? WHERE job_id=?`, path, job)
		return e
	})
}

func readingClosureFenceTx(ctx context.Context, tx *sql.Tx, job string, source catalog.LibrarySource) (bool, error) {
	var ok bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_reading_jobs c JOIN inventory_runs r ON r.job_id=c.job_id JOIN reading_path_source_barrier b ON b.source_id=c.source_id
 WHERE c.job_id=? AND c.source_id=? AND c.incarnation=? AND c.generation=? AND c.root_identity=? AND c.root=? AND b.job_id=c.job_id AND b.incarnation=c.incarnation AND b.source_generation=c.generation AND b.root_identity=c.root_identity AND b.root=c.root AND b.phase='open' AND r.scan_mode='first-import')`, job, source.ID, source.Incarnation, source.Generation, source.RootIdentity, source.ResolvedPath).Scan(&ok)
	return ok, e
}
