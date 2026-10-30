package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// SeparateFolderFileRequest names one original association, never whichever
// title currently happens to own an asset. Before must recheck the parent's
// actual command authority, source/derivation custody and HasElsewhere decision
// in tx. The parent obtains that decision with its raw keyset walk, not a
// folder scan here. False preserves the existing whole-child reparent policy;
// true leaves the original child (and all its personal state) where it was.
// The caller supplies any already-validated mutation-family context. This
// primitive does not create a command, acquire custody or widen a write plan.
type SeparateFolderFileRequest struct {
	Library, Object, Revision, Source, Directory string
	SourceGeneration                             int64
	SourceIncarnation, RootIdentity, AssetPath   string
	OldContainer, NewContainer, Child, Asset     int64
	HasElsewhere                                 bool
	Link                                         compactcatalog.Link // parent's original link snapshot
	Before                                       func(context.Context, *sql.Tx) error
}

// SeparateFolderFileResult identifies the exact published child. Repeated is
// true only after checking the manual decision, identity, parent, facts and
// association. The parent owns container creation/touches and its raw cursor.
type SeparateFolderFileResult struct {
	Child    int64
	Repeated bool
}

// ErrSeparateFolderFileProjection refuses an unbounded existing book-file
// reprojection. ReprojectBookFileTx walks every linked/contributing asset; a
// multi-asset reparent needs a parent-owned bounded evidence seam first.
var ErrSeparateFolderFileProjection = errors.New("separate folder: book reparent has another linked or contributing asset")

