package administration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SelectionDeleteError gives the executor a truthful target failure code.
// It never authorizes a changed file merely because its old command was sealed.
type SelectionDeleteError struct {
	Code  string
	Cause error
}

func (e *SelectionDeleteError) Error() string { return e.Code + ": " + e.Cause.Error() }
func (e *SelectionDeleteError) Unwrap() error { return e.Cause }
func selectionDeleteChanged() error {
	return &SelectionDeleteError{Code: "file_changed", Cause: ErrConflict}
}

// SelectionDeleteAllowedTx uses the caller's snapshot; it opens no transaction.
// Session/owner/library visibility authority still belongs to the HTTP callback.
func SelectionDeleteAllowedTx(ctx context.Context, tx *sql.Tx, library string) (bool, error) {
	return deletionPolicy(ctx, tx, library)
}

const selectionDeleteUnits = 20
const (
	selectionDeleteAsset = iota
	selectionDeleteRoots
	selectionDeleteReferences
	selectionDeleteStat
	selectionDeleteFinished
)

// SelectionDeleteCursor retains one file, one root and scalar raw keyset
// cursors. It never retains a snapshot or an all-versions/reference collection.
// The producer emits Target before starting this cursor.
type SelectionDeleteCursor struct {
	target     selectionactions.Target
	file       *selectionactions.File
	afterAsset int64
	afterRoot  string
	refTable   int
	afterRef   int64
	phase      int
	units      int // most recent step's charged raw/EOF/scalar operations
}

func NewSelectionDeleteCursor(target selectionactions.Target) *SelectionDeleteCursor {
	return &SelectionDeleteCursor{target: target}
}
func selectionDeleteTargetBytes(t selectionactions.Target) int64 {
	return int64(len(t.ItemID) + len(t.LibraryID) + len(t.Kind) + len(t.Title) + len(t.Identity) + len(t.Lifetime) + len(t.VersionID) + len(t.OperationID) + len(t.DownloadIdentity))
}
func selectionDeleteFileBytes(f *selectionactions.File) int64 {
	if f == nil {
		return 0
	}
	n := int64(unsafe.Sizeof(*f)) + int64(len(f.Token)+len(f.Path)+len(f.Root)+len(f.RootIdentity)+len(f.SourceID)+len(f.SourceIncarnation)+len(f.Edition))
	if f.End != nil {
		n += 8
	}
	return n
}
func (c *SelectionDeleteCursor) Bytes() int64 {
	if c == nil {
		return 0
	}
	return int64(unsafe.Sizeof(*c)) + selectionDeleteTargetBytes(c.target) + int64(len(c.afterRoot)) + selectionDeleteFileBytes(c.file)
}
func (c *SelectionDeleteCursor) clone() *SelectionDeleteCursor {
	n := *c
	if c.file != nil {
		f := *c.file
		if f.End != nil {
			end := *f.End
			f.End = &end
		}
		n.file = &f
	}
	return &n
}

