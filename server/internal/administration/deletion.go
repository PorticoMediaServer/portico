// Deleting media. Deleting from Portico deletes the file on disk: nothing is
// moved into the server's own folders and nothing is kept for later. What a
// delete will take is shown first (PreviewDelete) and the owner types back the
// title, or the count, before anything goes.
package administration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// MaxDeleteTargets bounds one bulk delete. A larger sweep is a scan-driven
// operation, not a page action.
const MaxDeleteTargets = 200

// DeleteFile is one file a delete would remove.
type DeleteFile struct {
	AssetID string `json:"assetId"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	// Present is false when the file is already gone from storage; the catalog
	// row is still removed.
	Present bool `json:"present"`
	// Shared is true when a title outside this delete also uses the file, so
	// the delete keeps the file and removes only this title's claim on it.
	Shared bool `json:"shared"`
	// What the catalogue last saw at this path. A file that no longer matches
	// is some other file and is never removed.
	size, modifiedNS int64
	root             string
	rootIdentity     string
}

// Dependent is something that refers to the item and will change when it goes.
type Dependent struct {
	Kind        string `json:"kind"`
	Count       int64  `json:"count"`
	Description string `json:"description"`
}

// DeleteTarget is one item's share of a preview. A show, season, album, artist
// or book lists the files of everything that plays beneath it.
type DeleteTarget struct {
	ItemID             string `json:"itemId"`
	Title              string `json:"title"`
	Kind               string `json:"kind"`
	LibraryID          string `json:"libraryId"`
	LibraryName        string `json:"libraryName"`
	AllowMediaDeletion bool   `json:"allowMediaDeletion"`
	// Titles is how many playable titles go with this item (1 for a film).
	Titles     int          `json:"titles"`
	Files      []DeleteFile `json:"files"`
	Bytes      int64        `json:"bytes"`
	Dependents []Dependent  `json:"dependents"`
	// entityID is the catalogue integer the SQL statements use. It never
	// leaves the server: public ids stay the API's only identifier.
	entityID  int64
	members   []catalog.TitleDeletePlan
	emptyPlan catalog.TitleDeletePlan
	gone      []DeleteFile
}

// DeletePreview is what the delete dialog renders before anything is touched.
type DeletePreview struct {
	Revision int64          `json:"revision"`
	Targets  []DeleteTarget `json:"targets"`
	// Confirmation is exactly what the owner must type back: one item's title,
	// or the target count for a bulk delete.
	Confirmation     string   `json:"confirmation"`
	ConfirmationKind string   `json:"confirmationKind"`
	TotalBytes       int64    `json:"totalBytes"`
	TotalFiles       int      `json:"totalFiles"`
	TotalBytesText   string   `json:"totalBytesText"`
	Blocked          []string `json:"blocked"`
}

// DeleteRequest is the delete form. deleteFiles false removes the titles from
// the library and leaves the files where they are; the next scan finds them
// again and each profile gets its state back.
type DeleteRequest struct {
	ItemIDs          []string `json:"itemIds"`
	DeleteFiles      bool     `json:"deleteFiles"`
	Confirmation     string   `json:"confirmation"`
	ExpectedRevision int64    `json:"expectedRevision"`
	OperationID      string   `json:"operationId"`
}

// DeleteReceipt is one item's outcome.
type DeleteReceipt struct {
	ItemID       string `json:"itemId"`
	Title        string `json:"title"`
	Removed      bool   `json:"removed"`
	FilesDeleted int    `json:"filesDeleted"`
	FilesKept    int    `json:"filesKept"`
	Bytes        int64  `json:"bytes"`
	// Code says why an item was not removed: file_not_removed (storage refused
	// or the file is read-only) or file_changed (the file at that path is no
	// longer the one in the library).
	Code string `json:"code,omitempty"`
}

// DeleteResult is the whole operation's outcome.
type DeleteResult struct {
	Revision     int64           `json:"revision"`
	Receipts     []DeleteReceipt `json:"receipts"`
	Removed      int             `json:"removed"`
	Failed       int             `json:"failed"`
	BytesDeleted int64           `json:"bytesDeleted"`
	// FilesRetained is true when the caller asked to keep the files, so the page
	// can say the bytes are still on disk.
	FilesRetained bool `json:"filesRetained"`
}

func deleteFence(ctx context.Context, tx *sql.Tx, libraries []string) (int64, error) {
	if len(libraries) == 0 {
		return 0, nil
	}
	unique := map[string]bool{}
	ordered := []string{}
	for _, id := range libraries {
		if !unique[id] {
			unique[id] = true
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	var total int64
	for _, id := range ordered {
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM library_revisions WHERE library_id=?`, id).Scan(&revision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		total += revision
	}
	return total, nil
}

