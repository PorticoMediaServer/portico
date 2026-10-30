package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const maxStrictIntentRows = 4096

type integrationRefreshKey struct{}

// These rows are accepted work, not scan/query results. Ordinary source-wide
// intent is deliberately separate from strict folders and ordinary path notices.
func maintainIntentsTx(ctx context.Context, tx *sql.Tx, source strictSource, folders []string, whole bool) (bool, error) {
	if err := retireChangedIntentsTx(ctx, tx, source.id); err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT relative_path,created_ms FROM inventory_strict_intents WHERE source_id=? LIMIT ?`, source.id, maxPendingNotices+1)
	if err != nil {
		return false, err
	}
	unique := map[string]bool{}
	created := time.Now().UnixMilli()
	oldCount := 0
	for rows.Next() {
		var path string
		var at int64
		if err = rows.Scan(&path, &at); err != nil {
			rows.Close()
			return false, err
		}
		oldCount++
		unique[path] = true
		if at < created {
			created = at
		}
		if path == "." {
			whole = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, folder := range folders {
		if folder == "." || !filepath.IsLocal(folder) {
			return false, strictRefusal(StrictRootRefused, "")
		}
		unique[folder] = true
	}
	// Never reject the 501st user target: explicitly retain one source-wide scan.
	if len(unique) > maxPendingNotices {
		whole = true
	}
	targets := []string{}
	if whole {
		targets = []string{"."}
	} else {
		for folder := range unique {
			covered := false
			for parent := range unique {
				if parent != folder && within(parent, folder) {
					covered = true
					break
				}
			}
			if !covered {
				targets = append(targets, folder)
			}
		}
		sort.Strings(targets)
	}
	var total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_strict_intents`).Scan(&total); err != nil {
		return false, err
	}
	if total-oldCount+len(targets) > maxStrictIntentRows {
		return false, strictRefusal(StrictOverloaded, "")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_strict_intents WHERE source_id=?`, source.id); err != nil {
		return false, err
	}
	mode := "strict"
	if whole {
		mode = "incremental"
	}
	for _, folder := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_strict_intents(source_id,relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, source.id, folder, source.library, source.generation, source.incarnation, source.identity, source.resolved, source.configured, source.kind, source.follow, mode, created); err != nil {
			return false, err
		}
	}
	return whole, nil
}

// Invalidation is a lifetime/configuration comparison, never an age-based expiry.
func retireChangedIntentsTx(ctx context.Context, tx *sql.Tx, source string) error {
	const stale = `SELECT 1 FROM inventory_strict_intents AS i WHERE i.source_id=? AND NOT EXISTS(SELECT 1 FROM library_sources s WHERE s.id=i.source_id AND s.library_id=i.library_id AND s.generation=i.source_generation AND s.incarnation=i.root_incarnation AND s.root_identity=i.root_identity AND s.root=i.root AND s.configured_root=i.configured_root AND s.kind=i.source_kind AND s.follow_symlinks=i.follow_symlinks AND (i.mode<>'strict' OR s.identity_confirmed=1) AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id))`
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS("+stale+")", source).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM inventory_strict_intents AS i WHERE i.source_id=? AND NOT EXISTS(SELECT 1 FROM library_sources s WHERE s.id=i.source_id AND s.library_id=i.library_id AND s.generation=i.source_generation AND s.incarnation=i.root_incarnation AND s.root_identity=i.root_identity AND s.root=i.root AND s.configured_root=i.configured_root AND s.kind=i.source_kind AND s.follow_symlinks=i.follow_symlinks AND (i.mode<>'strict' OR s.identity_confirmed=1) AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id))`, source)
	return err
}