// selectionDeleteTargetTx verifies the exact entity lifetime, never resolves a
// merged/replacement public ID. The schema has no stronger incarnation than
// public_id plus nullable added_ms; same-ID/same-added recreation is not proven.
func selectionDeleteTargetTx(ctx context.Context, tx *sql.Tx, t selectionactions.Target) error {
	if t.EntityID <= 0 || t.ItemID == "" || t.LibraryID == "" || t.Identity == "" || !oneOf(t.Kind, "movie", "episode", "song", "audiobook_file") {
		return ErrInput
	}
	var public, library, kind string
	var parent, added sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT pid(e.public_id),cl.library_id,k.name,e.parent_id,e.added_ms
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind
 WHERE e.id=? AND e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0`, t.EntityID, t.ItemID).Scan(&public, &library, &kind, &parent, &added)
	if errors.Is(err, sql.ErrNoRows) {
		return selectionDeleteChanged()
	}
	if err != nil {
		return err
	}
	var parentValue any
	if parent.Valid {
		parentValue = parent.Int64
	}
	lifetime := ""
	if added.Valid {
		lifetime = strconv.FormatInt(added.Int64, 10)
	}
	if public != t.ItemID || library != t.LibraryID || kind != t.Kind || lifetime != t.Lifetime || selectionactions.TargetIdentity(public, library, kind, parentValue) != t.Identity {
		return selectionDeleteChanged()
	}
	return nil
}

// Read only one raw link (or its EOF). Oversized legal scalars fail capacity
// before decoding; they cannot silently turn into a truncated deletion command.
func selectionDeleteReadFile(ctx context.Context, tx *sql.Tx, target, after, exact int64) (*selectionactions.File, error) {
	var f selectionactions.File
	var token, path, edition sql.NullString
	var end sql.NullFloat64
	query := `SELECT l.asset_id,CASE WHEN length(CAST(a.token AS BLOB))<=65536 THEN a.token END,
 CASE WHEN length(CAST(a.path AS BLOB))<=65536 THEN a.path END,a.size,a.modified_ns,l.part_index,l.start_seconds,l.end_seconds,
 CASE WHEN length(CAST(l.edition AS BLOB))<=65536 THEN l.edition END
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`
	args := []any{target, after}
	if exact != 0 {
		query = `SELECT l.asset_id,CASE WHEN length(CAST(a.token AS BLOB))<=65536 THEN a.token END,
 CASE WHEN length(CAST(a.path AS BLOB))<=65536 THEN a.path END,a.size,a.modified_ns,l.part_index,l.start_seconds,l.end_seconds,
 CASE WHEN length(CAST(l.edition AS BLOB))<=65536 THEN l.edition END
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id=?`
		args = []any{target, exact}
	}
	err := tx.QueryRowContext(ctx, query, args...).Scan(&f.AssetID, &token, &path, &f.Size, &f.ModifiedNS, &f.Part, &f.Start, &end, &edition)
	if err != nil {
		return nil, err
	}
	if !token.Valid || !path.Valid || !edition.Valid {
		return nil, selectionactions.ErrCapacity
	}
	f.TargetID = target
	f.Token = token.String
	f.Path = path.String
	f.Edition = edition.String
	if end.Valid {
		f.End = &end.Float64
	}
	if f.Size < 0 || !filepath.IsAbs(f.Path) {
		return nil, selectionDeleteChanged()
	}
	return &f, nil
}

// Each probe is one raw indexed row, including rows belonging to this target.
// Do not put item_id<>target before LIMIT: that hides unbounded nested work.
var selectionDeleteRefs = [...]string{
	`SELECT entity_id,entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? AND entity_id>? ORDER BY entity_id LIMIT 1`,
	`SELECT rowid,item_id FROM item_extras WHERE asset_id=? AND rowid>? ORDER BY rowid LIMIT 1`,
	`SELECT rowid,item_id FROM playback_sessions WHERE asset_id=? AND rowid>? ORDER BY rowid LIMIT 1`,
	`SELECT rowid,item_id FROM lyric_resources WHERE asset_id=? AND rowid>? ORDER BY rowid LIMIT 1`,
	`SELECT rowid,item_id FROM lyric_candidates WHERE asset_id=? AND rowid>? ORDER BY rowid LIMIT 1`,
	`SELECT rowid,item_id FROM subtitle_resources WHERE source_id=? AND rowid>? ORDER BY rowid LIMIT 1`,
	`SELECT rowid,item_id FROM dvr_catalog_provenance WHERE asset_id=? AND rowid>? ORDER BY rowid LIMIT 1`,
}

func (c *SelectionDeleteCursor) raw(ctx context.Context, tx *sql.Tx) (record *selectionactions.Record, err error) {
	c.units++
	switch c.phase {
	case selectionDeleteAsset:
		f, e := selectionDeleteReadFile(ctx, tx, c.target.EntityID, c.afterAsset, 0)
		if errors.Is(e, sql.ErrNoRows) {
			c.phase = selectionDeleteFinished
			return &selectionactions.Record{Finish: c.target.EntityID}, nil
		}
		if e != nil {
			return nil, e
		}
		c.file = f
		c.afterRoot = ""
		c.refTable = 0
		c.afterRef = 0
		c.phase = selectionDeleteRoots
	case selectionDeleteRoots:
		var id, root, incarnation, rootIdentity sql.NullString
		var generation int64
		e := tx.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(id AS BLOB))<=65536 THEN id END,
 CASE WHEN length(CAST(root AS BLOB))<=65536 THEN root END,
 CASE WHEN length(CAST(incarnation AS BLOB))<=65536 THEN incarnation END,
 CASE WHEN length(CAST(root_identity AS BLOB))<=65536 THEN root_identity END,generation
 FROM library_sources INDEXED BY library_sources_library WHERE library_id=? AND id>? ORDER BY id LIMIT 1`, c.target.LibraryID, c.afterRoot).Scan(&id, &root, &incarnation, &rootIdentity, &generation)
		if errors.Is(e, sql.ErrNoRows) {
			c.phase = selectionDeleteReferences
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		if !id.Valid || !root.Valid || !incarnation.Valid || !rootIdentity.Valid {
			return nil, selectionactions.ErrCapacity
		}
		c.afterRoot = id.String
		rel, e := filepath.Rel(root.String, c.file.Path)
		if e == nil && filepath.IsAbs(root.String) && filepath.IsLocal(rel) && rel != "." && len(root.String) > len(c.file.Root) {
			c.file.Root = root.String
			c.file.RootIdentity = rootIdentity.String
			c.file.SourceID = id.String
			c.file.SourceIncarnation = incarnation.String
			c.file.SourceGeneration = generation
		}
	case selectionDeleteReferences:
		if c.refTable == len(selectionDeleteRefs) || c.file.Shared {
			c.phase = selectionDeleteStat
			return nil, nil
		}
		var key, owner int64
		var asset any = c.file.Token
		if c.refTable == 0 {
			asset = c.file.AssetID
		}
		e := tx.QueryRowContext(ctx, selectionDeleteRefs[c.refTable], asset, c.afterRef).Scan(&key, &owner)
		if errors.Is(e, sql.ErrNoRows) {
			c.refTable++
			c.afterRef = 0
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		c.afterRef = key
		if owner != c.target.EntityID {
			c.file.Shared = true
		}
	case selectionDeleteStat:
		if err = selectionDeleteObserve(c.file); err != nil {
			return nil, err
		}
		f := c.file
		c.afterAsset = f.AssetID
		c.file = nil
		c.afterRoot = ""
		c.phase = selectionDeleteAsset
		return &selectionactions.Record{File: f}, nil
	}
	return nil, nil
}