func tableExists(ctx context.Context, tx *sql.Tx, name string) bool {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	return err == nil
}

// dependents counts what refers to an item. Each probe is guarded by a table
// check so a build without one of these features still previews correctly.
func dependents(ctx context.Context, tx *sql.Tx, item string) ([]Dependent, error) {
	out := []Dependent{}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return nil, err
	}
	probes := []struct{ table, kind, query, description string }{
		{"catalog_collection_members", "collection", `SELECT COUNT(*) FROM catalog_collection_members WHERE item_id=?`, "collections that list this item"},
		{"catalog_playlist_entries", "playlist", `SELECT COUNT(*) FROM catalog_playlist_entries WHERE item_id=?`, "playlist entries that point at this item"},
		{"saved_resource_entries", "saved", `SELECT COUNT(*) FROM saved_resource_entries WHERE item_id=?`, "saved resources that list this item"},
		{"progress", "progress", `SELECT COUNT(*) FROM progress WHERE item_id=?`, "profiles with saved playback position"},
		{"lc_entries", "library-channel", `SELECT COUNT(*) FROM lc_entries WHERE item_id=?`, "scheduled library channel slots"},
		{"dvr_recordings", "recording", `SELECT COUNT(*) FROM dvr_recordings WHERE item_id=? AND state!='deleted'`, "recordings published as this item"},
	}
	for _, probe := range probes {
		if !tableExists(ctx, tx, probe.table) {
			continue
		}
		var count int64
		if err := tx.QueryRowContext(ctx, probe.query, id).Scan(&count); err != nil {
			// A schema shape this build does not share is not a preview failure.
			continue
		}
		if count > 0 {
			out = append(out, Dependent{probe.kind, count, probe.description})
		}
	}
	return out, nil
}

