package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strings"
	"time"
)

// InventoryFenceTx is the last publication check for every page, status and
// analysis result. Cancellation and configuration mutations use the same DB.
func InventoryFenceTx(ctx context.Context, tx *sql.Tx, job string) (LibrarySource, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT r.source_id FROM inventory_runs r JOIN jobs j ON j.id=r.job_id JOIN library_sources s ON s.id=r.source_id WHERE r.job_id=? AND j.status='running' AND s.enabled=1 AND r.source_generation=s.generation AND r.root_incarnation=s.incarnation AND r.root_identity=s.root_identity`, job).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return LibrarySource{}, context.Canceled
	}
	if err != nil {
		return LibrarySource{}, err
	}
	source, err := scanLibrarySource(tx.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM library_sources WHERE id=?`, id))
	if err == nil && source.Kind == "remote" {
		err = remoteInventoryFenceTx(ctx, tx, job)
	}
	return source, err
}
func inventoryEvent(ctx context.Context, tx *sql.Tx, source, job, object, asset, revision, kind string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_changes(source_id,job_id,object_id,asset_id,revision,kind,created_at) VALUES(?,?,?,?,?,?,?)`, source, job, object, asset, revision, kind, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func InventoryMedia(path, kind string) (bool, string) {
	if assets.DiscImage(path) {
		// A disc image is one title; its main title is probed during analysis.
		// Audio, book and comic libraries keep rejecting images: they have
		// no video title to probe.
		if kind == "music" || kind == "audiobook" || kind == "book" || kind == "comic" {
			return false, ""
		}
		return true, ""
	}
	return assets.SupportedForKind(path, kind), ""
}

// ObservationRevision is the revision of one observation of a file: what the
// catalogue compares to tell whether the file it analysed is still the file
// on disk. The scan and the analysis that follows it both use it, so they
// agree on what "the same file" is.
func ObservationRevision(source LibrarySource, v storage.Snapshot) (string, string, error) {
	return observationRevision(source, v)
}

func observationRevision(source LibrarySource, v storage.Snapshot) (string, string, error) {
	// Kept across restarts, so the device number this boot gave the
	// filesystem is left out (storage.StableObjectIdentity).
	object := storage.StableObjectIdentity(v.ObjectIdentity, source.RootIdentity)
	if object == "" {
		object = "path:" + v.Path
	}
	e := mediasource.InventoryEvidence{Kind: source.Kind, Scope: source.ID + ":" + source.Incarnation, Object: object, Size: v.Size, ModifiedNS: v.ModifiedNS, ChangeToken: v.ChangeToken}
	revision, err := e.Revision()
	raw, _ := json.Marshal(e)
	return revision, string(raw), err
}
func (s *Service) CommitInventoryPage(ctx context.Context, job, relative, cursor string, page storage.InventoryPage) error {
	gated, err := dbwork.Begin(dbwork.WithPlan(ctx, inventoryPagesPlan), s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	keep, err := s.commitInventoryPageTx(ctx, tx, job, source, InventoryPageWrite{Relative: relative, Cursor: cursor, Page: page})
	if err != nil || !keep {
		return err
	}
	if err = compactcatalog.FlushReadingPathSemanticTx(ctx, tx); err != nil {
		return err
	}
	return gated.Commit()
}

// InventoryPageWrite is one folder's listed page and the cursor it continues.
type InventoryPageWrite struct {
	Relative, Cursor string
	Page             storage.InventoryPage
	// DiscRoot is observed outside the writer and committed with the page's
	// cursor, so a restart cannot skip the title that this page discovered.
	DiscRoot *storage.Snapshot
}

// inventoryPagesPlan keeps the named exception for an indivisible publication.
// The local ingestion producer requests <=2 raw names across <=2 folders before
// listing (one folder on platforms without the shared budget). This includes
// managed subtitle rows and bounds empty-folder/ignore diagnostic overhead.
// It does not prove all legal entries fit the default plan: one existing object
// can invalidate retained relationships/Reading facts, a folder can carry 257
// ignore diagnostics, and a single existing object may still exceed the default class budget.
// Remote ingestion also requests real pages under the same name allowance.
// Never slice a listed page or reject a legal write to fit these estimates.
var inventoryPagesPlan = dbwork.WritePlan{Name: "inventory-pages", Rows: 8192, Pages: 1024, VMOps: 2_000_000, Why: "a folder page's entries publish atomically with their reading-path effects; bounding a page's fan-out is DB-001's publication redesign (writer-discipline item H)"}

// CommitInventoryPages publishes the pages of several folders in one
// transaction, each exactly as CommitInventoryPage would: a page that is a
// replay of one already published leaves nothing behind. Any page's error
// publishes none of them, and the caller publishes them one at a time. A
// library of one folder per title commits once per batch of folders instead
// of once per title.
func (s *Service) CommitInventoryPages(ctx context.Context, job string, pages []InventoryPageWrite) error {
	gated, err := dbwork.Begin(dbwork.WithPlan(ctx, inventoryPagesPlan), s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	for _, p := range pages {
		if _, err = tx.ExecContext(ctx, `SAVEPOINT inventory_page`); err != nil {
			return err
		}
		keep, err := s.commitInventoryPageTx(ctx, tx, job, source, p)
		if err != nil {
			return err
		}
		if !keep {
			if _, err = tx.ExecContext(ctx, `ROLLBACK TO inventory_page`); err != nil {
				return err
			}
		} else if p.DiscRoot != nil {
			if err = s.commitInventoryDiscRootTx(ctx, tx, job, source, p.Relative, *p.DiscRoot); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `RELEASE inventory_page`); err != nil {
			return err
		}
	}
	if err = compactcatalog.FlushReadingPathSemanticTx(ctx, tx); err != nil {
		return err
	}
	return gated.Commit()
}

// commitInventoryPageTx writes one folder's page. keep is false when the page
// was already published (a replay) and its writes are to be discarded.
func (s *Service) commitInventoryPageTx(ctx context.Context, tx *sql.Tx, job string, source LibrarySource, write InventoryPageWrite) (keep bool, err error) {
	relative, cursor, page := write.Relative, write.Cursor, write.Page
	if page.RootIdentity != source.RootIdentity || page.DirectoryIdentity == "" || page.DirectoryRevision == "" || len(page.Entries) > 128 || len(page.Issues) > 512 || page.Complete && page.NextCursor != "" || !page.Complete && (page.NextCursor == "" || page.NextCursor == cursor && !page.Pending) {
		return false, storage.ErrInventoryChanged
	}
	if page.Pending && (page.Remote == nil || len(page.Entries) != 0 || page.Complete) {
		return false, storage.ErrInventoryChanged
	}
	if page.Remote != nil {
		if s.storage == nil || !s.storage.IsRemote(source.ResolvedPath) || page.Remote.Directory != filepath.Join(source.ResolvedPath, relative) {
			return false, storage.ErrRemoteConfig
		}
		if err = s.storage.Remote.ValidateScan(ctx, tx, job, source.ResolvedPath); err != nil {
			return false, err
		}
		if err = s.storage.Remote.CommitPage(ctx, tx, *page.Remote); err != nil {
			return false, err
		}
	}
	{
		if err = subtitles.CommitManagedPage(ctx, tx, source.ID, source.Incarnation, job, filepath.Join(source.ResolvedPath, relative), page.Entries); err != nil {
			return false, err
		}
	}
	for _, issue := range page.Issues {
		if err = InventoryIssueTx(ctx, tx, job, issue); err != nil {
			return false, err
		}
	}
	if page.Unstable {
		if err = InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: relative, Reason: "directory_changed", Protect: true, Warning: true}); err != nil {
			return false, err
		}
	}
	var oldCursor, state, oldRevision string
	if err = tx.QueryRowContext(ctx, `SELECT cursor,state,revision FROM inventory_directories WHERE job_id=? AND relative_path=?`, job, relative).Scan(&oldCursor, &state, &oldRevision); err != nil {
		return false, err
	}
	if state == "done" || oldCursor != cursor {
		if oldCursor == page.NextCursor && oldRevision == page.DirectoryRevision {
			return false, nil
		}
		return false, ErrStaleContinuation
	}
	if oldRevision != "" && oldRevision != page.DirectoryRevision {
		return false, storage.ErrInventoryChanged
	}
	var libraryKind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&libraryKind); err != nil {
		return false, err
	}
	readingClosure := (libraryKind == "book" || libraryKind == "comic") && source.Kind == "local" && source.IdentityConfirmed
	var policy ScanPolicy
	if policy, err = ScanPolicyTx(ctx, tx, source.LibraryID); err != nil {
		return false, err
	}
	var alias bool
	// identity<>'' lets the visited-identity index answer; without it the
	// check read every directory the run had recorded, once per directory.
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_visited WHERE job_id=? AND identity=? AND identity<>'' AND relative_path<>?)`, job, page.DirectoryIdentity, relative).Scan(&alias); err != nil {
		return false, err
	}
	if alias {
		if err = InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: relative, Reason: "directory_alias", Protect: true}); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE inventory_directories SET state='blocked',cursor='' WHERE job_id=? AND relative_path=?`, job, relative)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	if readingClosure {
		if err = readingInventoryDirectoryTx(ctx, tx, job, relative); err != nil {
			return false, err
		}
	}
	// Register the current directory before its children so a link back to the
	// root is recognized as an already visited identity rather than a conflict.
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_directories SET identity=?,revision=? WHERE job_id=? AND relative_path=?`, page.DirectoryIdentity, page.DirectoryRevision, job, relative); err != nil {
		return false, err
	}
	discovered := 0
	// A name the folder held last time can drop out of its cached children
	// only through a changed revision or a skipped entry. Any page that skips
	// one marks the folder for the sidecar phase's cache prune.
	pruneParent := false
	for _, issue := range page.Issues {
		pruneParent = pruneParent || !issue.Protect
	}
	// Both old and new logical scopes are maintained by the normalized feed.
	var movedOut map[string]bool
	for _, v := range page.Entries {
		child := filepath.Join(relative, v.Name)
		expected := filepath.Join(source.ResolvedPath, child)
		if v.Path != expected || !filepath.IsLocal(child) || filepath.Base(v.Name) != v.Name || v.Size < 0 {
			return false, ErrAdminQuery
		}
		if naming.IgnorePathWithSize(filepath.ToSlash(child), v.Directory, v.Size) {
			if err = InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: child, Reason: "ignored_name"}); err != nil {
				return false, err
			}
			pruneParent = true
			continue
		}
		if v.Directory {
			if discs := naming.DiscLayout(filepath.ToSlash(child)); len(discs) > 0 {
				pruneParent = true
				// A title-bearing disc root belongs to the folder that holds
				// BDMV or VIDEO_TS. The scan loop publishes that folder as one
				// object with this page, if DiscFolderTitle admits it; otherwise
				// the layout stays explicitly unsupported.
				if root := discs[0].Value.ParentPath; root != "" && root != "." && DiscFolderTitle(libraryKind, source.Kind) {
					continue
				}
				reason := "unsupported_disc_layout"
				if source.Kind == "remote" && videoLibrary(libraryKind) {
					reason = "unsupported_remote_disc"
				}
				if _, e := s.observeInventoryTx(ctx, tx, job, source, child, v, reason, policy, movedOut); e != nil {
					return false, e
				}
				discovered++
				if err = InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: child, Reason: reason, Warning: true}); err != nil {
					return false, err
				}
				continue
			}
			if v.ObjectIdentity == "" {
				return false, storage.ErrInventoryChanged
			}
			var duplicate bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_visited WHERE job_id=? AND identity=? AND identity<>'' AND relative_path<>?)`, job, v.ObjectIdentity, child).Scan(&duplicate); err != nil {
				return false, err
			}
			if duplicate {
				if err = InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: child, Reason: "directory_alias", Protect: true}); err != nil {
					return false, err
				}
				continue
			}
			childRevision := storage.InventoryRevision(v)
			if page.Remote != nil {
				childRevision = ""
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path,parent_revision,seen_job) VALUES(?,?,?,?,?,?) ON CONFLICT(source_id,incarnation,relative_path) DO UPDATE SET parent_path=excluded.parent_path,parent_revision=excluded.parent_revision,seen_job=excluded.seen_job`, source.ID, source.Incarnation, child, relative, page.DirectoryRevision, job); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_directories(job_id,relative_path,identity,revision) VALUES(?,?,?,?)`, job, child, v.ObjectIdentity, childRevision); err != nil {
				return false, err
			}
			if readingClosure {
				if err = readingInventoryDirectoryTx(ctx, tx, job, child); err != nil {
					return false, err
				}
			}
			continue
		}
		if err = observeSidecarTx(ctx, tx, job, source, child, v); err != nil {
			return false, err
		}
		supported, reason := InventoryMedia(v.Path, libraryKind)
		if !supported {
			continue
		}
		// Root64: a remote disc image is an ordinary title for discovery. A scan
		// reads nothing from it and the naming-only analysis publishes it from its
		// name, so it is no longer tagged as an unsupported remote disc. Folders
		// that carry a disc layout keep their explicit reason above.
		// CBR/CB7 have only local admitted archive representations. A remote source
		// never admits either, so a capable scan cannot create an inventory-ready
		// remote archive or starve a local comic primary. The opener also
		// enforces local archive custody.
		extension := strings.ToLower(filepath.Ext(v.Path))
		if libraryKind == "comic" && source.Kind != "local" && (extension == ".cb7" || extension == ".cbr") {
			continue
		}
		if reason != "" {
			if err = InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: child, Reason: reason, Warning: reason == "unsupported_remote_disc"}); err != nil {
				return false, err
			}
		}
		added, e := s.observeInventoryTx(ctx, tx, job, source, child, v, reason, policy, movedOut)
		if e != nil {
			return false, e
		}
		if added {
			discovered++
		}
	}
	state = "pending"
	if page.Complete {
		state = "done"
		var cachedRevision string
		switch err = tx.QueryRowContext(ctx, `SELECT revision FROM inventory_directory_cache WHERE source_id=? AND incarnation=? AND relative_path=?`, source.ID, source.Incarnation, relative).Scan(&cachedRevision); {
		case err == nil && cachedRevision != "":
			pruneParent = pruneParent || cachedRevision != page.DirectoryRevision
		case err == nil || errors.Is(err, sql.ErrNoRows):
			// A folder never listed before (its row, if any, was written by
			// its parent's page) can hold stale children only if its own row
			// went while they stayed, as when its folder was disabled and
			// enabled again: cached children this run did not see.
			var orphans bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directory_cache INDEXED BY inventory_directory_children WHERE source_id=? AND incarnation=? AND parent_path=? AND relative_path<>'.' AND seen_job<>?)`, source.ID, source.Incarnation, relative, job).Scan(&orphans); err != nil {
				return false, err
			}
			pruneParent = pruneParent || orphans
		default:
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path,identity,revision,seen_job) VALUES(?,?,?,?,?,?,?) ON CONFLICT(source_id,incarnation,relative_path) DO UPDATE SET identity=excluded.identity,revision=excluded.revision,seen_job=excluded.seen_job`, source.ID, source.Incarnation, relative, filepath.Dir(relative), page.DirectoryIdentity, page.DirectoryRevision, job); err != nil {
			return false, err
		}
	}
	if pruneParent {
		if err = MarkDirectoryPruneTx(ctx, tx, source.ID, source.Incarnation, relative, job); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_directories SET identity=?,revision=?,cursor=?,state=?,pages=pages+1 WHERE job_id=? AND relative_path=? AND cursor=?`, page.DirectoryIdentity, page.DirectoryRevision, page.NextCursor, state, job, relative, cursor); err != nil {
		return false, err
	}
	if readingClosure {
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET listing_eof=?,identity=?,revision=?,phase=CASE WHEN ? THEN 'verify' ELSE 'listing' END WHERE job_id=? AND relative_path=? AND phase='listing'`, page.Complete, page.DirectoryIdentity, page.DirectoryRevision, page.Complete, job, relative); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET discovered=discovered+? WHERE job_id=?`, discovered, job); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET processed=processed+? WHERE id=?`, discovered, job); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, time.Now().UTC().Format(time.RFC3339Nano), source.ID); err != nil {
		return false, err
	}
	return true, nil
}
func (s *Service) observeInventoryTx(ctx context.Context, tx *sql.Tx, job string, source LibrarySource, relative string, v storage.Snapshot, reason string, policy ScanPolicy, movedOut map[string]bool) (bool, error) {
	var removed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_scan_issues WHERE job_id=? AND relative_path=? AND reason='reported_file_removed')`, job, relative).Scan(&removed); err != nil {
		return false, err
	}
	if removed {
		// A later run owns reappearance; this run may be replaying a page read
		// before the exact reported removal was committed.
		return false, nil
	}
	if source.Kind == "remote" {
		if s.storage == nil || !s.storage.IsRemote(v.Path) {
			return false, storage.ErrRemoteConfig
		}
		if err := s.storage.Remote.RecordReference(ctx, tx, v.Path, v.Revision); err != nil {
			return false, err
		}
	}
	// What is kept across restarts names a file by its inode, not by the
	// device number this boot gave its filesystem.
	v.ObjectIdentity = storage.StableObjectIdentity(v.ObjectIdentity, source.RootIdentity)
	revision, evidence, err := observationRevision(source, v)
	if err != nil {
		return false, err
	}
	var object, asset, previousRevision, previousSeen, previousIdentity, incarnation, previousState string
	knownObject := false
	var previousSize, previousModified int64
	relocated, relocatedFrom := false, ""
	err = tx.QueryRowContext(ctx, `SELECT id,asset_id,revision,seen_job,object_identity,root_incarnation,state,size,modified_ns FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0`, source.ID, relative).Scan(&object, &asset, &previousRevision, &previousSeen, &previousIdentity, &incarnation, &previousState, &previousSize, &previousModified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	knownObject = object != ""
	// A file at the same place with the same size and modified time is the same
	// file, whatever its filesystem now calls it: inode and device numbers are
	// renumbered by remounts, by filesystems that do not keep them, and when a
	// library is copied to a new disk and its folder accepted as the same one.
	// Only a file whose content evidence changed has been replaced.
	sameContent := object != "" && previousSize == v.Size && previousModified == v.ModifiedNS
	renamedByFilesystem := sameContent && (incarnation != source.Incarnation || previousIdentity != v.ObjectIdentity)
	replaced := object != "" && !sameContent && (incarnation != source.Incarnation || previousIdentity != "" && v.ObjectIdentity != "" && previousIdentity != v.ObjectIdentity)
	if replaced {
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET retired=1,state='unavailable' WHERE id=?`, object); err != nil {
			return false, err
		}
		if err = inventoryEvent(ctx, tx, source.ID, job, object, asset, previousRevision, "replaced"); err != nil {
			return false, err
		}
		object, asset, previousRevision, previousSeen, previousState = "", "", "", "", ""
		knownObject = false
	}
	if object == "" {
		// Exact storage identity plus unchanged size/mtime, never filename similarity.
		// LIMIT 2 deliberately refuses ambiguous identity candidates.
		if v.ObjectIdentity != "" {
			// Stable inode identities belong to one admitted source, never to a
			// similarly named file on another disk or in another library.
			rows, e := tx.QueryContext(ctx, `SELECT DISTINCT o.asset_id FROM inventory_objects o WHERE o.source_id=?1 AND o.root_incarnation=?2 AND o.object_identity=?3 AND o.size=?4 AND o.modified_ns=?5 AND o.retired=0 LIMIT 2`, source.ID, source.Incarnation, v.ObjectIdentity, v.Size, v.ModifiedNS)
			if e != nil {
				return false, e
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return false, e
				}
				ids = append(ids, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return false, e
			}
			if len(ids) == 1 {
				asset = ids[0]
			}
		}
		if asset == "" {
			// Adopt legacy assets only once and only with the retained observation. A
			// migrated pathname is not evidence for a future replacement or rename.
			_ = tx.QueryRowContext(ctx, `SELECT a.token FROM catalog_assets a WHERE a.path=? AND a.size=? AND a.modified_ns=? AND NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.asset_id=a.token)`, v.Path, v.Size, v.ModifiedNS).Scan(&asset)
		}
		// A single known location of this exact physical object can move without
		// replacing its location ID. Multiple hardlink aliases stay separate.
		if asset != "" && v.ObjectIdentity != "" {
			var oldID, oldRev, oldSeen, oldState string
			// Both reads start from the asset: a source's own index would read
			// every file of the source.
			e := tx.QueryRowContext(ctx, `SELECT id,revision,seen_job,state,relative_path FROM inventory_objects INDEXED BY inventory_objects_asset WHERE asset_id=?3 AND source_id=?1 AND root_incarnation=?2 AND object_identity=?4 AND retired=0 AND seen_job!=?5 AND (SELECT count(*) FROM inventory_objects x INDEXED BY inventory_objects_asset WHERE x.asset_id=?3 AND x.source_id=?1 AND x.retired=0)=1 LIMIT 1`, source.ID, source.Incarnation, asset, v.ObjectIdentity, job).Scan(&oldID, &oldRev, &oldSeen, &oldState, &relocatedFrom)
			if e == nil {
				object, previousRevision, previousSeen, previousState = oldID, oldRev, oldSeen, oldState
				relocated = true
				knownObject = true
			} else if !errors.Is(e, sql.ErrNoRows) {
				return false, e
			}
		}
		if object == "" {
			object = identity.Token()
		}
	}
	newAsset := asset == ""
	if newAsset {
		asset = identity.Token()
	}
	// The baseline asset path is a locator projection. Preserve the old asset ID
	// when that pathname is replaced, but never silently point it at new bytes.
	var collision string
	var collisionID int64
	err = tx.QueryRowContext(ctx, `SELECT id,token FROM catalog_assets WHERE path=? AND token!=?`, v.Path, asset).Scan(&collisionID, &collision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if collision != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET state='unavailable' WHERE asset_id=? AND source_id=? AND relative_path=?`, collision, source.ID, relative); err != nil {
			return false, err
		}
		var available bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT available FROM inventory_asset_availability WHERE asset_id=?),0)`, collision).Scan(&available); err != nil {
			return false, err
		}
		if err = compactcatalog.SetAssetTx(ctx, tx, collisionID, map[string]any{"path": "unavailable:" + collision, "available": available}); err != nil {
			return false, err
		}
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(v.Path)), ".")
	if newAsset {
		_, err = compactcatalog.CreateAssetTx(ctx, tx, asset, compactcatalog.Asset{Path: v.Path, Size: v.Size, ModifiedNS: v.ModifiedNS, Container: ext}, reason == "")
	} else {
		var size, modified, assetID int64
		if err = tx.QueryRowContext(ctx, `SELECT id,size,modified_ns FROM catalog_assets WHERE token=?`, asset).Scan(&assetID, &size, &modified); err != nil {
			return false, err
		}
		changed := size != v.Size || modified != v.ModifiedNS || previousRevision != "" && previousRevision != revision && !renamedByFilesystem
		if changed {
			if err = invalidateInventoryFacts(ctx, tx, asset); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision='',analysis_policy=0,analysis_operations_json='[]',analysis_state='pending' WHERE asset_id=?`, asset); err != nil {
				return false, err
			}
		}
		// Do not replace verified format facts with a filename extension on an
		// unchanged scan. Avoid no-op asset writes/revisions for every observation.
		values := map[string]any{"path": v.Path, "size": v.Size, "modified_ns": v.ModifiedNS, "available": reason == ""}
		if changed {
			values["container"] = ext
		}
		err = compactcatalog.SetAssetTx(ctx, tx, assetID, values)
	}
	if err != nil {
		return false, err
	}
	if knownObject {
		// Do not attempt INSERT for a known observation: SQLite runs BEFORE
		// INSERT invalidation even when an UPSERT resolves to an unchanged UPDATE.
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET root_incarnation=?,directory_path=?,relative_path=?,revision=?,evidence_json=?,size=?,modified_ns=?,object_identity=?,seen_job=?,state='available',missing_since='',missing_observations=0,last_missing_job='',unsupported_reason=? WHERE id=? AND source_id=? AND asset_id=? AND retired=0`, source.Incarnation, filepath.Dir(relative), relative, revision, evidence, v.Size, v.ModifiedNS, v.ObjectIdentity, job, reason, object, source.ID, asset)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				err = ErrStaleContinuation
			}
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,unsupported_reason,directory_path) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, object, source.ID, asset, source.Incarnation, relative, v.ObjectIdentity, revision, evidence, v.Size, v.ModifiedNS, job, reason, filepath.Dir(relative))
	}
	if err != nil {
		return false, err
	}
	if renamedByFilesystem && previousRevision != revision {
		// The analysis already done describes this same content.
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision=? WHERE id=? AND analysis_revision=?`, revision, object, previousRevision); err != nil {
			return false, err
		}
	}
	var associated int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_entities i ON i.id=l.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE a.token=? AND cl.library_id=?)`, asset, source.LibraryID).Scan(&associated); err != nil {
		return false, err
	}
	if associated == 0 {
		if err = s.associateInventoryTx(ctx, tx, source, asset, v.Path); err != nil {
			return false, err
		}
	} else if relocated {
		// A renamed or moved file is read again under its new name.
		if err = s.reinterpretInventoryTx(ctx, tx, source, asset, v.Path); err != nil {
			return false, err
		}
	}
	if !newAsset && previousState != "available" {
		// A file that was missing is back: its titles are whole again.
		assetID, e := compactcatalog.AssetByTokenTx(ctx, tx, asset)
		if e != nil {
			return false, e
		}
		if assetID != 0 {
			if e = compactcatalog.SettleAssetMissingTx(ctx, tx, assetID); e != nil {
				return false, e
			}
		}
	}
	kind := ""
	if previousRevision == "" {
		kind = "created"
	} else if previousRevision != revision {
		kind = "revision_changed"
	} else if previousState != "available" {
		kind = "reappeared"
	} else if relocated {
		kind = "relocated"
	}
	if kind != "" {
		if err = inventoryEvent(ctx, tx, source.ID, job, object, asset, revision, kind); err != nil {
			return false, err
		}
	}
	if reason != "" && previousSeen != job {
		if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET warnings=warnings+1 WHERE job_id=?`, job); err != nil {
			return false, err
		}
	}
	if ext == "strm" {
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM source_strm_analysis WHERE library_id=?),0)`, source.LibraryID).Scan(&enabled); err != nil {
			return false, err
		}
		if !enabled || !policy.Allows("probe") {
			if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_state='deferred' WHERE id=? AND analysis_state<>'deferred'`, object); err != nil {
				return false, err
			}
		}
	}
	// A revision/policy-specific queue is separate from inventory visibility.
	// Static reading publications have no timed media probe. Their package and
	// page resources are validated by the interactive, actor-authorized reader.
	if policy.Tier != "file_list_only" && reason == "" && ext != "strm" && ext != "epub" && ext != "pdf" && ext != "cbz" && ext != "cb7" && ext != "cbr" {
		_, err = tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision) SELECT ?,id,revision,? FROM inventory_objects WHERE id=? AND (analysis_revision!=revision OR analysis_policy!=?) ON CONFLICT(job_id,object_id) DO UPDATE SET revision=excluded.revision,policy_revision=excluded.policy_revision,state=CASE WHEN inventory_analysis_queue.revision=excluded.revision AND inventory_analysis_queue.policy_revision=excluded.policy_revision THEN inventory_analysis_queue.state ELSE 'pending' END`, job, policy.Revision, object, policy.Revision)
	}
	if err == nil {
		err = compactcatalog.ReadingNormalizeObjectTx(ctx, tx, object)
	}
	return previousSeen != job, err
}
func invalidateInventoryFacts(ctx context.Context, tx *sql.Tx, asset string) error {
	// Keep the bounded OLD technical set until its accepted replacement. Its
	// stat fingerprint cannot describe the new physical file; informational
	// readers resolve it only against the accepted scalar frame. Eager parent
	// deletes would cascade through the child sets before the FI1 pre-fence.
	id, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
	if err != nil || id == 0 {
		return err
	}
	if err := compactcatalog.SetAssetTx(ctx, tx, id, map[string]any{"video_codec": "", "audio_codec": "", "width": 0, "height": 0, "duration": 0}); err != nil {
		return err
	}
	return compactcatalog.InvalidateInfoTechnicalTx(ctx, tx, id)
}
func (s *Service) associateInventoryTx(ctx context.Context, tx *sql.Tx, source LibrarySource, asset, path string) error {
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind); err != nil {
		return err
	}
	// Sidecar extras are published before any library-kind association so an
	// Extras/ folder never becomes a movie, episode, track or book part.
	if extra, ok := ClassifyExtraFor(kind, source.ResolvedPath, path); ok {
		return s.publishExtraTx(ctx, tx, source, asset, path, extra)
	}
	switch kind {
	case "book", "comic":
		_, err := attachReadingTx(ctx, tx, source.LibraryID, kind, asset, path)
		return err
	case "music", "audiobook":
		return s.commitAudio(ctx, tx, source.LibraryID, kind, source.ResolvedPath, asset, path, assets.Facts{InventoryOnly: true})
	case "tv", "anime":
		relative, err := filepath.Rel(source.ResolvedPath, path)
		if err != nil {
			return err
		}
		return s.commitEpisodes(tx, source.LibraryID, asset, filepath.Base(path), ParseEpisode(relative, kind), false)
	default:
		assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
		if err != nil {
			return err
		}
		_, err = commitMovieItem(ctx, tx, source.LibraryID, assetID, path)
		return err
	}
}

// reinterpretInventoryTx reads a renamed or moved file's new name and moves
// the file to the title that name says, unless an owner placed it by hand.
// Tags, not names, say what an audio file is, so those stay as they are.
func (s *Service) reinterpretInventoryTx(ctx context.Context, tx *sql.Tx, source LibrarySource, asset, path string) error {
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind); err != nil {
		return err
	}
	if _, extra := ClassifyExtraFor(kind, source.ResolvedPath, path); extra {
		return nil
	}
	switch kind {
	case "tv", "anime":
		relative, err := filepath.Rel(source.ResolvedPath, path)
		if err != nil {
			return err
		}
		return s.commitEpisodes(tx, source.LibraryID, asset, filepath.Base(path), ParseEpisode(relative, kind), false)
	case "movie":
		var manual bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_version_links WHERE library_id=? AND asset_id=?)`, source.LibraryID, asset).Scan(&manual); err != nil || manual {
			return err
		}
		assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
		if err != nil || assetID == 0 {
			return err
		}
		f, e := movieFileReadingTx(ctx, tx, source.LibraryID, assetID, path)
		if e != nil {
			return e
		}
		if e = reinterpretMovieTx(ctx, tx, source.LibraryID, assetID, asset, path, f.Title, f.Year); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT l.entity_id FROM catalog_asset_links l JOIN catalog_entities i ON i.id=l.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE l.asset_id=? AND cl.library_id=?`, assetID, source.LibraryID)
		ids, e := scanInt64s(rows, e)
		if e != nil {
			return e
		}
		handle, e := compactcatalog.LibraryTx(ctx, tx, source.LibraryID)
		if e != nil {
			return e
		}
		for _, id := range ids {
			var locked bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_relationship_decisions WHERE kind='item' AND entity_id=? AND relationship='identity' AND locked=1)`, id).Scan(&locked); e != nil {
				return e
			}
			if !locked {
				for _, provider := range f.Provider {
					target, err := compactcatalog.FindByProviderTx(ctx, tx, handle, compactcatalog.Movie, provider)
					if err != nil {
						return err
					}
					if target != 0 && target != id {
						if err = MergeTitlesTx(ctx, tx, id, target); err != nil {
							return err
						}
						id = target
						break
					}
				}
			}
			if e = compactcatalog.LinkAssetTx(ctx, tx, id, assetID, compactcatalog.Link{Part: f.Part, Edition: f.Edition}); e != nil {
				return e
			}
		}
		return nil
	}
	return nil
}

