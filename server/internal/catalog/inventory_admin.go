package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// InventoryRow is owner-only. Locators never appear in the viewer status DTO.
type InventoryRow struct {
	ID                string `json:"id"`
	SourceID          string `json:"sourceId"`
	AssetID           string `json:"assetId"`
	ItemID            string `json:"itemId"`
	Title             string `json:"title"`
	RelativePath      string `json:"relativePath"`
	Revision          string `json:"revision"`
	State             string `json:"state"`
	Size              int64  `json:"size"`
	ModifiedNS        int64  `json:"modifiedNs"`
	MissingSince      string `json:"missingSince"`
	AnalysisState     string `json:"analysisState"`
	AnalysisError     string `json:"analysisError"`
	UnsupportedReason string `json:"unsupportedReason"`
}
type InventoryPageResult struct {
	Scope      AdminScope     `json:"scope"`
	LibraryID  string         `json:"libraryId"`
	SourceID   string         `json:"sourceId"`
	Revision   int64          `json:"revision"`
	Items      []InventoryRow `json:"items"`
	NextCursor string         `json:"nextCursor"`
}

// Inventory is a live keyset listing, fenced to the library configuration, not
// a snapshot of a running scan. The stable location ID orders each bounded page.
func (s *Service) Inventory(ctx context.Context, r AdminRequest, source, state string) (InventoryPageResult, error) {
	if dbwork.Snapshot(ctx) == nil {
		var out InventoryPageResult
		err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).Inventory(ctx, r, source, state)
			return err
		})
		return out, err
	}
	if _, err := adminLibraryScope(ctx, &r); err != nil {
		return InventoryPageResult{}, err
	}
	out := InventoryPageResult{Scope: AdminScope{r.ServerID, r.ViewerFence}, LibraryID: r.LibraryID, SourceID: source, Items: []InventoryRow{}}
	if err := adminLimit(&r); err != nil {
		return out, err
	}
	if state != "" && state != "missing" && state != "available" && state != "unsupported" {
		return out, ErrAdminQuery
	}
	if err := s.read().QueryRowContext(ctx, `SELECT revision FROM library_configuration WHERE library_id=?`, r.LibraryID).Scan(&out.Revision); err != nil {
		return out, err
	}
	scope := cursorScope{Profile: r.Profile, Viewer: r.ViewerFence, Library: r.LibraryID, View: "inventory", Entity: source, Category: state + ":" + r.AssetID, Limit: r.Limit}
	rev := ContentRevision{Catalog: out.Revision}
	after := ""
	if r.Cursor != "" {
		c, err := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if err != nil {
			return out, err
		}
		after = c.ID
	}
	where := `s.library_id=? AND o.id>?`
	args := []any{r.LibraryID, after}
	if source != "" {
		where += ` AND s.id=?`
		args = append(args, source)
	}
	if r.AssetID != "" {
		if len(r.AssetID) > 160 {
			return out, ErrAdminQuery
		}
		where += ` AND o.asset_id=?`
		args = append(args, r.AssetID)
	}
	switch state {
	case "missing":
		where += ` AND o.state='missing'`
	case "available":
		where += ` AND o.state='available' AND o.retired=0 AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing') AND o.root_incarnation=s.incarnation AND o.unsupported_reason=''`
	case "unsupported":
		where += ` AND o.unsupported_reason!=''`
	}
	args = append(args, r.Limit+1)
	rows, err := s.read().QueryContext(ctx, `SELECT o.id,o.source_id,o.asset_id,COALESCE((SELECT pid(ve.public_id) FROM inventory_version_links v JOIN catalog_entities ve ON ve.id=v.item_id WHERE v.library_id=s.library_id AND v.asset_id=o.asset_id),(SELECT pid(i.public_id) FROM catalog_assets a JOIN catalog_asset_links ia INDEXED BY catalog_asset_links_asset ON ia.asset_id=a.id JOIN catalog_entities i ON i.id=ia.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE a.token=o.asset_id AND cl.library_id=s.library_id ORDER BY ia.entity_id LIMIT 1),''),o.relative_path,o.revision,CASE WHEN o.retired=1 OR o.root_incarnation!=s.incarnation THEN 'replaced' WHEN s.enabled=0 THEN 'source_removed' WHEN s.health IN('offline','root_changed') THEN 'source_unavailable' WHEN o.unsupported_reason!='' THEN 'unsupported' ELSE o.state END,o.size,o.modified_ns,o.missing_since,o.analysis_state,o.analysis_error,o.unsupported_reason FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id WHERE `+where+` ORDER BY o.id LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v InventoryRow
		if err = rows.Scan(&v.ID, &v.SourceID, &v.AssetID, &v.ItemID, &v.RelativePath, &v.Revision, &v.State, &v.Size, &v.ModifiedNS, &v.MissingSince, &v.AnalysisState, &v.AnalysisError, &v.UnsupportedReason); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) > r.Limit {
		out.Items = out.Items[:r.Limit]
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, ID: out.Items[len(out.Items)-1].ID, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if err != nil {
			return out, err
		}
	}
	for i := range out.Items {
		v := &out.Items[i]
		if v.ItemID != "" {
			if err = s.read().QueryRowContext(ctx, `SELECT e.title FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND cl.library_id=?`, v.ItemID, r.LibraryID).Scan(&v.Title); err != nil {
				return out, err
			}
		}
	}
	var current int64
	err = s.read().QueryRowContext(ctx, `SELECT revision FROM library_configuration WHERE library_id=?`, r.LibraryID).Scan(&current)
	if err == nil && current != out.Revision {
		err = ErrStaleContinuation
	}
	return out, err
}

type InventoryChange struct {
	Sequence  int64  `json:"sequence"`
	SourceID  string `json:"sourceId"`
	JobID     string `json:"jobId"`
	ObjectID  string `json:"objectId"`
	AssetID   string `json:"assetId"`
	Revision  string `json:"revision"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"createdAt"`
}

type InventoryChangesPage struct {
	SourceID       string            `json:"sourceId"`
	Items          []InventoryChange `json:"items"`
	NextSequence   int64             `json:"nextSequence"`
	HasMore        bool              `json:"hasMore"`
	ResetRequired  bool              `json:"resetRequired"`
	OldestSequence int64             `json:"oldestSequence"`
}

func (s *Service) InventoryChangesPage(ctx context.Context, library, source string, after int64) (InventoryChangesPage, error) {
	out := InventoryChangesPage{SourceID: source, Items: []InventoryChange{}, NextSequence: after}
	if after < 0 {
		return out, ErrAdminQuery
	}
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		var floor int64
		if err := dbwork.QueryRow(ctx, s.db, `SELECT changes_pruned_sequence FROM library_sources WHERE id=? AND library_id=?`, source, library).Scan(&floor); err != nil {
			return err
		}
		out.ResetRequired = after > 0 && after < floor
		if err := dbwork.QueryRow(ctx, s.db, `SELECT COALESCE(min(sequence),0) FROM inventory_changes WHERE source_id=?`, source).Scan(&out.OldestSequence); err != nil {
			return err
		}
		rows, err := dbwork.Query(ctx, s.db, `SELECT sequence,source_id,job_id,object_id,asset_id,revision,kind,created_at FROM inventory_changes WHERE source_id=? AND sequence>? ORDER BY sequence LIMIT 129`, source, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v InventoryChange
			if err = rows.Scan(&v.Sequence, &v.SourceID, &v.JobID, &v.ObjectID, &v.AssetID, &v.Revision, &v.Kind, &v.CreatedAt); err != nil {
				return err
			}
			out.Items = append(out.Items, v)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Items) > 128 {
			out.HasMore = true
			out.Items = out.Items[:128]
		}
		if len(out.Items) > 0 {
			out.NextSequence = out.Items[len(out.Items)-1].Sequence
		}
		return nil
	})
	return out, err
}
func (s *Service) InventoryChanges(ctx context.Context, library, source string, after int64) ([]InventoryChange, int64, error) {
	out, err := s.InventoryChangesPage(ctx, library, source, after)
	return out.Items, out.NextSequence, err
}

// CheckSource publishes health only after reauthorizing and comparing the exact
// source generation. A successful check never creates absence evidence.
func (s *Service) CheckSource(ctx context.Context, library, id string, authorize func(*sql.Tx) error) (LibrarySource, error) {
	source, err := s.LibrarySource(ctx, id)
	if err != nil {
		return source, err
	}
	if source.LibraryID != library {
		return source, sql.ErrNoRows
	}
	observed, checkErr := s.inspectSource(ctx, source.ResolvedPath)
	health := "ready"
	if checkErr != nil {
		health = "offline"
	} else if storage.RootIdentity(observed) == "" || source.RootIdentity != "" && !storage.SameRoot(source.RootIdentity, storage.RootIdentity(observed)) {
		health = "root_changed"
	}
	if ctx.Err() != nil {
		return source, ctx.Err()
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return source, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize == nil {
		return source, ErrAdminQuery
	}
	if err = authorize(tx); err != nil {
		return source, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE library_sources SET health=? WHERE id=? AND library_id=? AND generation=? AND enabled=1`, health, id, library, source.Generation)
	if err != nil {
		return source, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return source, ErrSourceChanged
	}
	if err = gated.Commit(); err != nil {
		return source, err
	}
	return s.LibrarySource(ctx, id)
}

