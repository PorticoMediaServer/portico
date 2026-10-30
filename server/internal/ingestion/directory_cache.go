package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// directoryPruneWindow is how many cached children of one folder a prune
// turn reads.
const directoryPruneWindow = 64

// The directory cache describes the current tree, not a history of every
// folder name. A fully listed, verified parent proves which old children went.
// Protected subtrees keep their checkpoints until they can be observed again.
//
// Only folders that may hold stale children are looked at: the page commit
// marks one listed again at a changed revision or listed with a name skipped
// (inventory_directory_prune), and removing a cached folder here marks it in
// turn so its own children, now without a parent, are visited. One turn reads
// a bounded window of one marked folder's children from its saved place and
// decides each child exactly as a whole-cache check would: a child is removed
// when its parent was listed done in this run and did not list it again, or
// when its parent is no longer cached, unless a protecting issue covers it.
// A folder that met a protected child is deferred and looked at once in the
// next run. With nothing marked, no writer is opened.
func (s *Service) pruneDirectoryCachePage(ctx context.Context, job string, source catalog.LibrarySource) (bool, error) {
	if key, err := nextDirectoryPruneKey(ctx, s.db, source, job); err != nil || key == nil {
		return false, err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		return false, err
	}
	key, err := nextDirectoryPruneKey(ctx, tx, source, job)
	if err != nil || key == nil {
		return false, err
	}
	if key.job != job || key.state != "ready" {
		// A key from an earlier run, or deferred by one: the decision is made
		// against this run's listing, so its walk starts again.
		*key = directoryPruneKey{parent: key.parent, job: job, state: "ready"}
	}
	// The parent's own facts are the same for every child in the window.
	var parentRevision string
	parentDone := true
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM inventory_directories WHERE job_id=? AND relative_path=? AND state='done' AND skipped=0`, job, key.parent).Scan(&parentRevision); errors.Is(err, sql.ErrNoRows) {
		parentDone, err = false, nil
	}
	if err != nil {
		return false, err
	}
	var parentCached bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directory_cache WHERE source_id=? AND incarnation=? AND relative_path=?)`, source.ID, source.Incarnation, key.parent).Scan(&parentCached); err != nil {
		return false, err
	}
	// A raw window of the children in index order, eligible or not, so a
	// folder of a million kept children is read once, a window at a time.
	rows, err := tx.QueryContext(ctx, `SELECT rowid,relative_path,parent_revision,seen_job FROM inventory_directory_cache INDEXED BY inventory_directory_children
 WHERE source_id=? AND incarnation=? AND parent_path=? AND (parent_revision,relative_path)>(?,?) ORDER BY parent_revision,relative_path LIMIT ?`,
		source.ID, source.Incarnation, key.parent, key.cursorRevision, key.cursorPath, directoryPruneWindow)
	if err != nil {
		return false, err
	}
	type child struct {
		rowid                   int64
		path, revision, seenJob string
	}
	var window, eligible []child
	for rows.Next() {
		var c child
		if err = rows.Scan(&c.rowid, &c.path, &c.revision, &c.seenJob); err != nil {
			rows.Close()
			return false, err
		}
		window = append(window, c)
		if c.path != "." && (parentDone && (parentRevision != c.revision || c.seenJob != job) || !parentCached) {
			eligible = append(eligible, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	protected, err := protectedPaths(ctx, tx, job, eligible, func(c child) string { return c.path })
	if err != nil {
		return false, err
	}
	for _, c := range eligible {
		if protected[c.path] {
			key.sawProtected = true
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_directory_cache WHERE rowid=?`, c.rowid); err != nil {
			return false, err
		}
		// Its own children, if it has any, have lost their parent: visit
		// them in turn.
		var parent bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directory_cache INDEXED BY inventory_directory_children WHERE source_id=? AND incarnation=? AND parent_path=?)`, source.ID, source.Incarnation, c.path).Scan(&parent); err != nil {
			return false, err
		}
		if !parent {
			continue
		}
		if err = catalog.MarkDirectoryPruneTx(ctx, tx, source.ID, source.Incarnation, c.path, job); err != nil {
			return false, err
		}
	}
	switch {
	case len(window) == directoryPruneWindow:
		last := window[len(window)-1]
		key.cursorRevision, key.cursorPath = last.revision, last.path
		_, err = tx.ExecContext(ctx, `UPDATE inventory_directory_prune SET job_id=?,state='ready',cursor_parent_revision=?,cursor_relative_path=?,saw_protected=? WHERE source_id=? AND incarnation=? AND parent_path=?`,
			job, key.cursorRevision, key.cursorPath, key.sawProtected, source.ID, source.Incarnation, key.parent)
	case key.sawProtected:
		// A protected child is kept with everything under it; the next run
		// looks at this folder again, once, against its own issues.
		_, err = tx.ExecContext(ctx, `UPDATE inventory_directory_prune SET job_id=?,state='deferred',cursor_parent_revision='',cursor_relative_path='',saw_protected=0 WHERE source_id=? AND incarnation=? AND parent_path=?`,
			job, source.ID, source.Incarnation, key.parent)
	default:
		_, err = tx.ExecContext(ctx, `DELETE FROM inventory_directory_prune WHERE source_id=? AND incarnation=? AND parent_path=?`, source.ID, source.Incarnation, key.parent)
	}
	if err != nil {
		return false, err
	}
	return true, gated.Commit()
}

type directoryPruneKey struct {
	parent, job, state         string
	cursorRevision, cursorPath string
	sawProtected               bool
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// nextDirectoryPruneKey is the first ready key of the source, else the first
// key deferred by another run; nil when there is none. A key this run
// deferred waits for the next run. Each probe is one index seek.
func nextDirectoryPruneKey(ctx context.Context, db queryRower, source catalog.LibrarySource, job string) (*directoryPruneKey, error) {
	for _, q := range []string{
		`SELECT parent_path,job_id,state,cursor_parent_revision,cursor_relative_path,saw_protected FROM inventory_directory_prune INDEXED BY inventory_directory_prune_due WHERE source_id=?1 AND incarnation=?2 AND state='ready' ORDER BY job_id,parent_path LIMIT 1`,
		`SELECT parent_path,job_id,state,cursor_parent_revision,cursor_relative_path,saw_protected FROM inventory_directory_prune INDEXED BY inventory_directory_prune_due WHERE source_id=?1 AND incarnation=?2 AND state='deferred' AND job_id<?3 ORDER BY job_id,parent_path LIMIT 1`,
		`SELECT parent_path,job_id,state,cursor_parent_revision,cursor_relative_path,saw_protected FROM inventory_directory_prune INDEXED BY inventory_directory_prune_due WHERE source_id=?1 AND incarnation=?2 AND state='deferred' AND job_id>?3 ORDER BY job_id,parent_path LIMIT 1`,
	} {
		var k directoryPruneKey
		err := db.QueryRowContext(ctx, q, source.ID, source.Incarnation, job).Scan(&k.parent, &k.job, &k.state, &k.cursorRevision, &k.cursorPath, &k.sawProtected)
		if err == nil {
			return &k, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return nil, nil
}

// protectedPaths reports which paths a protecting issue of job covers: one at
// the root, at the path itself or at any folder above it. The folders above
// each path are found by splitting it, and all are probed in one query on the
// protected-issue index, so the cost is the window's paths times their depth.
func protectedPaths[T any](ctx context.Context, tx *sql.Tx, job string, items []T, path func(T) string) (map[string]bool, error) {
	if len(items) == 0 {
		return nil, nil
	}
	seen := map[string]bool{".": true}
	scopes := []string{"."}
	for _, item := range items {
		for p := path(item); p != "." && p != "" && !seen[p]; p = filepath.Dir(p) {
			seen[p] = true
			scopes = append(scopes, p)
		}
	}
	raw, _ := json.Marshal(scopes)
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT x.relative_path FROM json_each(?) a CROSS JOIN inventory_scan_issues x INDEXED BY inventory_scan_issues_protected ON x.job_id=? AND x.relative_path=a.value WHERE x.protect=1`, string(raw), job)
	if err != nil {
		return nil, err
	}
	issues := map[string]bool{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		issues[p] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, item := range items {
		p := path(item)
		covered := issues["."]
		for q := p; !covered && q != "." && q != ""; q = filepath.Dir(q) {
			covered = issues[q]
		}
		out[p] = covered
	}
	return out, nil
}