// CommitInventoryDiscRoot publishes one BDMV or VIDEO_TS folder as a single
// title object. The caller stats the admitted directory outside this
// transaction; the object's revision is that exact observation, so the
// analysis fence compares the same directory snapshot. The folder's own
// children are not inventoried as loose files.
func (s *Service) CommitInventoryDiscRoot(ctx context.Context, job, relative string, v storage.Snapshot) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	if err = s.commitInventoryDiscRootTx(ctx, tx, job, source, relative, v); err != nil {
		return err
	}
	return gated.Commit()
}

func (s *Service) commitInventoryDiscRootTx(ctx context.Context, tx *sql.Tx, job string, source LibrarySource, relative string, v storage.Snapshot) error {
	if relative == "" || relative == "." || !v.Directory {
		return ErrAdminQuery
	}
	policy, err := ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	// The folder's own size and mtime cannot witness the bytes of the title it
	// holds. The object's evidence is the last committed disc structure, and
	// every scan re-probes the folder's actual title bytes below it.
	var fingerprint string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(f.fingerprint,'') FROM inventory_objects o JOIN inventory_disc_facts f ON f.object_id=o.id WHERE o.source_id=? AND o.relative_path=? AND o.retired=0`, source.ID, relative).Scan(&fingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	v.ChangeToken = DiscStructureWitness(fingerprint)
	if _, err = s.observeInventoryTx(ctx, tx, job, source, relative, v, "", policy, map[string]bool{}); err != nil {
		return err
	}
	if policy.Tier == "file_list_only" {
		return nil
	}
	// Re-probe every time: a clip, playlist or VIDEO_TS member can change
	// without changing the folder's revision.
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision='',analysis_policy=0,analysis_operations_json='[]',analysis_state='pending' WHERE source_id=? AND relative_path=? AND retired=0`, source.ID, relative); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision) SELECT ?,id,revision,? FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0 ON CONFLICT(job_id,object_id) DO UPDATE SET revision=excluded.revision,policy_revision=excluded.policy_revision,state='pending'`, job, policy.Revision, source.ID, relative); err != nil {
		return err
	}
	return nil
}

func videoLibrary(kind string) bool {
	return kind != "music" && kind != "audiobook" && kind != "book" && kind != "comic"
}

// DiscFolderTitle reports whether a folder holding BDMV or VIDEO_TS becomes one
// disc title: a local video folder, on a host that can mount an admitted
// directory descriptor. Everywhere else the page records the layout as
// unsupported, and the scan must not observe the folder as a second title.
func DiscFolderTitle(libraryKind, sourceKind string) bool {
	return videoLibrary(libraryKind) && sourceKind == "local" && storage.DirectoryDiscScanSupported()
}

// DiscStructureWitness is the object-evidence token for a folder disc: the
// last committed structure fingerprint. It changes the object revision when
// playlists or member layout change, without pretending the folder's own
// size/mtime is the title's content revision.
func DiscStructureWitness(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("disc_structure_witness_v1\x00" + fingerprint))
	return "disc-structure:" + hex.EncodeToString(sum[:])
}

// CommitInventoryAnalysis never stats/reads storage while holding a DB writer.
// The caller revalidates the observed revision after all bounded reads; this
// transaction additionally checks job cancellation, source and current policy.
func (s *Service) commitInventoryAnalysisApplied(ctx context.Context, job, object, revision string, policyRevision int64, f assets.Facts) error {
	ctx, audioAdmissions := withAudioMergeAdmissions(ctx)
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	policy, err := ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	if policy.Revision != policyRevision || !policy.Allows("probe") {
		return context.Canceled
	}
	var asset, relative string
	var size, modified int64
	err = tx.QueryRowContext(ctx, `SELECT asset_id,relative_path,size,modified_ns FROM inventory_objects WHERE id=? AND source_id=? AND revision=? AND retired=0 AND state='available'`, object, source.ID, revision).Scan(&asset, &relative, &size, &modified)
	if err != nil {
		return err
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind); err != nil {
		return err
	}
	if f.InventoryOnly {
		return errors.New("technical metadata was not observed")
	}
	if source.Kind == "remote" {
		if s.storage == nil {
			return storage.ErrRemoteConfig
		}
		if err = s.storage.Remote.RecordReference(ctx, tx, filepath.Join(source.ResolvedPath, relative), f.ObservedRevision); err != nil {
			return err
		}
	}
	if (kind == "music" || kind == "audiobook") && f.AudioCodec == "" || kind != "music" && kind != "audiobook" && f.VideoCodec == "" {
		return errors.New("media stream unsupported for library kind")
	}
	// Technical rows and scalars were applied by the accepted source41 frame
	// before entering this final naming/queue transaction. Never republish them
	// here or install a delayed dirty marker after canonical NEW.
	var sidecarObservation *sidecarRead
	if f.ArtworkObserved {
		ctx, sidecarObservation = withSidecarRead(ctx, asset)
	}
	// Bind local evidence to the same revision already authorized by this commit.
	f.ObservedRevision = revision
	if kind == "movie" || kind == "tv" || kind == "anime" {
		var manuallyLinked int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_version_links WHERE library_id=? AND asset_id=?)`, source.LibraryID, asset).Scan(&manuallyLinked); err != nil {
			return err
		}
		if err = s.publishVideoNamingTx(ctx, tx, job, source, asset, relative, revision, kind, manuallyLinked, f); err != nil {
			return err
		}
	}
	if kind == "music" || kind == "audiobook" {
		if err = dbwork.UsePlan(tx, audioFilePublicationPlan); err != nil {
			return err
		}
		if err = persistAudioFileMetadata(ctx, tx, asset, revision, size, modified, f); err != nil {
			return err
		}
		if err = s.commitInventoryAudioTx(ctx, tx, source, asset, kind, filepath.Join(source.ResolvedPath, relative), f); err != nil {
			return err
		}
	}
	if policy.Allows("local_metadata") && (kind == "movie" || kind == "tv" || kind == "anime") {
		if err = localmetadata.PersistVideo(ctx, tx, source.LibraryID, asset, f); err != nil {
			return err
		}
	}
	if err = s.persistScanArtworkTx(ctx, tx, source, object, revision, asset, kind, policy, f, sidecarObservation); err != nil {
		return err
	}
	if f.Container == "strm" {
		if f.AnalysisSourceEvidence == "" {
			return errors.New("STRM analysis target evidence is missing")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO analysis_probe_bindings(object_id,source_revision,evidence) VALUES(?,?,?) ON CONFLICT(object_id) DO UPDATE SET source_revision=excluded.source_revision,evidence=excluded.evidence`, object, revision, f.AnalysisSourceEvidence); err != nil {
			return err
		}
	}
	queueState := "done"
	if f.SubtitleTextPending {
		queueState = "pending"
	}
	for _, op := range DeepScanOperations {
		if policy.Allows(op) {
			queueState = "pending"
		}
	}
	analysisState := "complete"
	if queueState == "pending" {
		analysisState = "pending"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision=?,analysis_policy=?,analysis_operations_json=?,analysis_state=?,analysis_error='',analysis_algorithm=?,analysis_generation=? WHERE id=?`, func() string {
		if f.SubtitleTextPending {
			return ""
		}
		return revision
	}(), policyRevision, actualScanOperations(policy, f.AnalysisOperations), analysisState, f.AnalysisAlgorithm, source.Generation, object); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_analysis_queue SET state=? WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=?`, queueState, job, object, revision, policyRevision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET analyzed=analyzed+1 WHERE job_id=?`, job); err != nil {
		return err
	}
	if err = inventoryEvent(ctx, tx, source.ID, job, object, asset, revision, "analysis_completed"); err != nil {
		return err
	}
	if err = finishAudioMergeAdmissionsTx(ctx, tx, audioAdmissions); err != nil {
		return err
	}
	return gated2.Commit()
}