// selectionDeleteObserve pins a physical root even when an older source has no
// admitted root_identity. removeSelectionMediaFile verifies it again at unlink.
func selectionDeleteObserve(f *selectionactions.File) error {
	if f.Root == "" || f.SourceID == "" {
		return selectionDeleteChanged()
	}
	canonical, err := filepath.EvalSymlinks(f.Root)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(f.Root) {
		return selectionDeleteChanged()
	}
	root, err := os.Open(f.Root)
	if err != nil {
		return selectionDeleteChanged()
	}
	physical, err := storage.FileObjectIdentity(root)
	root.Close()
	if err != nil || (f.RootIdentity != "" && !storage.SameRoot(f.RootIdentity, physical)) {
		return selectionDeleteChanged()
	}
	f.RootIdentity = physical
	info, err := os.Lstat(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		f.Present = false
		return nil
	}
	if err != nil {
		return &SelectionDeleteError{Code: "file_not_removed", Cause: err}
	}
	if !info.Mode().IsRegular() || info.Size() != f.Size || info.ModTime().UnixNano() != f.ModifiedNS {
		return selectionDeleteChanged()
	}
	f.Present = true
	return nil
}

// StepSelectionDelete opens and closes a fresh read snapshot. The returned
// cursor is a clone; errors and cancellation never advance the input cursor.
// Every raw row/EOF and filesystem scalar consumes one of twenty units.
func (s *Service) StepSelectionDelete(ctx context.Context, cursor *SelectionDeleteCursor) (next *SelectionDeleteCursor, records []selectionactions.Record, done bool, err error) {
	if cursor == nil {
		return nil, nil, false, ErrInput
	}
	next = cursor.clone()
	next.units = 0
	if next.Bytes() > selectionactions.MaxSourceBytes {
		return nil, nil, false, selectionactions.ErrCapacity
	}
	if next.phase == selectionDeleteFinished {
		return next, nil, true, nil
	}
	err = s.snapshot(ctx, nil, func(tx *sql.Tx) error {
		next.units++ // exact target scalar
		if e := selectionDeleteTargetTx(ctx, tx, next.target); e != nil {
			return e
		}
		for next.units < selectionDeleteUnits && next.phase != selectionDeleteFinished {
			if e := ctx.Err(); e != nil {
				return e
			}
			record, e := next.raw(ctx, tx)
			if e != nil {
				return e
			}
			if next.Bytes() > selectionactions.MaxSourceBytes {
				return selectionactions.ErrCapacity
			}
			if record != nil {
				records = append(records, *record)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	return next, records, next.phase == selectionDeleteFinished, nil
}

func selectionDeleteSameLink(a, b *selectionactions.File) bool {
	return a.TargetID == b.TargetID && a.AssetID == b.AssetID && a.Token == b.Token && a.Path == b.Path && a.Size == b.Size && a.ModifiedNS == b.ModifiedNS && a.Part == b.Part && a.Start == b.Start && a.Edition == b.Edition && ((a.End == nil && b.End == nil) || (a.End != nil && b.End != nil && *a.End == *b.End))
}

// selectionDeleteLiveFileTx rechecks source/root/owners using bounded raw probes
// in this very snapshot. Without a maintained cross-table reference fence a
// partial scan cannot grant unlink permission; dense unprovable cases refuse.
func selectionDeleteLiveFileTx(ctx context.Context, tx *sql.Tx, t selectionactions.Target, approved *selectionactions.File) error {
	f, err := selectionDeleteReadFile(ctx, tx, t.EntityID, 0, approved.AssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return selectionDeleteChanged()
	}
	if err != nil {
		return err
	}
	if !selectionDeleteSameLink(f, approved) {
		return selectionDeleteChanged()
	}
	c := NewSelectionDeleteCursor(t)
	c.file = f
	c.phase = selectionDeleteRoots
	for c.units < selectionDeleteUnits {
		record, e := c.raw(ctx, tx)
		if e != nil {
			return e
		}
		if record == nil {
			continue
		}
		current := record.File
		if current == nil || current.Root != approved.Root || !storage.SameRoot(approved.RootIdentity, current.RootIdentity) || current.SourceID != approved.SourceID || current.SourceIncarnation != approved.SourceIncarnation || current.SourceGeneration != approved.SourceGeneration || (!approved.Shared && current.Shared) || (!approved.Present && current.Present) {
			return selectionDeleteChanged()
		}
		return nil
	}
	return selectionDeleteChanged()
}

// ApplySelectionDelete applies exactly one approved file/link, or (file=nil)
// one final target. The worker must durably set Started before the first call;
// absent replay never invents attribution. checkpoint owns journal deletion and
// runs in the same writer as the observed catalogue effect. No writer spans I/O.
// Authorize receives (ctx, tx, principal, action, target.LibraryID, &target);
// OperationID is preserved for the caller's live cancellation/authority checks.
func (s *Service) ApplySelectionDelete(ctx context.Context, p identity.Principal, action string, options selectionactions.Options, target selectionactions.Target, file *selectionactions.File, authorize selectionactions.Authorize, checkpoint func(*sql.Tx, selectionactions.Effect) error) error {
	if action != "delete" || options.DeleteFiles == nil || authorize == nil || checkpoint == nil {
		return ErrInput
	}
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	if dbwork.InWriteTx(ctx) || dbwork.Snapshot(ctx) != nil {
		return ErrInput
	}
	// Only this command holds the filesystem lock, never the entire job.
	s.files.Lock()
	defer s.files.Unlock()
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	check := func(checkCtx context.Context, tx *sql.Tx) error {
		if err := authorize(checkCtx, tx, p, action, target.LibraryID, &target); err != nil {
			return err
		}
		if err := selectionDeleteTargetTx(checkCtx, tx, target); err != nil {
			return err
		}
		allowed, err := SelectionDeleteAllowedTx(checkCtx, tx, target.LibraryID)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrDenied
		}
		return nil
	}
	if file == nil {
		// The existing title/state/empty-parent cascade is one indivisible domain
		// command. All arbitrarily many asset links have already left separately.
		plan := dbwork.WritePlan{Name: "selection-delete-title", Rows: 8192, Pages: 2048, VMOps: 2_000_000, Why: "One approved title's retained profile state and empty-parent catalogue cascade are indivisible; asset links are removed individually beforehand."}
		return s.transaction(dbwork.WithPlan(ctx, plan), nil, func(tx *sql.Tx) error {
			if err := check(ctx, tx); err != nil {
				return err
			}
			var asset int64
			err := tx.QueryRowContext(ctx, `SELECT asset_id FROM catalog_asset_links WHERE entity_id=? ORDER BY asset_id LIMIT 1`, target.EntityID).Scan(&asset)
			if err == nil {
				return selectionDeleteChanged()
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			// The journal reauthorizes target visibility in its checkpoint. Run
			// it while the exact target is still live, then remove the catalogue
			// entity in this same gated transaction. Any subsequent domain error
			// rolls back the journal outcome together with the removal.
			if err = checkpoint(tx, selectionactions.Effect{}); err != nil {
				return err
			}
			if err = catalog.RemoveTitleTx(ctx, tx, target.EntityID); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, target.LibraryID)
			return err
		})
	}
	if file.TargetID != target.EntityID || file.AssetID <= 0 {
		return ErrInput
	}
	approved := *file
	err := s.snapshot(ctx, nil, func(tx *sql.Tx) error {
		if e := check(ctx, tx); e != nil {
			return e
		}
		return selectionDeleteLiveFileTx(ctx, tx, target, &approved)
	})
	if err != nil {
		return err
	}
	effect := selectionactions.Effect{}
	disk := *options.DeleteFiles && !approved.Shared
	if disk {
		if err = ctx.Err(); err != nil {
			return err
		}
		removed, code := removeSelectionMediaFile(approved)
		if code != "" {
			return &SelectionDeleteError{Code: code, Cause: ErrConflict}
		}
		if removed {
			effect.Files = 1
			effect.Bytes = approved.Size
		}
	}
	// A cancellation/revocation after observed I/O cannot erase that fact. This
	// bounded writer acknowledges existing effects; a fresh check gates any new
	// link deletion. Nothing here grants another filesystem operation.
	reconcile := ctx
	cancel := func() {}
	if disk {
		reconcile, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	}
	defer cancel()
	var refused error
	err = s.transaction(reconcile, nil, func(tx *sql.Tx) error {
		if disk {
			if e := selectionDeleteReconcileTx(reconcile, tx, target, &approved); e != nil {
				return e
			}
		}
		if refused = check(reconcile, tx); refused == nil {
			current, e := selectionDeleteReadFile(reconcile, tx, target.EntityID, 0, approved.AssetID)
			if e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					refused = selectionDeleteChanged()
				} else {
					return e
				}
			} else if !selectionDeleteSameLink(current, &approved) {
				refused = selectionDeleteChanged()
			}
		}
		if refused == nil {
			// Do not SettleMissing here: finalization still owns this exact entity.
			if _, e := tx.ExecContext(reconcile, `DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, target.EntityID, approved.AssetID); e != nil {
				return e
			}
			for _, domain := range []int{compactcatalog.DomainAvailability, compactcatalog.DomainItemMetrics, compactcatalog.DomainBrowseRows} {
				if e := compactcatalog.TouchTx(reconcile, tx, domain, target.EntityID); e != nil {
					return e
				}
			}
			if _, e := tx.ExecContext(reconcile, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, target.LibraryID); e != nil {
				return e
			}
		} else {
			if !disk {
				return refused
			}
			effect.Code = "file_changed"
			if !errors.Is(refused, ErrConflict) {
				effect.Code = "authority_revoked"
			}
		}
		return checkpoint(tx, effect)
	})
	if err != nil {
		return err
	}
	return refused
}

// Reconcile only the exact observed asset and target link. Avoid helpers that
// enumerate all owners or settle/remove a title while its command is unfinished.
func selectionDeleteReconcileTx(ctx context.Context, tx *sql.Tx, t selectionactions.Target, f *selectionactions.File) error {
	result, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET available=0 WHERE id=? AND token=? AND path=? AND size=? AND modified_ns=?`, f.AssetID, f.Token, f.Path, f.Size, f.ModifiedNS)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return err
	}
	// Reused entity IDs must not inherit even a missing-link update. The asset
	// observation itself remains valid independently of target ownership.
	if err = selectionDeleteTargetTx(ctx, tx, t); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	// A fresh link check below decides whether the approved association can leave.
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=? AND asset_id=?`, t.EntityID, f.AssetID); err != nil {
		return err
	}
	for _, domain := range []int{compactcatalog.DomainAvailability, compactcatalog.DomainItemMetrics, compactcatalog.DomainBrowseRows} {
		if err = compactcatalog.TouchTx(ctx, tx, domain, t.EntityID); err != nil {
			return err
		}
	}
	return compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainAssetMetrics, f.AssetID)
}

// This is removeMediaFile's directory/root/file-handle protection, with a
// stricter observation result: Remove returning ENOENT means zero attributed
// bytes, rather than claiming that this command removed an already absent file.
// It lives here because this helper owns new files only.
func removeSelectionMediaFile(f selectionactions.File) (bool, string) {
	if f.Root == "" || f.RootIdentity == "" {
		return false, "file_changed"
	}
	expected, err := os.Lstat(f.Root)
	if err != nil || !expected.IsDir() {
		return false, "file_not_removed"
	}
	canonical, err := filepath.EvalSymlinks(f.Root)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(f.Root) {
		return false, "file_changed"
	}
	root, err := os.OpenRoot(f.Root)
	if err != nil {
		return false, "file_not_removed"
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(expected, current) {
		return false, "file_changed"
	}
	directory, err := root.Open(".")
	if err != nil {
		return false, "file_not_removed"
	}
	physical, err := storage.FileObjectIdentity(directory)
	directory.Close()
	if err != nil || !storage.SameRoot(f.RootIdentity, physical) {
		return false, "file_changed"
	}
	rel, err := filepath.Rel(f.Root, f.Path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return false, "file_changed"
	}
	parent, err := root.OpenRoot(filepath.Dir(rel))
	if errors.Is(err, os.ErrNotExist) {
		return false, ""
	}
	if err != nil {
		return false, "file_not_removed"
	}
	defer parent.Close()
	name := filepath.Base(rel)
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, ""
	}
	if err != nil {
		return false, "file_not_removed"
	}
	if !f.Present || !info.Mode().IsRegular() || info.Size() != f.Size || info.ModTime().UnixNano() != f.ModifiedNS {
		return false, "file_changed"
	}
	if err = parent.Remove(name); errors.Is(err, os.ErrNotExist) {
		return false, ""
	} else if err != nil {
		return false, "file_not_removed"
	}
	return true, ""
}
