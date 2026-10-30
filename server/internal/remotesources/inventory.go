package remotesources

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type continuation struct{ ID, After string }

func cursor(c continuation) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func parseCursor(value string) (continuation, error) {
	var c continuation
	if len(value) > 16<<10 {
		return c, storage.ErrRemoteCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.ID == "" || len(c.After) > 8192 {
		return c, storage.ErrRemoteCursor
	}
	return c, nil
}

// ListPage materializes only this directory in the private adapter cache, then
// returns a bounded immutable page. Partial provider output never becomes a page.
// The durable inventory owner commits these pages and its continuation atomically.
func (s *Service) ListPage(ctx context.Context, job, directory, after string) (storage.RemotePage, error) {
	return s.ListInventoryPage(ctx, job, directory, after, 128)
}

// ListInventoryPage selects a real bounded page from immutable source evidence.
// The cursor identifies a source/key boundary, independent of page size, so a
// restarted scanner can use a smaller next page without skipping any names.
// Replay is exact for the tuple (source listing, cursor, limit).
func (s *Service) ListInventoryPage(ctx context.Context, job, directory, after string, limit int) (storage.RemotePage, error) {
	ctx = listingContext(ctx, job)
	if limit < 1 || limit > 128 {
		return storage.RemotePage{}, storage.ErrRemoteLimit
	}
	b, relative, ok := s.find(directory)
	if !ok || b.Removed {
		return storage.RemotePage{}, storage.ErrRemoteOffline
	}
	generation, err := s.generation(ctx, b)
	if err != nil {
		return storage.RemotePage{}, err
	}
	c := continuation{}
	if after != "" {
		c, err = parseCursor(after)
		if err != nil {
			return storage.RemotePage{}, err
		}
	}
	// A retained producer owns provider I/O across request/worker quanta.
	release, err := s.claimListing(job + ":" + directory)
	if err != nil {
		return storage.RemotePage{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var listedJob, listedDirectory, listedGeneration, sourceID string
	var ready bool
	if c.ID == "" {
		err = s.db.QueryRowContext(ctx, `SELECT id FROM remote_listing_sessions WHERE job_id=? AND directory=?`, job, directory).Scan(&c.ID)
		if errors.Is(err, sql.ErrNoRows) {
			c.ID = identity.Token()
			// The library folder this scan (or its verification pass) is for,
			// and the directory under it, go on the listing for retention.
			_, err = listingExec(ctx, s.db, `INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,created_at,inventory_source_id,incarnation,relative_path)
 SELECT ?1,?2,?3,?4,?5,?6,COALESCE(r.source_id,''),COALESCE(r.root_incarnation,''),CASE WHEN f.root IS NULL THEN '' WHEN ?5=f.root THEN '.' WHEN substr(?5,1,length(f.root)+1)=f.root||?7 THEN substr(?5,length(f.root)+2) ELSE '' END
 FROM (SELECT 1) LEFT JOIN inventory_runs r ON r.job_id=COALESCE((SELECT c.job_id FROM remote_inventory_checks c WHERE c.check_job=?2),?2) LEFT JOIN library_sources f ON f.id=r.source_id`, c.ID, job, b.ID, generation, directory, time.Now().Unix(), string(filepath.Separator))
		}
		if err != nil {
			return storage.RemotePage{}, err
		}
	}
	err = s.db.QueryRowContext(ctx, `SELECT job_id,directory,generation,source_id,ready FROM remote_listing_sessions WHERE id=?`, c.ID).Scan(&listedJob, &listedDirectory, &listedGeneration, &sourceID, &ready)
	if err != nil || listedJob != job || listedDirectory != directory || sourceID != b.ID {
		return storage.RemotePage{}, storage.ErrRemoteCursor
	}
	if listedGeneration != generation {
		return storage.RemotePage{}, storage.ErrRemoteChanged
	}
	if !ready {
		err = s.startNativeListing(ctx, b, relative, c.ID, generation)
		if err != nil {
			// startNativeListing reports cached or persisted listing outcomes
			// without contacting the provider; the completion path records the
			// real evidence once. Do not replay it here.
			return storage.RemotePage{}, mapError(err)
		}
		if err = s.db.QueryRowContext(ctx, `SELECT ready FROM remote_listing_sessions WHERE id=?`, c.ID).Scan(&ready); err != nil {
			return storage.RemotePage{}, err
		}
		if !ready {
			return storage.RemotePage{Limit: limit, ID: c.ID, SourceID: b.ID, Directory: directory, Generation: generation, Cursor: after, Next: cursor(continuation{c.ID, ""}), Entries: []storage.Snapshot{}, Pending: true}, nil
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT snapshot FROM remote_listing_entries WHERE listing_id=? AND name>? ORDER BY name LIMIT ?`, c.ID, c.After, limit+1)
	if err != nil {
		return storage.RemotePage{}, err
	}
	entries := []storage.Snapshot{}
	for rows.Next() {
		var raw string
		var v storage.Snapshot
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			break
		}
		entries = append(entries, v)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return storage.RemotePage{}, err
	}
	if readErr != nil {
		return storage.RemotePage{}, readErr
	}
	complete := len(entries) <= limit
	if !complete {
		entries = entries[:limit]
	}
	last := c.After
	if len(entries) > 0 {
		last = entries[len(entries)-1].Path
	}
	return storage.RemotePage{Limit: limit, ID: c.ID, SourceID: b.ID, Directory: directory, Generation: generation, Cursor: after, Next: cursor(continuation{c.ID, last}), Entries: entries, Complete: complete}, nil
}
func (s *Service) buildListing(ctx context.Context, b binding, relative, id, generation, owner string, retired func()) error {
	defer retired()
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	capture, err := s.serveListingFenceTx(ctx, view.Tx(), b, relative, id, generation)
	if err == nil {
		err = s.nativeListingFence(ctx, view.Tx(), id, owner)
	}
	view.Rollback()
	if err != nil {
		return err
	}
	process, err := s.ensureSourceServe(ctx, SourceReadBinding{Purpose: SourceInventory, Observation: ObservationRef{SourceID: b.ID, Generation: generation}, Root: b.Root, Profile: capture.profile}, "")
	if err != nil {
		return err
	}
	return s.buildServeSnapshot(ctx, process, b, relative, id, generation)
}

// CommitPage is called in the SAME transaction that inserts scanner children.
// Advancing a cursor before applying its validated page is impossible here.
func (s *Service) CommitPage(ctx context.Context, tx *sql.Tx, p storage.RemotePage) error {
	if tx == nil {
		return storage.ErrRemoteCursor
	}
	var job, gen string
	var removed, ready bool
	err := tx.QueryRowContext(ctx, `SELECT l.job_id,CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END,r.removed,l.ready FROM remote_listing_sessions l JOIN remote_sources r ON r.id=l.source_id LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id WHERE l.id=? AND l.source_id=? AND l.directory=? AND l.generation=?`, p.ID, p.SourceID, p.Directory, p.Generation).Scan(&job, &gen, &removed, &ready)
	if err != nil || removed {
		return storage.ErrRemoteCursor
	}
	// Readiness may advance after a Pending response; committing its empty
	// continuation is harmless and must not fail an otherwise healthy scan.
	if p.Pending {
		if len(p.Entries) != 0 || p.Complete {
			return storage.ErrRemoteCursor
		}
	} else if !ready {
		return storage.ErrRemoteCursor
	}
	if gen != p.Generation {
		return storage.ErrRemoteChanged
	}
	var status string
	if tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, job).Scan(&status) != nil || status != "running" {
		return context.Canceled
	}
	source, err := catalog.InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(source.ResolvedPath, p.Directory)
	if err != nil || source.Kind != "remote" || !filepath.IsLocal(relative) {
		return storage.ErrRemoteChanged
	}
	var capturedPolicy, currentPolicy int64
	if err = tx.QueryRowContext(ctx, `SELECT r.policy_revision,p.revision FROM inventory_runs r JOIN library_scan_policies p ON p.library_id=? WHERE r.job_id=?`, source.LibraryID, job).Scan(&capturedPolicy, &currentPolicy); err != nil {
		return err
	}
	if capturedPolicy < 1 || capturedPolicy != currentPolicy {
		return storage.ErrRemoteChanged
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT cursor FROM remote_scan_cursors WHERE job_id=? AND path=?`, job, p.Directory).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		current = ""
	} else if err != nil {
		return err
	}
	if current != p.Cursor && current != p.Next {
		return storage.ErrRemoteCursor
	}
	if !p.Pending {
		if err = s.commitListingObservationsTx(ctx, tx, job, p, current == p.Next); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_scan_cursors(job_id,path,listing_id,cursor) VALUES(?,?,?,?) ON CONFLICT(job_id,path) DO UPDATE SET cursor=excluded.cursor,listing_id=excluded.listing_id`, job, p.Directory, p.ID, p.Next)
	return err
}