// SeparateFolderFileTx publishes one caller-selected file unit in the supplied
// writer. Any error requires the caller to roll back tx, including Before's
// writes. Unlink, link and the rescan owner decision share this transaction.
// Download/prepared-media claims and personal state are deliberately untouched;
// the parent retires split-file claims under its same actual command. A whole
// reparent preserves claims because the item and asset identities are unchanged.
func SeparateFolderFileTx(ctx context.Context, tx *sql.Tx, req SeparateFolderFileRequest) (SeparateFolderFileResult, error) {
	zero := SeparateFolderFileResult{}
	if tx == nil || req.Before == nil || req.Library == "" || req.Object == "" || req.Revision == "" || req.Source == "" || req.SourceGeneration <= 0 || req.SourceIncarnation == "" || req.AssetPath == "" || req.OldContainer <= 0 || req.NewContainer <= 0 || req.Child <= 0 || req.Asset <= 0 || req.OldContainer == req.NewContainer {
		return zero, ErrAdminQuery
	}
	if err := req.Before(ctx, tx); err != nil {
		return zero, err
	}
	for _, id := range []int64{req.OldContainer, req.NewContainer, req.Child} {
		if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, id); err != nil {
			return zero, err
		}
	}
	var library, parent int64
	var root, token, title string
	var kind compactcatalog.Kind
	// All joins are point identities. A replaced source root, changed file,
	// retired library/title, or moved object cannot silently continue the split.
	err := tx.QueryRowContext(ctx, `SELECT cl.id,cl.root,a.token,c.kind,c.parent_id,c.title
 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id
 JOIN catalog_assets a ON a.token=o.asset_id
 JOIN catalog_libraries cl ON cl.library_id=s.library_id AND cl.retired=0
 JOIN catalog_entities c ON c.id=?1 AND c.library_id=cl.id AND c.retired=0
 JOIN catalog_entities old ON old.id=?2 AND old.library_id=cl.id AND old.retired=0
 JOIN catalog_entities new ON new.id=?3 AND new.library_id=cl.id AND new.retired=0 AND new.kind=old.kind AND new.parent_id IS old.parent_id
 JOIN catalog_kept_apart k ON k.entity_id=new.id
 WHERE o.id=?4 AND o.revision=?5 AND o.source_id=?6 AND o.directory_path=?7
 AND o.retired=0 AND o.state='available' AND o.missing_since='' AND o.root_incarnation=s.incarnation
 AND s.library_id=?8 AND s.generation=?9 AND s.incarnation=?10 AND s.root_identity=?11
 AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing')
 AND a.id=?12 AND a.path=?13 AND a.available=1 AND a.size=o.size AND a.modified_ns=o.modified_ns
 AND ((c.kind=?14 AND old.kind=?15) OR (c.kind=?16 AND old.kind=?17))`,
		req.Child, req.OldContainer, req.NewContainer, req.Object, req.Revision, req.Source, req.Directory, req.Library, req.SourceGeneration, req.SourceIncarnation, req.RootIdentity, req.Asset, req.AssetPath,
		compactcatalog.Track, compactcatalog.Album, compactcatalog.Part, compactcatalog.Book).Scan(&library, &root, &token, &kind, &parent, &title)
	if err != nil {
		return zero, err
	}
	table, parentColumn, numberColumn := "catalog_songs", "album_id", "track_number"
	if kind == compactcatalog.Part {
		table, parentColumn, numberColumn = "catalog_book_files", "book_id", "part_number"
	}
	var factParent int64
	var disc, number sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT `+parentColumn+`,disc_number,`+numberColumn+` FROM `+table+` WHERE entity_id=?`, req.Child).Scan(&factParent, &disc, &number); err != nil {
		return zero, err
	}
	var exactContainer bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_identities i JOIN catalog_entities e ON e.public_id=i.public_id WHERE i.root=? AND i.source_key=? AND e.id=?)`, root, compactcatalog.ItemKey(root, req.Source+"/"+req.Directory, 0)+"#separate", req.NewContainer).Scan(&exactContainer); err != nil {
		return zero, err
	}
	if !exactContainer {
		return zero, compactcatalog.ErrMutationStale
	}
	var manual int64
	err = tx.QueryRowContext(ctx, `SELECT item_id FROM inventory_version_links WHERE library_id=? AND asset_id=?`, req.Library, token).Scan(&manual)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	key := compactcatalog.ItemKey(root, req.AssetPath, 0) + "#separate"
	if manual != 0 && (manual != req.Child || parent == req.NewContainer) {
		if req.HasElsewhere && (parent != req.OldContainer || factParent != req.OldContainer) || !req.HasElsewhere && (parent != req.NewContainer || factParent != req.NewContainer) {
			return zero, compactcatalog.ErrMutationStale
		}
		if err = separateFolderPublishedFileTx(ctx, tx, req, manual, library, kind, root, key, table, parentColumn, numberColumn, disc, number); err != nil {
			return zero, err
		}
		return SeparateFolderFileResult{Child: manual, Repeated: true}, nil
	}
	if !req.HasElsewhere && parent == req.NewContainer && factParent == req.NewContainer {
		// An earlier file unit reparented this same child with all its versions
		// (the existing album policy). Publish only this file's owner memory;
		// do not require the parent to rescan or reparent the child again.
		if err = separateFolderPublishedFileTx(ctx, tx, req, req.Child, library, kind, root, key, table, parentColumn, numberColumn, disc, number); err != nil {
			return zero, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET item_id=excluded.item_id`, req.Library, token, req.Child); err != nil {
			return zero, err
		}
		return SeparateFolderFileResult{Child: req.Child}, nil
	}
	if parent != req.OldContainer || factParent != req.OldContainer {
		return zero, compactcatalog.ErrMutationStale
	}
	link := compactcatalog.Link{}
	var end sql.NullFloat64
	if err = tx.QueryRowContext(ctx, `SELECT part_index,start_seconds,end_seconds,edition FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND part_index=? AND start_seconds=? AND end_seconds IS ? AND edition=?`, req.Child, req.Asset, req.Link.Part, req.Link.Start, separateFolderEnd(req.Link.End), req.Link.Edition).Scan(&link.Part, &link.Start, &end, &link.Edition); err != nil {
		return zero, err
	}
	if end.Valid {
		link.End = &end.Float64
	}
	child := req.Child
	if req.HasElsewhere {
		// The link's Reading FK can carry unbounded children even on an
		// inconsistent listening row. Never invoke that cascade here.
		var publication bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_publications WHERE item_id=? AND asset_id=?)`, child, req.Asset).Scan(&publication); err != nil {
			return zero, err
		}
		if publication {
			return zero, compactcatalog.ErrEntityMutating
		}
		// EnsureEntityTx can reparent an existing identity. Never use that
		// behaviour to adopt another title or resurrect an old split here.
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_identities WHERE root=? AND source_key=?)`, root, key).Scan(&occupied); err != nil {
			return zero, err
		}
		if occupied {
			return zero, compactcatalog.ErrMutationStale
		}
		facts := map[string]any{parentColumn: req.NewContainer, "disc_number": separateFolderNumber(disc), numberColumn: separateFolderNumber(number)}
		child, _, err = compactcatalog.EnsureEntityWithFactsTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: kind, Parent: req.NewContainer, Key: key, Title: title, Added: catalogedNow()}, facts)
		if err != nil {
			return zero, err
		}
		if err = KeepApartTx(ctx, tx, child); err != nil {
			return zero, err
		}
		// Preserve the existing delete/insert cascade and all derived fences.
		// An entity_id UPDATE is a different trigger contract.
		if err = compactcatalog.UnlinkAssetTx(ctx, tx, req.Child, req.Asset); err != nil {
			return zero, err
		}
		if err = compactcatalog.LinkAssetTx(ctx, tx, child, req.Asset, link); err != nil {
			return zero, err
		}
	} else {
		if kind == compactcatalog.Part {
			// Bound ReprojectBookFileTx's asset union to this exact one file.
			// Each disjunct is an indexed range probe, not a count/array walk.
			var other bool
			if err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=?1 AND asset_id<?2) OR EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=?1 AND asset_id>?2)
 OR EXISTS(SELECT 1 FROM catalog_book_series_contrib WHERE file_id=?1 AND asset_id<?2) OR EXISTS(SELECT 1 FROM catalog_book_series_contrib WHERE file_id=?1 AND asset_id>?2)
 OR EXISTS(SELECT 1 FROM catalog_book_position_contrib WHERE file_id=?1 AND asset_id<?2) OR EXISTS(SELECT 1 FROM catalog_book_position_contrib WHERE file_id=?1 AND asset_id>?2)`, child, req.Asset).Scan(&other); err != nil {
				return zero, err
			}
			if other {
				return zero, ErrSeparateFolderFileProjection
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=?`, req.NewContainer, child); err != nil {
			return zero, err
		}
		// This is an explicit owner decision. Do not let an automatic-field
		// lock leave the backbone and kind-specific parent disagreeing.
		if err = compactcatalog.SetFieldsTx(ctx, tx, child, compactcatalog.Owner, map[string]any{parentColumn: req.NewContainer}); err != nil {
			return zero, err
		}
		if err = KeepApartTx(ctx, tx, child); err != nil {
			return zero, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET item_id=excluded.item_id`, req.Library, token, child); err != nil {
		return zero, err
	}
	if kind == compactcatalog.Part {
		if err = compactcatalog.ReprojectBookFileTx(ctx, tx, child); err != nil {
			return zero, err
		}
		if req.HasElsewhere {
			// The original file may still contribute this removed asset to its
			// old book. Its existing raw evidence walker retires that pair;
			// reprojecting all of the original's other assets here is unbounded.
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_evidence_jobs(kind,key) VALUES(1,?) ON CONFLICT(kind,key) DO UPDATE SET phase=0,cursor=0`, req.Child); err != nil {
				return zero, err
			}
		}
	}
	return SeparateFolderFileResult{Child: child}, nil
}

func separateFolderNumber(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func separateFolderEnd(end *float64) any {
	if end != nil {
		return *end
	}
	return nil
}

func separateFolderPublishedFileTx(ctx context.Context, tx *sql.Tx, req SeparateFolderFileRequest, child, library int64, kind compactcatalog.Kind, root, key, table, parentColumn, numberColumn string, disc, number sql.NullInt64) error {
	if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, child); err != nil {
		return err
	}
	if (child == req.Child) == req.HasElsewhere {
		return compactcatalog.ErrMutationStale
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e
 JOIN `+table+` f ON f.entity_id=e.id AND f.`+parentColumn+`=?2 AND f.disc_number IS ?3 AND f.`+numberColumn+` IS ?4
 JOIN catalog_asset_links l ON l.entity_id=e.id AND l.asset_id=?5 AND l.part_index=?11 AND l.start_seconds=?12 AND l.end_seconds IS ?13 AND l.edition=?14
 JOIN catalog_kept_apart k ON k.entity_id=e.id
 WHERE e.id=?1 AND e.library_id=?6 AND e.kind=?7 AND e.parent_id=?2 AND e.retired=0
 AND (?1=?8 OR (EXISTS(SELECT 1 FROM catalog_identities WHERE root=?9 AND source_key=?10 AND public_id=e.public_id)
 AND NOT EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=?8 AND asset_id=?5))))`, child, req.NewContainer, separateFolderNumber(disc), separateFolderNumber(number), req.Asset, library, kind, req.Child, root, key, req.Link.Part, req.Link.Start, separateFolderEnd(req.Link.End), req.Link.Edition).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return compactcatalog.ErrMutationStale
	}
	return nil
}