// RefreshLibraries accepts one coalesced ordinary library refresh. The existing
// scanner owns per-source jobs; an already busy/paused source needs a follow-up
// intent, since the running scan may already have passed the changed directory.
func (s *Service) RefreshLibraries(ctx context.Context, library string, authorize func(*sql.Tx, string) error) (PathReport, error) {
	out := PathReport{Paths: []ReportedPath{}, Jobs: []Job{}}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx, library); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,library_id,configured_root,root,incarnation,root_identity,kind,generation,follow_symlinks,identity_confirmed FROM library_sources s WHERE enabled=1 AND (?='' OR library_id=?) AND health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id) ORDER BY library_id,id LIMIT ?`, library, library, maxStrictIntentRows+1)
	if err != nil {
		return out, err
	}
	sources := []strictSource{}
	for rows.Next() {
		var source strictSource
		if err = rows.Scan(&source.id, &source.library, &source.configured, &source.resolved, &source.incarnation, &source.identity, &source.kind, &source.generation, &source.follow, &source.confirmed); err != nil {
			rows.Close()
			return out, err
		}
		sources = append(sources, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(sources) > maxStrictIntentRows {
		return out, strictRefusal(StrictOverloaded, "")
	}
	if len(sources) == 0 && library != "" {
		// An existing library with no enabled sources still accepts refresh;
		// disabled/private sources are never silently re-enabled or scanned.
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries l WHERE l.id=? AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=l.id))`, library).Scan(&exists); err != nil {
			return out, err
		}
		if !exists {
			return out, sql.ErrNoRows
		}
	}
	for _, source := range sources {
		if authorize != nil {
			if err = authorize(tx, source.library); err != nil {
				return out, err
			}
		}
		if _, err = maintainIntentsTx(ctx, tx, source, nil, true); err != nil {
			return out, err
		}
		job, e := transferIntentsTx(ctx, tx, source, []string{"."}, true)
		if e != nil {
			return out, e
		}
		if job.ID != "" {
			out.Jobs = append(out.Jobs, job)
		} else {
			out.Deferred = true
		}
	}
	if err = gated.Commit(); err != nil {
		return PathReport{}, err
	}
	return out, nil
}

