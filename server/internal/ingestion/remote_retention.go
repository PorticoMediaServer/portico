package ingestion

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"time"
)

// remoteListingWindow is how many of a folder's listings one retention turn
// looks at.
const remoteListingWindow = 64

// remoteListingCursor is where the retention walk over one folder's listings
// has got to: (created_at,id) of the last listing looked at. It is kept in
// memory; after a restart the walk starts again from the front, which is one
// bounded pass.
type remoteListingCursor struct {
	created int64
	id      string
}

type remoteListingCursors struct {
	sync.Mutex
	at map[string]remoteListingCursor
}

// pruneRemoteListingsTx retires a folder's remote listing snapshots: those of
// its finished runs (and their verification passes) once past cutoff, and
// old ones whose run has gone. One current ready listing per live cached
// directory is kept for inherited ignore configuration, along with anything
// an active run or verification pass still uses. Each call looks at one
// window of the folder's listings from where the last call stopped, and
// deletes from at most one listing in bounded chunks. It reports whether it
// did anything or more listings remain; at the end of the folder's listings
// the walk starts again from the front.
func (s *Service) pruneRemoteListingsTx(ctx context.Context, tx *sql.Tx, source, cutoff string) (bool, error) {
	s.remoteListings.Lock()
	defer s.remoteListings.Unlock()
	if s.remoteListings.at == nil {
		s.remoteListings.at = map[string]remoteListingCursor{}
	}
	at := s.remoteListings.at[source]
	sep := string(filepath.Separator)
	// A listing is kept for its directory while it is the newest ready one
	// and the directory is in the live folder's cache under the same
	// incarnation: a point probe of the cache's primary key.
	const current = `l.ready=1 AND l.id=(SELECT x.id FROM remote_listing_sessions x WHERE x.source_id=l.source_id AND x.directory=l.directory AND x.ready=1 ORDER BY x.created_at DESC,x.id DESC LIMIT 1)
 AND EXISTS(SELECT 1 FROM library_sources f JOIN inventory_directory_cache d ON d.source_id=f.id AND d.incarnation=f.incarnation AND d.relative_path=l.relative_path
  WHERE f.id=l.inventory_source_id AND f.enabled=1 AND f.incarnation=l.incarnation AND l.directory=CASE WHEN l.relative_path='.' THEN f.root ELSE f.root||?4||l.relative_path END)`
	rows, err := tx.QueryContext(ctx, `SELECT l.id,l.job_id,l.directory,l.created_at,
 (EXISTS(SELECT 1 FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id=COALESCE((SELECT c.job_id FROM remote_inventory_checks c WHERE c.check_job=l.job_id),l.job_id)
   AND r.source_id=?1 AND j.status NOT IN('queued','running','paused') AND COALESCE(nullif(r.finished_at,''),j.created_at)<?2) AND NOT(`+current+`))
 OR (l.created_at<?3 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=l.job_id)
   AND NOT EXISTS(SELECT 1 FROM remote_inventory_checks c JOIN jobs j ON j.id=c.job_id WHERE c.check_job=l.job_id AND j.status IN('queued','running','paused'))
   AND (l.ready=0 OR EXISTS(SELECT 1 FROM remote_listing_sessions newer WHERE newer.source_id=l.source_id AND newer.directory=l.directory AND newer.ready=1 AND (newer.created_at>l.created_at OR newer.created_at=l.created_at AND newer.id>l.id))
    OR NOT EXISTS(SELECT 1 FROM library_sources f JOIN inventory_directory_cache d ON d.source_id=f.id AND d.incarnation=f.incarnation AND d.relative_path=l.relative_path
     WHERE f.id=l.inventory_source_id AND f.enabled=1 AND f.incarnation=l.incarnation AND l.directory=CASE WHEN l.relative_path='.' THEN f.root ELSE f.root||?4||l.relative_path END)))
 FROM remote_listing_sessions l INDEXED BY remote_listing_inventory
 WHERE l.inventory_source_id=?1 AND (l.created_at,l.id)>(?5,?6)
 ORDER BY l.created_at,l.id LIMIT ?7`, source, cutoff, time.Now().Add(-15*time.Minute).Unix(), sep, at.created, at.id, remoteListingWindow)
	if err != nil {
		return false, err
	}
	type listing struct {
		id, job, directory string
		created            int64
		doomed             bool
	}
	var window []listing
	for rows.Next() {
		var l listing
		if err = rows.Scan(&l.id, &l.job, &l.directory, &l.created, &l.doomed); err != nil {
			rows.Close()
			return false, err
		}
		window = append(window, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, l := range window {
		if !l.doomed {
			at = remoteListingCursor{l.created, l.id}
			continue
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE rowid IN(SELECT rowid FROM remote_listing_entries WHERE listing_id=? LIMIT ?)`, l.id, scanRetentionBatch)
		if err != nil {
			return false, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			// The listing's own verification record and the verification
			// pass it was taken by go with it; both are key lookups.
			if _, err = tx.ExecContext(ctx, `DELETE FROM remote_inventory_checks WHERE job_id=? AND directory=? AND listing_id=?`, l.job, l.directory, l.id); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM remote_inventory_checks WHERE check_job=?`, l.job); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM remote_listing_sessions WHERE id=?`, l.id); err != nil {
				return false, err
			}
			at = remoteListingCursor{l.created, l.id}
		}
		s.remoteListings.at[source] = at
		return true, nil
	}
	if len(window) == remoteListingWindow {
		s.remoteListings.at[source] = at
		return true, nil
	}
	delete(s.remoteListings.at, source)
	return false, nil
}