func targetFor(ctx context.Context, tx *sql.Tx, item string) (DeleteTarget, error) {
	t := DeleteTarget{ItemID: item, Files: []DeleteFile{}, Dependents: []Dependent{}}
	err := tx.QueryRowContext(ctx, `SELECT e.id,e.title,k.name,cl.library_id,l.name FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id WHERE e.public_id=pid_blob(?) AND cl.retired=0`, item).Scan(&t.entityID, &t.Title, &t.Kind, &t.LibraryID, &t.LibraryName)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if t.AllowMediaDeletion, err = deletionPolicy(ctx, tx, t.LibraryID); err != nil {
		return t, err
	}
	members, err := catalog.TitleMembersTx(ctx, tx, t.entityID)
	if errors.Is(err, catalog.ErrTooManyMembers) {
		return t, ErrInput
	}
	if err != nil {
		return t, err
	}
	t.emptyPlan, err = catalog.CaptureTitleDeleteTx(ctx, tx, t.entityID)
	if err != nil {
		return t, err
	}
	t.Titles = len(members)
	inside := map[int64]bool{t.entityID: true}
	for _, member := range members {
		inside[member] = true
		plan, err := catalog.CaptureTitleDeleteTx(ctx, tx, member)
		if err != nil {
			return t, err
		}
		t.members = append(t.members, plan)
	}
	// Only a file under one of the library's own folders is ever removed.
	roots := []string{}
	rootIdentities := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT root,root_identity FROM library_sources WHERE library_id=?`, t.LibraryID)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var root, identity string
		if err = rows.Scan(&root, &identity); err != nil {
			rows.Close()
			return t, err
		}
		roots = append(roots, root)
		rootIdentities[root] = identity
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return t, err
	}
	seen := map[string]bool{}
	for _, member := range members {
		files, err := tx.QueryContext(ctx, `SELECT a.id,a.token,a.path,a.size,a.modified_ns FROM catalog_asset_links al JOIN catalog_assets a ON a.id=al.asset_id WHERE al.entity_id=? ORDER BY al.part_index,a.id`, member)
		if err != nil {
			return t, err
		}
		type row struct {
			id int64
			f  DeleteFile
		}
		var batch []row
		for files.Next() {
			var r row
			if err = files.Scan(&r.id, &r.f.AssetID, &r.f.Path, &r.f.size, &r.f.modifiedNS); err != nil {
				files.Close()
				return t, err
			}
			batch = append(batch, r)
		}
		err = files.Err()
		files.Close()
		if err != nil {
			return t, err
		}
		for _, r := range batch {
			if seen[r.f.AssetID] {
				continue
			}
			seen[r.f.AssetID] = true
			f := r.f
			f.Bytes = f.size
			// Another title's file, or a file something else still needs, stays.
			users, err := tx.QueryContext(ctx, `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?1
 UNION SELECT item_id FROM item_extras WHERE asset_id=?2 UNION SELECT item_id FROM playback_sessions WHERE asset_id=?2
 UNION SELECT item_id FROM lyric_resources WHERE asset_id=?2 UNION SELECT item_id FROM lyric_candidates WHERE asset_id=?2
 UNION SELECT item_id FROM subtitle_resources WHERE source_id=?2 UNION SELECT item_id FROM dvr_catalog_provenance WHERE asset_id=?2`, r.id, f.AssetID)
			if err != nil {
				return t, err
			}
			for users.Next() {
				var user int64
				if err = users.Scan(&user); err != nil {
					users.Close()
					return t, err
				}
				if !inside[user] {
					f.Shared = true
				}
			}
			err = users.Err()
			users.Close()
			if err != nil {
				return t, err
			}
			for _, root := range roots {
				if underAny([]string{root}, f.Path) && len(root) > len(f.root) {
					f.root = root
					f.rootIdentity = rootIdentities[root]
				}
			}
			if f.root != "" {
				if info, statErr := os.Lstat(f.Path); statErr == nil && info.Mode().IsRegular() {
					f.Present = true
					f.Bytes = info.Size()
				}
			}
			if !f.Shared {
				t.Bytes += f.Bytes
			}
			t.Files = append(t.Files, f)
		}
	}
	t.Dependents, err = dependents(ctx, tx, item)
	return t, err
}

func underAny(roots []string, path string) bool {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, path); err == nil && filepath.IsLocal(rel) {
			return true
		}
	}
	return false
}

func validTargets(ids []string) error {
	if len(ids) == 0 || len(ids) > MaxDeleteTargets {
		return ErrInput
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || seen[id] || !safeText(id, 128) {
			return ErrInput
		}
		seen[id] = true
	}
	return nil
}

// PreviewDelete lists exactly what a delete would take. It never writes.
func (s *Service) PreviewDelete(ctx context.Context, auth Authorize, ids []string) (DeletePreview, error) {
	out := DeletePreview{Targets: []DeleteTarget{}, Blocked: []string{}}
	if err := validTargets(ids); err != nil {
		return out, err
	}
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		libraries := []string{}
		for _, id := range ids {
			target, err := targetFor(ctx, tx, id)
			if err != nil {
				return err
			}
			if !target.AllowMediaDeletion {
				out.Blocked = append(out.Blocked, target.LibraryID)
			}
			libraries = append(libraries, target.LibraryID)
			out.TotalBytes += target.Bytes
			out.TotalFiles += len(target.Files)
			out.Targets = append(out.Targets, target)
		}
		fence, err := deleteFence(ctx, tx, libraries)
		out.Revision = fence
		return err
	})
	if err != nil {
		return out, err
	}
	out.TotalBytesText = formatBytes(out.TotalBytes)
	if len(out.Targets) == 1 {
		out.Confirmation, out.ConfirmationKind = out.Targets[0].Title, "title"
	} else {
		out.Confirmation, out.ConfirmationKind = strconv.Itoa(len(out.Targets)), "count"
	}
	// Duplicate library identifiers in the blocked list help nobody.
	seen, blocked := map[string]bool{}, []string{}
	for _, id := range out.Blocked {
		if !seen[id] {
			seen[id] = true
			blocked = append(blocked, id)
		}
	}
	sort.Strings(blocked)
	out.Blocked = blocked
	return out, nil
}

// removeMediaFile deletes one catalogued file from disk. The file must still
// be the one the catalogue saw (same size and modified time): a different
// file that has taken the path is left alone. A file already gone is done.
func removeMediaFile(f DeleteFile) (removed bool, code string) {
	if f.root == "" {
		return false, "file_changed"
	}
	// The configured root is already canonical. Pin that directory before
	// resolving any descendants; replacing it or a parent with a symlink
	// cannot redirect the delete into a different tree.
	expected, err := os.Lstat(f.root)
	if err != nil || !expected.IsDir() {
		return false, "file_not_removed"
	}
	canonical, err := filepath.EvalSymlinks(f.root)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(f.root) {
		return false, "file_changed"
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return false, "file_not_removed"
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(expected, current) {
		return false, "file_changed"
	}
	if f.rootIdentity != "" {
		file, err := root.Open(".")
		if err != nil {
			return false, "file_not_removed"
		}
		identity, err := storage.FileObjectIdentity(file)
		file.Close()
		if err != nil || !storage.SameRoot(f.rootIdentity, identity) {
			return false, "file_changed"
		}
	}
	rel, err := filepath.Rel(f.root, f.Path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return false, "file_changed"
	}
	// Pin the parent too, so validation and removal resolve the final name
	// through the same directory handle even when an ancestor is renamed.
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
	if !info.Mode().IsRegular() || info.Size() != f.size || info.ModTime().UnixNano() != f.modifiedNS {
		return false, "file_changed"
	}
	if err = parent.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, "file_not_removed"
	}
	return true, ""
}

// deleteMemberBatch is how many titles one write transaction removes, so a
// long delete never holds the database for long.
const deleteMemberBatch = 40

// Delete removes titles and, when asked, their files from disk.
//
// It checks everything first with the owner re-verified (permission per
// library, the revision the preview was made against, the typed confirmation),
// then removes the files with no database writer held, then removes the titles
// in short transactions and records the outcome. Each profile's state for a
// removed title is kept under the title's identity, so the same title added
// again later gets it back. A file that cannot be removed leaves its title in
// the library and says so.
func (s *Service) Delete(ctx context.Context, auth Authorize, actor string, request DeleteRequest) (DeleteResult, error) {
	ctx = catalog.MutationWriteContext(ctx)
	out := DeleteResult{Receipts: []DeleteReceipt{}, FilesRetained: !request.DeleteFiles}
	if err := validTargets(request.ItemIDs); err != nil {
		return out, err
	}
	if !validOperationID(request.OperationID) || !safeText(request.Confirmation, 400) {
		return out, ErrInput
	}
	s.files.Lock()
	defer s.files.Unlock()
	const scope = "media-delete"
	digest := digestOf(request)
	var targets []DeleteTarget
	libraries := []string{}
	replayed := false
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		stored, found, err := receipt[DeleteResult](ctx, tx, scope, request.OperationID, digest)
		if err != nil {
			return err
		}
		if found {
			out, replayed = stored, true
			return nil
		}
		targets, libraries = targets[:0], libraries[:0]
		for _, id := range request.ItemIDs {
			target, err := targetFor(ctx, tx, id)
			if err != nil {
				return err
			}
			if !target.AllowMediaDeletion {
				return ErrDenied
			}
			libraries = append(libraries, target.LibraryID)
			targets = append(targets, target)
		}
		fence, err := deleteFence(ctx, tx, libraries)
		if err != nil {
			return err
		}
		if fence != request.ExpectedRevision {
			return ErrConflict
		}
		expected := strconv.Itoa(len(targets))
		if len(targets) == 1 {
			expected = targets[0].Title
		}
		if request.Confirmation != expected {
			return ErrConfirmation
		}
		return nil
	})
	if err != nil || replayed {
		return out, err
	}
	// Management and this library's deletion policy are current in every
	// drain/final writer. A captured confirmation is not a continuing grant.
	liveAuth := func(ctx context.Context, tx *sql.Tx) error {
		if auth != nil {
			if e := auth(ctx, tx); e != nil {
				return e
			}
		}
		for _, library := range libraries {
			allowed, e := deletionPolicy(ctx, tx, library)
			if e != nil {
				return e
			}
			if !allowed {
				return ErrDenied
			}
		}
		return nil
	}

	receipts := make([]DeleteReceipt, len(targets))
	for i, target := range targets {
		r := DeleteReceipt{ItemID: target.ItemID, Title: target.Title, Removed: true}
		for _, file := range target.Files {
			if file.Shared || !request.DeleteFiles {
				if file.Shared {
					r.FilesKept++
				}
				continue
			}
			if err = ctx.Err(); err != nil {
				return out, err
			}
			removed, code := removeMediaFile(file)
			if code != "" {
				r.Removed, r.Code = false, code
				r.FilesKept++
				break
			}
			if removed {
				r.FilesDeleted++
				r.Bytes += file.Bytes
			}
			targets[i].gone = append(targets[i].gone, file)
		}
		receipts[i] = r
	}
	// The files are gone; the catalogue follows. Each title goes in short
	// transactions, and a title whose file could not be removed stays.
	for i, target := range targets {
		ctx := catalog.WithAdministrationDeleteTarget(ctx, target.emptyPlan, request.OperationID+":"+digest)
		for start := 0; start < len(target.gone); start += deleteMemberBatch {
			end := min(start+deleteMemberBatch, len(target.gone))
			if err = s.transaction(ctx, liveAuth, func(tx *sql.Tx) error {
				return markDeletedFilesTx(ctx, tx, target.gone[start:end])
			}); err != nil {
				return out, err
			}
		}
		if !receipts[i].Removed {
			continue
		}
		for _, plan := range target.members {
			complete := false
			for !complete {
				err = s.transaction(ctx, liveAuth, func(tx *sql.Tx) error {
					step, e := catalog.DeleteApprovedTitleStepTx(ctx, tx, plan, request.OperationID+":"+digest)
					if e != nil {
						return e
					}
					complete = step.Complete
					if complete && step.Outcome != "removed" {
						receipts[i].Removed, receipts[i].Code = false, "file_changed"
					}
					if complete {
						_, e = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, target.LibraryID)
					}
					return e
				})
				if err != nil {
					return out, err
				}
				if !complete {
					dbwork.Yield(ctx)
				}
			}
		}
		if !receipts[i].Removed {
			continue
		}
		// The zero-member policy never adopts newly attached members. Pending
		// container/season work commits and resumes before the final receipt.
		emptyDone := false
		for !emptyDone {
			err = s.transaction(ctx, liveAuth, func(tx *sql.Tx) error {
				step, e := catalog.DeleteEmptyApprovedContainerStepTx(ctx, tx, target.emptyPlan, request.OperationID+":"+digest)
				if e != nil {
					return e
				}
				emptyDone = step.Complete
				if step.Complete && step.Outcome == "stale" {
					receipts[i].Removed, receipts[i].Code = false, "file_changed"
				}
				return nil
			})
			if err != nil {
				return out, err
			}
			if !emptyDone {
				dbwork.Yield(ctx)
			}
		}
	}
	err = s.transaction(ctx, liveAuth, func(tx *sql.Tx) error {
		out.Receipts, out.Removed, out.Failed, out.BytesDeleted = out.Receipts[:0], 0, 0, 0
		for _, r := range receipts {
			out.Receipts = append(out.Receipts, r)
			out.BytesDeleted += r.Bytes
			if r.Removed {
				out.Removed++
			} else {
				out.Failed++
			}
		}
		var e error
		if out.Revision, e = deleteFence(ctx, tx, libraries); e != nil {
			return e
		}
		if e = saveReceipt(ctx, tx, scope, request.OperationID, digest, out, s.milliseconds()); e != nil {
			return e
		}
		return catalog.AcknowledgeAdministrationTx(ctx, tx, request.OperationID+":"+digest)
	})
	_ = actor
	return out, err
}

// A title preserved because a new version arrived must stop offering the
// old files already removed from disk. New observations of replacement bytes
// at those locators are left alone.
func markDeletedFilesTx(ctx context.Context, tx *sql.Tx, files []DeleteFile) error {
	for _, file := range files {
		var asset int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_assets WHERE token=? AND path=? AND size=? AND modified_ns=?`, file.AssetID, file.Path, file.size, file.modifiedNS).Scan(&asset)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inventory_objects SET state='missing',missing_since=CASE WHEN missing_since='' THEN ? ELSE missing_since END WHERE asset_id=? AND size=? AND modified_ns=?`, time.Now().UTC().Format(time.RFC3339Nano), file.AssetID, file.size, file.modifiedNS); err != nil {
			return err
		}
		if err := compactcatalog.SetAssetAvailableTx(ctx, tx, asset, false); err != nil {
			return err
		}
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