// publishVideoNamingTx publishes the video title a scanned asset names: the
// stored embedded name, the library's title-source preference, the ordinary
// file-name reading, and the movie/tv routing that creates or links the title.
// Naming needs no technical facts, so the probe-bearing analysis commit and the
// naming-only remote disc image commit share this one implementation.
func (s *Service) publishVideoNamingTx(ctx context.Context, tx *sql.Tx, job string, source LibrarySource, asset, relative, revision, kind string, manuallyLinked int, f assets.Facts) error {
	if err := persistVideoNameTx(ctx, tx, asset, revision, f.VideoName); err != nil {
		return err
	}
	path := filepath.Join(source.ResolvedPath, relative)
	if manuallyLinked == 0 {
		prefs, err := LibraryPreferencesTx(ctx, tx, source.LibraryID)
		if err != nil {
			return err
		}
		embedded := f.VideoName
		if prefs.TitleSource == "names" {
			embedded = naming.Embedded{}
		}
		reading := naming.ReadWithEmbedded(relative, kind, embedded)
		if kind == "movie" {
			if err = s.reinterpretInventoryTx(ctx, tx, source, asset, path); err != nil {
				return err
			}
		} else if _, extra := ClassifyExtraFor(kind, source.ResolvedPath, path); !extra {
			if err = s.commitEpisodes(tx, source.LibraryID, asset, filepath.Base(path), EpisodeFromReading(reading), false); err != nil {
				return err
			}
		}
	}
	if f.VideoNameSkipped {
		return InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: relative, Reason: "embedded_name_fields_skipped", Warning: true})
	}
	return nil
}

