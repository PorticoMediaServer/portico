package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// inventorySnapshot gives observation evidence an adapter namespace. Neither
// these hashes nor directory listing IDs are playback byte-continuity grants.
func inventorySnapshot(b binding, generation string, v storage.Snapshot) storage.Snapshot {
	v.Revision = generation + ":" + v.Revision
	v.ChangeToken = v.Revision
	if v.ObjectID != "" {
		v.ObjectIdentity = remoteVersionScope(b, generation) + ":" + v.ObjectID
	}
	if v.Directory {
		v.ObjectIdentity = storage.RemoteDirectoryIdentity(b.ID, v.Path)
	}
	return v
}

// A claim is nonblocking and scoped to one listing. Never hold a global mutex
// across provider I/O; an inaccessible source must not stall unrelated sources.
func (s *Service) claimListing(key string) (func(), error) {
	s.listings.Lock()
	if s.listingClaims == nil {
		s.listingClaims = map[string]bool{}
	}
	if s.listingClaims[key] {
		s.listings.Unlock()
		return nil, storage.ErrBusy
	}
	s.listingClaims[key] = true
	s.listings.Unlock()
	return func() { s.listings.Lock(); delete(s.listingClaims, key); s.listings.Unlock() }, nil
}

// VerifyInventoryDirectory re-enumerates the complete directory, rather than
// assuming that a DAV collection ETag/mtime describes its children. Private DAV
// continuations survive worker quanta and restart. ErrBusy means proof is still
// pending: it must never be interpreted as an empty or authoritative directory.
func (s *Service) VerifyInventoryDirectory(ctx context.Context, job, directory, listing string) error {
	release, err := s.claimListing("verify:" + job + ":" + directory)
	if err != nil {
		return err
	}
	defer release()
	b, _, ok := s.find(directory)
	if !ok || b.Removed {
		return storage.ErrRemoteOffline
	}
	if err = s.CheckScan(ctx, job, directory); err != nil {
		return err
	}
	var generation string
	err = s.db.QueryRowContext(ctx, `SELECT generation FROM remote_listing_sessions WHERE id=? AND job_id=? AND directory=? AND source_id=? AND ready=1`, listing, job, directory, b.ID).Scan(&generation)
	if err != nil {
		return storage.ErrRemoteCursor
	}
	if err = s.check(ctx, b, generation); err != nil {
		return err
	}
	var checkJob, expected string
	var verified int64
	err = s.db.QueryRowContext(ctx, `SELECT check_job,listing_id,verified_ms FROM remote_inventory_checks WHERE job_id=? AND directory=?`, job, directory).Scan(&checkJob, &expected, &verified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UnixMilli()
	if expected == listing && verified > 0 && verified <= now && now-verified <= 5000 {
		return nil
	}
	if checkJob == "" || expected != listing || verified != 0 {
		checkJob = job + ":verify:" + identity.Token()
		_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO remote_inventory_checks(job_id,directory,listing_id,check_job,verified_ms) VALUES(?,?,?,?,0) ON CONFLICT(job_id,directory) DO UPDATE SET listing_id=excluded.listing_id,check_job=excluded.check_job,verified_ms=0`, job, directory, listing, checkJob)
		if err != nil {
			return err
		}
	}
	// ListPage's provider cache is complete before any public catalog page can be
	// emitted. Only its first bounded output page is needed to obtain that proof.
	page, err := s.ListPage(ctx, checkJob, directory, "")
	if err != nil {
		return err
	}
	if page.Pending {
		return storage.ErrBusy
	}
	if page.Generation != generation {
		return storage.ErrRemoteChanged
	}
	var changed bool
	// Compare file/directory evidence, not publication CAS or process custody.
	// A verification pass captures a later expected observation revision.
	const evidence = `name,json_remove(snapshot,'$.expectedObservationRevision','$.processEpoch','$.readProfile')`
	err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT `+evidence+` FROM remote_listing_entries WHERE listing_id=? EXCEPT SELECT `+evidence+` FROM remote_listing_entries WHERE listing_id=?) OR EXISTS(SELECT `+evidence+` FROM remote_listing_entries WHERE listing_id=? EXCEPT SELECT `+evidence+` FROM remote_listing_entries WHERE listing_id=?)`, listing, page.ID, page.ID, listing).Scan(&changed)
	if err != nil {
		return err
	}
	if changed {
		return storage.ErrInventoryChanged
	}
	if err = s.check(ctx, b, generation); err != nil {
		return err
	}
	_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE remote_inventory_checks SET verified_ms=? WHERE job_id=? AND directory=? AND listing_id=? AND check_job=?`, time.Now().UnixMilli(), job, directory, listing, checkJob)
	return err
}

// A configuration can point the same virtual path at an unrelated remote tree.
// Root admission must therefore change even when that path and collection name
// do not. The existing owner replacement flow creates a new source incarnation;
// old observations cannot become deletion proof for the replacement namespace.
func inventoryRootSnapshot(v storage.Snapshot) storage.Snapshot {
	generation, _, _ := strings.Cut(v.Revision, ":")
	v.ObjectIdentity += ":generation:" + generation
	return v
}

// A strong ETag is meaningful only inside the captured provider namespace.
// Reusing an ID/ETag after a root or credentials change must not alias byte facts.
func remoteVersionScope(b binding, generation string) string {
	return "remote:" + b.ID + ":" + generation
}
