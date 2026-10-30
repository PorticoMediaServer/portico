package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Deletions are observed only under complete, stable coverage. Failed or
// changing paths keep their prior evidence, just as media files do.
func (s *Service) reconcileSidecars(ctx context.Context, job string, source catalog.LibrarySource) error {
	if more, err := s.pruneDirectoryCachePage(ctx, job, source); more || err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.relative_path FROM inventory_sidecars c JOIN inventory_runs r ON r.source_id=c.source_id WHERE r.job_id=? AND r.authoritative=1 AND c.incarnation=r.root_incarnation AND c.seen_job<>? AND
 (NOT EXISTS(SELECT 1 FROM inventory_run_targets t WHERE t.job_id=r.job_id) OR EXISTS(SELECT 1 FROM inventory_run_targets t WHERE t.job_id=r.job_id AND (c.directory_path=t.relative_path OR substr(c.directory_path,1,length(t.relative_path)+1)=t.relative_path||?))) AND
 NOT EXISTS(SELECT 1 FROM inventory_scan_issues x WHERE x.job_id=r.job_id AND x.protect=1 AND (x.relative_path='.' OR c.relative_path=x.relative_path OR substr(c.relative_path,1,length(x.relative_path)+1)=x.relative_path||?)) ORDER BY c.relative_path LIMIT 32`, job, job, string(filepath.Separator), string(filepath.Separator))
	if err != nil {
		return err
	}
	paths := []string{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, path)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	safe := map[string]bool{}
	for _, path := range paths {
		folder := filepath.Dir(path)
		if _, ok := safe[folder]; ok {
			continue
		}
		err = s.catalog.VerifyInventoryAbsence(ctx, source, job, path)
		if err != nil {
			if errors.Is(err, storage.ErrBusy) || ctx.Err() != nil {
				return err
			}
			if err = s.excludeDirectory(ctx, job, folder, "directory_changed"); err != nil {
				return err
			}
			safe[folder] = false
		} else {
			safe[folder] = true
		}
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		return err
	}
	for _, path := range paths {
		if !safe[filepath.Dir(path)] {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_sidecar_changes(job_id,relative_path) VALUES(?,?)`, job, filepath.Dir(path)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_sidecars WHERE source_id=? AND incarnation=? AND relative_path=? AND seen_job<>?`, source.ID, source.Incarnation, path, job); err != nil {
			return err
		}
	}
	if len(paths) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='analysis_enqueue' WHERE job_id=?`, job)
	}
	if err != nil {
		return err
	}
	return gated.Commit()
}

// sidecarChangePage is how many inventory rows one sidecar-change page reads
// while it holds the writer.
var sidecarChangePage = 64

// The three keyset windows a sidecar-change page reads. Each one is answered in
// order by its index, so LIMIT bounds the rows examined, not only those
// returned.
const (
	sidecarSourceRows = `SELECT id,directory_path,0,root_incarnation=? AND state='available' FROM inventory_objects INDEXED BY inventory_objects_source WHERE source_id=? AND retired=0 AND id>? ORDER BY id LIMIT ?`
	sidecarFolderRows = `SELECT id,directory_path,0,state='available' FROM inventory_objects INDEXED BY inventory_objects_directory WHERE source_id=? AND root_incarnation=? AND directory_path=? AND retired=0 AND id>? ORDER BY id LIMIT ?`
	// The descendant window keys on the index's (directory_path, retired, id)
	// tail. A retired=0 filter after the directory range would cost the index
	// order (and a temporary sort of the whole subtree), so retired rows are
	// read, counted against the window and never queued.
	sidecarDescendantRows = `SELECT id,directory_path,retired,retired=0 AND state='available' FROM inventory_objects INDEXED BY inventory_objects_directory WHERE source_id=? AND root_incarnation=? AND (directory_path,retired,id)>(?,?,?) AND directory_path<? ORDER BY directory_path,retired,id LIMIT ?`
)