// persistScanArtworkTx records the local artwork a scan observed for one asset:
// the movie poster, sidecar keys, and candidates. It is naming/metadata evidence,
// never a technical media fact.
func (s *Service) persistScanArtworkTx(ctx context.Context, tx *sql.Tx, source LibrarySource, object, revision, asset, kind string, policy ScanPolicy, f assets.Facts, sidecarObservation *sidecarRead) error {
	if policy.Allows("local_metadata") && kind == "movie" {
		if f.ArtworkKey != "" {
			items, err := scanInt64s(tx.QueryContext(ctx, `SELECT l.entity_id FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id
 JOIN catalog_entities i ON i.id=l.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE a.token=? AND cl.library_id=?`, asset, source.LibraryID))
			if err != nil {
				return err
			}
			for _, item := range items {
				if err = compactcatalog.SetFieldsTx(ctx, tx, item, compactcatalog.Automatic, map[string]any{"poster_url": "local:" + f.ArtworkKey}); err != nil {
					return err
				}
			}
		}
	}
	if policy.Allows("local_metadata") && (len(f.ArtworkExtra) > 0 || f.ArtworkObserved || f.ArtworkKey != "") && (kind == "movie" || kind == "tv" || kind == "anime") {
		if f.ArtworkExtra == nil {
			f.ArtworkExtra = map[string]string{}
		}
		if kind == "movie" && f.ArtworkKey != "" {
			f.ArtworkExtra["item/poster"] = f.ArtworkKey
		}
		if err := s.commitAssetSidecars(ctx, tx, source.LibraryID, kind, asset, f.ArtworkExtra); err != nil {
			return err
		}
	}
	if policy.Allows("local_metadata") && f.ArtworkCandidatesObserved && (kind == "movie" || kind == "tv" || kind == "anime") {
		if err := s.commitInventoryArtworkCandidatesTx(ctx, tx, source, object, revision, asset, kind, f.ArtworkCandidates); err != nil {
			return err
		}
	}
	if sidecarObservation != nil {
		return settleSidecarRead(ctx, tx, source.LibraryID, sidecarObservation)
	}
	return nil
}