// SeparateInventoryVersion makes a file that was grouped as a version of a
// title its own title again: the answer when two different films or tracks
// were taken for one. The file keeps everything known about it; the title it
// leaves keeps its other files and everyone's state. The decision is
// remembered, so a later scan does not group them again.
func (s *Service) SeparateInventoryVersion(ctx context.Context, library, object, revision string, authorize func(*sql.Tx) error) (string, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return "", err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize == nil {
		return "", ErrAdminQuery
	}
	if err = authorize(tx); err != nil {
		return "", err
	}
	var asset, source, relative, libraryKind string
	if err = tx.QueryRowContext(ctx, `SELECT o.asset_id,o.source_id,o.relative_path,l.kind FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id JOIN libraries l ON l.id=s.library_id WHERE o.id=? AND o.revision=? AND o.retired=0 AND s.library_id=?`, object, revision, library).Scan(&asset, &source, &relative, &libraryKind); err != nil {
		return "", err
	}
	if libraryKind != "movie" && libraryKind != "music" && libraryKind != "audiobook" {
		// An episode is placed by assigning it to a show, season and number.
		return "", ErrAdminQuery
	}
	if err = sourcePlaybackBusy(ctx, tx, source); err != nil {
		return "", err
	}
	assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
	if err != nil {
		return "", err
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return "", err
	}
	var current int64
	var kind compactcatalog.Kind
	var title string
	var year, others int
	if err = tx.QueryRowContext(ctx, `SELECT e.id,e.kind,e.title,e.year,(SELECT count(*) FROM catalog_asset_links x WHERE x.entity_id=e.id AND x.asset_id<>l.asset_id) FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_entities e ON e.id=l.entity_id
 JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 WHERE l.asset_id=? AND e.library_id=? ORDER BY e.id LIMIT 1`, assetID, handle).Scan(&current, &kind, &title, &year, &others); err != nil {
		return "", err
	}
	if others == 0 {
		// The only file of its title is already on its own.
		return "", ErrAdminQuery
	}
	root, err := compactcatalog.LibraryRootTx(ctx, tx, handle)
	if err != nil {
		return "", err
	}
	var path string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM catalog_assets WHERE id=?`, assetID).Scan(&path); err != nil {
		return "", err
	}

	link := compactcatalog.Link{}
	var end sql.NullFloat64
	if err = tx.QueryRowContext(ctx, `SELECT part_index,start_seconds,end_seconds,edition FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, current, assetID).Scan(&link.Part, &link.Start, &end, &link.Edition); err != nil {
		return "", err
	}
	if end.Valid {
		link.End = &end.Float64
	}
	if err = compactcatalog.UnlinkAssetTx(ctx, tx, current, assetID); err != nil {
		return "", err
	}
	// The new title is known by its place alone, so no name it shares with the
	// title it left can pull it back.
	key := compactcatalog.ItemKey(root, path, 0) + "#separate"
	separate, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: kind, Key: key, Title: title, Year: year, Added: catalogedNow()})
	if err != nil {
		return "", err
	}
	if kind == compactcatalog.Track {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_songs(entity_id,album_id,disc_number,track_number) SELECT ?,album_id,disc_number,track_number FROM catalog_songs WHERE entity_id=?`, separate, current); err != nil {
			return "", err
		}
	}
	if kind == compactcatalog.Part {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_book_files(entity_id,book_id,disc_number,part_number) SELECT ?,book_id,disc_number,part_number FROM catalog_book_files WHERE entity_id=?`, separate, current); err != nil {
			return "", err
		}
	}
	if err = moveDownloadClaimsTx(ctx, tx, current, separate, asset); err != nil {
		return "", err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, separate, assetID, link); err != nil {
		return "", err
	}
	if err = compactcatalog.TouchEntityTx(ctx, tx, separate); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET item_id=excluded.item_id`, library, asset, separate); err != nil {
		return "", err
	}
	if err = inventoryEvent(ctx, tx, source, "owner:"+identity.Token(), object, asset, revision, "version_separated"); err != nil {
		return "", err
	}
	var public string
	if err = tx.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, separate).Scan(&public); err != nil {
		return "", err
	}
	return public, gated.Commit()
}

// AssociateInventoryVersion adds a physical alternative to an existing logical
// item. It never deletes an item's history or infers identity from its title.
// Episodic boundaries and multipart position must match an existing association.
func (s *Service) AssociateInventoryVersion(ctx context.Context, library, object, revision, target string, authorize func(*sql.Tx) error) error {
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if authorize == nil {
		return ErrAdminQuery
	}
	if err = authorize(tx); err != nil {
		return err
	}
	var asset, source, kind, libraryKind string
	err = tx.QueryRowContext(ctx, `SELECT o.asset_id,o.source_id,k.name,l.kind FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id JOIN libraries l ON l.id=s.library_id JOIN catalog_libraries cl ON cl.library_id=l.id JOIN catalog_entities i ON i.library_id=cl.id JOIN catalog_kinds k ON k.id=i.kind WHERE o.id=? AND o.revision=? AND o.retired=0 AND s.library_id=? AND i.public_id=pid_blob(?)`, object, revision, library, target).Scan(&asset, &source, &kind, &libraryKind)
	if err != nil {
		return err
	}
	if err = sourcePlaybackBusy(ctx, tx, source); err != nil {
		return err
	}
	valid := libraryKind == "movie" && kind == "movie" || (libraryKind == "tv" || libraryKind == "anime") && kind == "episode" || libraryKind == "music" && kind == "song" || libraryKind == "audiobook" && kind == "audiobook_file"
	if !valid {
		return ErrAdminQuery
	}
	item, err := entityid.Resolve(ctx, tx, target)
	if err != nil {
		return err
	}
	assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
	if err != nil {
		return err
	}
	var part int
	var start, end sql.NullFloat64
	// Do not flatten multipart or range-bound media by guessing a part/boundary.
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(DISTINCT part_index||':'||COALESCE(start_seconds,-1)||':'||COALESCE(end_seconds,-1)) FROM catalog_asset_links WHERE entity_id=?`, item).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return requestinput.New("select an unambiguous whole-source item for a physical version")
	}
	if err = tx.QueryRowContext(ctx, `SELECT part_index,start_seconds,end_seconds FROM catalog_asset_links WHERE entity_id=? ORDER BY asset_id LIMIT 1`, item).Scan(&part, &start, &end); err != nil {
		return err
	}
	if start.Valid && start.Float64 != 0 || end.Valid {
		return requestinput.New("range-bound versions require explicit boundary evidence")
	}
	if kind == "episode" {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM episode_asset_boundaries WHERE item_id=? AND status='whole_source'`, item).Scan(&n); err != nil {
			return err
		}
		if n < 1 {
			return requestinput.New("episode boundary is unresolved")
		}
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, item, assetID, compactcatalog.Link{Part: part}); err != nil {
		return err
	}
	if kind == "episode" {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(?,?,'whole_source')`, item, asset); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET item_id=excluded.item_id`, library, asset, item); err != nil {
		return err
	}
	if err = inventoryEvent(ctx, tx, source, "owner:"+identity.Token(), object, asset, revision, "version_associated"); err != nil {
		return err
	}
	return gated3.Commit()
}

// InventoryStatus is the path-free viewer projection shared by web/iOS/tvOS.
type InventoryStatus struct {
	LibraryID string                  `json:"libraryId"`
	Revision  int64                   `json:"revision"`
	Sources   []InventorySourceStatus `json:"sources"`
}
type InventorySourceStatus struct {
	DeferredChanges   bool   `json:"deferredChanges,omitempty"`
	DeferredScanScope string `json:"deferredScanScope,omitempty"`
	ID                string `json:"id"`
	Health            string `json:"health"`
	LastCompleteAt    string `json:"lastCompleteAt"`
	JobID             string `json:"jobId"`
	Status            string `json:"status"`
	Phase             string `json:"phase"`
	Discovered        int64  `json:"discovered"`
	Analyzed          int64  `json:"analyzed"`
	Warnings          int64  `json:"warnings"`
	PauseReason       string `json:"pauseReason"`
}

func (s *Service) InventoryStatus(ctx context.Context, library string) (InventoryStatus, error) {
	out := InventoryStatus{LibraryID: library, Sources: []InventorySourceStatus{}}
	if err := s.read().QueryRowContext(ctx, `SELECT revision FROM library_revisions WHERE library_id=?`, library).Scan(&out.Revision); err != nil {
		return out, err
	}
	rows, err := s.read().QueryContext(ctx, `SELECT s.id,s.health,s.last_complete_at,COALESCE(j.id,''),COALESCE(j.status,'not_scanned'),COALESCE(r.phase,''),COALESCE(r.discovered,0),COALESCE(r.analyzed,0),COALESCE(r.warnings,0),COALESCE(r.pause_reason,''),COALESCE((SELECT CASE i.mode WHEN 'incremental' THEN 'source' ELSE 'folders' END FROM inventory_strict_intents i WHERE i.source_id=s.id AND i.library_id=s.library_id AND i.source_generation=s.generation AND i.root_incarnation=s.incarnation AND i.root_identity=s.root_identity AND i.root=s.root AND i.configured_root=s.configured_root AND i.source_kind=s.kind AND i.follow_symlinks=s.follow_symlinks AND (i.mode<>'strict' OR s.identity_confirmed=1) AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id) LIMIT 1),'') FROM library_sources s LEFT JOIN jobs j ON j.id=COALESCE((SELECT job_id FROM inventory_source_active WHERE source_id=s.id),(SELECT x.job_id FROM inventory_runs x JOIN jobs y ON y.id=x.job_id WHERE x.source_id=s.id ORDER BY y.created_at DESC,y.id DESC LIMIT 1)) LEFT JOIN inventory_runs r ON r.job_id=j.id WHERE s.library_id=? ORDER BY s.id LIMIT 64`, library)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v InventorySourceStatus
		if err = rows.Scan(&v.ID, &v.Health, &v.LastCompleteAt, &v.JobID, &v.Status, &v.Phase, &v.Discovered, &v.Analyzed, &v.Warnings, &v.PauseReason, &v.DeferredScanScope); err != nil {
			return out, err
		}
		v.DeferredChanges = v.DeferredScanScope != ""
		out.Sources = append(out.Sources, v)
	}
	return out, rows.Err()
}

// Sequence parameter parsing deliberately rejects negative/overflow values.
func ParseInventorySequence(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, ErrAdminQuery
	}
	return n, nil
}

// VerifyInventoryAbsence rechecks the nearest enumerated ancestor outside any
// database writer. A removed subtree is covered by its unchanged, fully listed
// parent; permission errors or changed evidence never establish absence.
func (s *Service) VerifyInventoryAbsence(ctx context.Context, source LibrarySource, job, relative string) error {
	if !filepath.IsLocal(relative) {
		return ErrAdminQuery
	}
	parent := filepath.Dir(relative)
	for {
		var dirID, dirRev string
		err := s.read().QueryRowContext(ctx, `SELECT identity,revision FROM inventory_directories WHERE job_id=? AND relative_path=? AND state='done'`, job, parent).Scan(&dirID, &dirRev)
		if err == nil {
			if s.storage.IsRemote(source.ResolvedPath) {
				verifier, ok := s.storage.Remote.(storage.RemoteInventoryVerifier)
				if !ok {
					return storage.ErrRemoteConfig
				}
				return verifier.VerifyInventoryDirectory(ctx, job, filepath.Join(source.ResolvedPath, parent), dirRev)
			}
			req := storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, RelativePath: parent, FollowSymlinks: source.FollowSymlinks}
			var observed storage.Snapshot
			if s.storage != nil {
				observed, err = s.storage.InventoryDirectory(ctx, source.ID, req)
			} else {
				observed, err = storage.LocalInventoryDirectory(req)
			}
			if err != nil {
				return err
			}
			if observed.ObjectIdentity != dirID || storage.InventoryRevision(observed) != dirRev {
				return storage.ErrInventoryChanged
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if parent == "." {
			return storage.ErrInventoryChanged
		}
		parent = filepath.Dir(parent)
	}
}