// sidecarCursor is where a folder's sidecar change has got to: the index key
// (directory, retired, object id) of the last row a page read. An empty directory means the
// folder's own files are still being read.
type sidecarCursor struct {
	Directory string `json:"d"`
	Retired   int    `json:"r,omitempty"`
	ID        string `json:"i"`
}

// A show/album sidecar may govern child seasons/discs. Queue those files in
// bounded pages; a changed sidecar never invalidates technical/deep facts, and
// a video file is not probed again for it (S6).
//
// Every read is a raw keyset window that its index answers in order, so a page
// examines at most sidecarChangePage rows whatever the size of the source: the
// folder's own files by (directory_path, id), then its descendants as the
// directory_path range below folder+separator, never a scan of the source for
// rows that happen to match. Rows that are no longer available are consumed by
// the window without being queued.
func (s *Service) enqueueSidecarChanges(ctx context.Context, job string, source catalog.LibrarySource) (bool, error) {
	var folder, cursorText string
	err := s.db.QueryRowContext(ctx, `SELECT relative_path,cursor FROM inventory_sidecar_changes WHERE job_id=? ORDER BY relative_path LIMIT 1`, job).Scan(&folder, &cursorText)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cursor sidecarCursor
	if cursorText != "" {
		if err = json.Unmarshal([]byte(cursorText), &cursor); err != nil {
			return false, err
		}
	}
	if s.LocalMetadata != nil {
		s.LocalMetadata.InvalidateSidecars(filepath.Join(source.ResolvedPath, folder))
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
	type row struct {
		id, directory string
		retired       int
		available     bool
	}
	read := func(query string, args ...any) ([]row, error) {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []row{}
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.id, &r.directory, &r.retired, &r.available); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	var window []row
	done := false
	if folder == "." {
		window, err = read(sidecarSourceRows, source.Incarnation, source.ID, cursor.ID, sidecarChangePage)
		done = err == nil && len(window) < sidecarChangePage
	} else {
		// Every descendant directory sorts in [folder+"/", folder+"0"): the
		// separator's successor bounds the range, and a sibling such as
		// "Album (Deluxe)" or "Album2" falls outside it.
		below, after := folder+string(filepath.Separator), folder+string(rune(filepath.Separator+1))
		if cursor.Directory == "" || cursor.Directory == folder {
			window, err = read(sidecarFolderRows, source.ID, source.Incarnation, folder, cursor.ID, sidecarChangePage)
			cursor = sidecarCursor{Directory: below}
		}
		if remaining := sidecarChangePage - len(window); err == nil && remaining > 0 {
			var descendants []row
			descendants, err = read(sidecarDescendantRows, source.ID, source.Incarnation, cursor.Directory, cursor.Retired, cursor.ID, after, remaining)
			window = append(window, descendants...)
			done = len(descendants) < remaining
		}
	}
	if err != nil {
		return false, err
	}
	for _, r := range window {
		if !r.available {
			continue
		}
		// Only the sidecar-derived operations are dropped. The analysis stays
		// of this revision, so a video file re-reads its local metadata and
		// subtitles without a new probe (refreshLocalMetadata).
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_state='pending',analysis_operations_json=COALESCE((SELECT json_group_array(value) FROM json_each(analysis_operations_json) WHERE value NOT IN('local_metadata','subtitles')),'[]') WHERE id=?`, r.id); err != nil {
			return false, err
		}
	}
	if done {
		_, err = tx.ExecContext(ctx, `DELETE FROM inventory_sidecar_changes WHERE job_id=? AND relative_path=?`, job, folder)
	} else {
		last := window[len(window)-1]
		next, _ := json.Marshal(sidecarCursor{Directory: last.directory, Retired: last.retired, ID: last.id})
		_, err = tx.ExecContext(ctx, `UPDATE inventory_sidecar_changes SET cursor=? WHERE job_id=? AND relative_path=?`, string(next), job, folder)
	}
	if err != nil {
		return false, err
	}
	return true, gated.Commit()
}