// Caller has re-proved strict paths outside the writer. The same configuration
// fences and active slot are checked again here, in the transfer transaction.
func transferIntentsTx(ctx context.Context, tx *sql.Tx, source strictSource, folders []string, whole bool) (Job, error) {
	if err := retireChangedIntentsTx(ctx, tx, source.id); err != nil {
		return Job{}, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=? AND j.status IN('queued','running','paused'))`, source.id).Scan(&active); err != nil {
		return Job{}, err
	}
	if active && whole {
		return Job{}, nil
	}
	if len(folders) == 0 || len(folders) > 16 && !whole {
		return Job{}, nil
	}
	mode := "strict"
	if whole {
		mode = "incremental"
		if len(folders) != 1 || folders[0] != "." {
			return Job{}, nil
		}
	}
	// Proved paths are an exact bounded batch. A coalesced successor with the
	// same row count cannot replace them between proof and this writer.
	for _, folder := range folders {
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_strict_intents WHERE source_id=? AND relative_path=? AND mode=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND root=? AND configured_root=? AND source_kind=? AND follow_symlinks=? AND library_id=?)`, source.id, folder, mode, source.generation, source.incarnation, source.identity, source.resolved, source.configured, source.kind, source.follow, source.library).Scan(&current); err != nil {
			return Job{}, err
		} else if !current {
			return Job{}, nil
		}
	}
	var err error
	var job Job
	if whole {
		job, err = queueSourceTx(context.WithValue(ctx, integrationRefreshKey{}, true), tx, source.id, false)
	} else {
		job, err = queueStrictTargetsTx(ctx, tx, source.id, folders)
	}
	if err != nil {
		var refusal *StrictPathRefusal
		if errors.As(err, &refusal) && refusal.Retryable() {
			return Job{}, nil
		}
		return Job{}, err
	}
	for _, folder := range folders {
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_strict_intents WHERE source_id=? AND relative_path=? AND mode=? AND source_generation=? AND root_incarnation=? AND root_identity=?`, source.id, folder, mode, source.generation, source.incarnation, source.identity); err != nil {
			return Job{}, err
		}
	}
	return job, nil
}

// Back off failed proofs as real deferred work, rather than waking a hot loop.
func (s *Service) startStrictIntents(ctx context.Context) bool {
	if time.Now().Before(s.strictIntentRetry) {
		return true
	}
	s.strictIntentRetry = time.Now().Add(2 * time.Second)
	query := func() ([]string, error) {
		rows, err := dbwork.Query(ctx, s.db, `SELECT DISTINCT i.source_id FROM inventory_strict_intents i WHERE i.source_id>? AND (NOT EXISTS(SELECT 1 FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=i.source_id AND j.status IN('queued','running','paused')) OR NOT EXISTS(SELECT 1 FROM library_sources s WHERE s.id=i.source_id AND s.library_id=i.library_id AND s.generation=i.source_generation AND s.incarnation=i.root_incarnation AND s.root_identity=i.root_identity AND s.root=i.root AND s.configured_root=i.configured_root AND s.kind=i.source_kind AND s.follow_symlinks=i.follow_symlinks AND (i.mode<>'strict' OR s.identity_confirmed=1) AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id))) ORDER BY i.source_id LIMIT 8`, s.strictIntentAfter)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		readErr := rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		return ids, readErr
	}
	ids, err := query()
	if err != nil {
		return true
	}
	if len(ids) == 0 && s.strictIntentAfter != "" {
		s.strictIntentAfter = ""
		ids, err = query()
		if err != nil {
			return true
		}
	}
	if len(ids) == 0 {
		return false
	}
	proof, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, id := range ids {
		if proof.Err() != nil {
			break
		}
		s.strictIntentAfter = id
		s.startStrictIntent(ctx, proof, id)
	}
	return true
}
func (s *Service) startStrictIntent(ctx, proof context.Context, id string) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return
	}
	if err = retireChangedIntentsTx(ctx, gated.Tx(), id); err == nil {
		err = gated.Commit()
	}
	if err != nil {
		gated.Rollback()
		return
	}
	if s.strictIntentFolders == nil {
		s.strictIntentFolders = map[string]string{}
	}
	// This in-memory cursor is bounded by the durable intent cap; it contains
	// scheduler position, never query results. A restart safely starts at EOF's beginning.
	if len(s.strictIntentFolders) >= maxStrictIntentRows {
		s.strictIntentFolders = map[string]string{}
	}
	read := func(after string) (strictSource, []string, bool, error) {
		source := strictSource{reportSource: reportSource{id: id}, confirmed: true}
		folders := []string{}
		whole := false
		rows, e := dbwork.Query(ctx, s.db, `SELECT relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode FROM inventory_strict_intents WHERE source_id=? AND relative_path>? ORDER BY relative_path LIMIT 16`, id, after)
		if e != nil {
			return source, nil, false, e
		}
		for rows.Next() {
			var folder, mode string
			if e = rows.Scan(&folder, &source.library, &source.generation, &source.incarnation, &source.identity, &source.resolved, &source.configured, &source.kind, &source.follow, &mode); e != nil {
				break
			}
			folders = append(folders, folder)
			whole = mode == "incremental"
		}
		readErr := rows.Err()
		rows.Close()
		if e != nil {
			return source, nil, false, e
		}
		return source, folders, whole, readErr
	}
	source, folders, whole, err := read(s.strictIntentFolders[id])
	if err != nil {
		return
	}
	if len(folders) == 0 {
		delete(s.strictIntentFolders, id)
		source, folders, whole, err = read("")
		if err != nil || len(folders) == 0 {
			return
		}
	}
	var active bool
	if err = dbwork.QueryRow(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=? AND j.status IN('queued','running','paused'))`, id).Scan(&active); err != nil || active {
		return
	}
	verified := []string{}
	if whole {
		verified = folders
	} else {
		for _, folder := range folders {
			if proof.Err() != nil {
				break
			}
			s.strictIntentFolders[id] = folder
			proved, e := s.strictFolder(proof, source, folder, filepath.Join(source.resolved, folder))
			if e == nil && proved == folder {
				verified = append(verified, folder)
			}
		}
	}
	if len(verified) == 0 {
		return
	}
	// A proof budget ending cannot revoke the prefix already proved; its writer
	// joins the original worker lifetime, with fresh source/path identity checks.
	gated, err = dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return
	}
	defer gated.Rollback()
	if _, err = transferIntentsTx(ctx, gated.Tx(), source, verified, whole); err == nil {
		_ = gated.Commit()
	}
}