// CommitInventoryNaming publishes a remote disc image as an ordinary title from
// its name and local metadata only. Root64: a scan never reads the image, so this
// records the naming work done for this revision and leaves technical analysis
// explicitly deferred; it writes no codec, duration, stream, chapter or disc
// fact and infers none. Its disc bytes are acquired only when someone plays it,
// through the ordinary playback path; any analysis of those owned playback bytes
// is a separate decision and never inherits this naming work.
func (s *Service) CommitInventoryNaming(ctx context.Context, job, object, revision string, policyRevision int64, f assets.Facts) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	if source.Kind != "remote" {
		return errors.New("naming-only publication is for a remote disc image")
	}
	policy, err := ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	if policy.Revision != policyRevision {
		return context.Canceled
	}
	// Never become an unqueued or unavailable naming publisher: the exact
	// accepted queue invocation, the current observation and live availability
	// must still hold in the transaction that publishes.
	var queued int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=? AND state='pending'`, job, object, revision, policyRevision).Scan(&queued); err != nil {
		return err
	}
	if queued != 1 {
		return context.Canceled
	}
	var asset, relative, incarnation, unsupported, objectState string
	var size, modified int64
	var available int
	var assetPath string
	var assetSize, assetModified int64
	if err = tx.QueryRowContext(ctx, `SELECT o.asset_id,o.relative_path,o.size,o.modified_ns,o.root_incarnation,o.unsupported_reason,o.state,a.available,a.path,a.size,a.modified_ns FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=? AND o.source_id=? AND o.revision=? AND o.retired=0`, object, source.ID, revision).Scan(&asset, &relative, &size, &modified, &incarnation, &unsupported, &objectState, &available, &assetPath, &assetSize, &assetModified); err != nil {
		return err
	}
	if objectState != "available" || available != 1 || unsupported != "" || incarnation != source.Incarnation {
		return errors.New("naming-only publication needs a live, observed, supported object")
	}
	// The asset must still describe the same file the object names, at the path
	// this source resolves it to, not merely be marked available.
	if assetSize != size || assetModified != modified || assetPath != filepath.Join(source.ResolvedPath, relative) {
		return errors.New("naming-only publication needs the exact asset the object names")
	}
	if f.ObservedSize != size || f.ObservedModifiedNS != modified {
		return errors.New("naming-only publication needs the exact observation it read")
	}
	if !assets.DiscImage(relative) {
		return errors.New("naming-only publication is for a disc image")
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind); err != nil {
		return err
	}
	// A disc image is one title in a video library; every other library kind is
	// refused at discovery, so reaching here with one is a caller mistake.
	if kind != "movie" && kind != "tv" && kind != "anime" {
		return errors.New("naming-only publication is for a video library")
	}
	if s.storage == nil || s.storage.Remote == nil {
		return storage.ErrRemoteConfig
	}
	if err = s.storage.Remote.RecordReference(ctx, tx, filepath.Join(source.ResolvedPath, relative), f.ObservedRevision); err != nil {
		return err
	}
	var sidecarObservation *sidecarRead
	if f.ArtworkObserved {
		ctx, sidecarObservation = withSidecarRead(ctx, asset)
	}
	f.ObservedRevision = revision
	var manuallyLinked int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_version_links WHERE library_id=? AND asset_id=?)`, source.LibraryID, asset).Scan(&manuallyLinked); err != nil {
		return err
	}
	if err = s.publishVideoNamingTx(ctx, tx, job, source, asset, relative, revision, kind, manuallyLinked, f); err != nil {
		return err
	}
	if policy.Allows("local_metadata") {
		if err = localmetadata.PersistVideo(ctx, tx, source.LibraryID, asset, f); err != nil {
			return err
		}
	}
	if err = s.persistScanArtworkTx(ctx, tx, source, object, revision, asset, kind, policy, f, sidecarObservation); err != nil {
		return err
	}
	// The naming work for this revision is complete; technical analysis is
	// explicitly deferred. Any analysis of bytes already acquired for playback
	// needs its own admission; this naming result grants no origin read.
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision=?,analysis_policy=?,analysis_operations_json=?,analysis_state='deferred',analysis_error='',analysis_algorithm='',analysis_generation=? WHERE id=?`, revision, policyRevision, actualScanOperations(policy, f.AnalysisOperations), source.Generation, object); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_analysis_queue SET state='done' WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=?`, job, object, revision, policyRevision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET analyzed=analyzed+1 WHERE job_id=?`, job); err != nil {
		return err
	}
	if err = inventoryEvent(ctx, tx, source.ID, job, object, asset, revision, "analysis_completed"); err != nil {
		return err
	}
	return gated.Commit()
}

func actualScanOperations(p ScanPolicy, actual []string) string {
	if actual != nil {
		b, _ := json.Marshal(actual)
		return string(b)
	}
	// Compatibility for callers that supply a complete local Basic result. The
	// durable scanner always passes exact successfully executed operations.
	return completedScanOperations(p)
}
func completedScanOperations(p ScanPolicy) string {
	ops := []string{}
	for _, v := range BasicScanOperations {
		if p.Allows(v) {
			ops = append(ops, v)
		}
	}
	b, _ := json.Marshal(ops)
	return string(b)
}

// AdoptLegacyInventoryTx records path-only baseline evidence without claiming a
// fresh observation. It runs in bounded pages, never as a startup-wide rewrite.
// Two complete inventories and the source grace still govern missing status.
func (s *Service) AdoptLegacyInventoryTx(ctx context.Context, tx *sql.Tx, source LibrarySource, asset, relative string, v storage.Snapshot) error {
	revision, evidence, err := observationRevision(source, v)
	if err != nil {
		return err
	}
	object := identity.Token()
	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES(?,?,?,?,?,?,?,?,?,CASE WHEN (SELECT available FROM catalog_assets WHERE token=?)=1 THEN 'available' ELSE 'unavailable' END)`, object, source.ID, asset, source.Incarnation, relative, revision, evidence, v.Size, v.ModifiedNS, asset)
	if err == nil {
		err = compactcatalog.ReadingNormalizeObjectTx(ctx, tx, object)
	}
	if err == nil {
		err = compactcatalog.FlushReadingPathSemanticTx(ctx, tx)
	}
	return err
}

// MarkDirectoryPruneTx asks the sidecar phase to look again at the cached
// child folders of parent: it may hold children it no longer has. The key
// starts its walk from the front for job.
func MarkDirectoryPruneTx(ctx context.Context, tx *sql.Tx, source, incarnation, parent, job string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO inventory_directory_prune(source_id,incarnation,parent_path,job_id) VALUES(?,?,?,?)
 ON CONFLICT(source_id,incarnation,parent_path) DO UPDATE SET job_id=excluded.job_id,state='ready',cursor_parent_revision='',cursor_relative_path='',saw_protected=0`, source, incarnation, parent, job)
	return err
}
