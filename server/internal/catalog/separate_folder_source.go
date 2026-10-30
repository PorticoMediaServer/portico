package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

const separateFolderCommandVersion = "separate-raw-links-v2"

type separateFolderSource struct {
	Version     string `json:"v"`
	Generation  int64  `json:"g"`
	Incarnation string `json:"i"`
	Root        string `json:"r"`
}

func separateFolderSourceTx(ctx context.Context, tx *sql.Tx, source string) (separateFolderSource, error) {
	w := separateFolderSource{Version: separateFolderCommandVersion}
	err := tx.QueryRowContext(ctx, `SELECT generation,incarnation,root_identity FROM library_sources WHERE id=?`, source).Scan(&w.Generation, &w.Incarnation, &w.Root)
	return w, err
}

func separateFolderSourceDecode(raw string) (separateFolderSource, error) {
	var w separateFolderSource
	if json.Unmarshal([]byte(raw), &w) != nil || w.Version != separateFolderCommandVersion || w.Generation < 1 || w.Incarnation == "" {
		return w, ErrSeparateFolderCursor
	}
	canonical, err := json.Marshal(w)
	if err != nil || string(canonical) != raw {
		return w, ErrSeparateFolderCursor
	}
	return w, nil
}

func separateFolderSourceCurrentTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) error {
	held, err := separateFolderSourceDecode(m.AcceptedRevision)
	if err != nil {
		return err
	}
	live, err := separateFolderSourceTx(ctx, tx, m.SplitSourceID)
	if err != nil {
		return err
	}
	if held != live {
		return compactcatalog.ErrMutationStale
	}
	return nil
}

// The first four equalities and the literal partial predicate precede LIMIT.
// Availability is checked on the returned raw row, never used to skip a prefix.
const separateFolderObjectHeadSQL = `SELECT id,revision,state,missing_since FROM inventory_objects INDEXED BY inventory_objects_separate_folder
 WHERE asset_id=?1 AND source_id=?2 AND root_incarnation=?3 AND directory_path=?4 AND retired=0 ORDER BY id LIMIT 1`

const separateFolderLatestOwnerSQL = `SELECT mutation_id,generation,owner_generation FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_owner
 WHERE owner_kind='separate' AND owner_id=? ORDER BY owner_generation DESC,subject_id DESC,mode DESC LIMIT 1`

const separateFolderNextChildSQL = `SELECT id FROM catalog_entities INDEXED BY catalog_entities_parent
 WHERE parent_id=? AND id>? ORDER BY id LIMIT 1`

// Admission keeps the existing 4000-file refusal and additionally bounds empty
// raw children. No child, link or object eligibility predicate precedes its raw
// PK LIMIT. The accepted command subsequently repeats these proofs per turn.
func separateFolderAdmissionWindowTx(ctx context.Context, tx *sql.Tx, container int64, kind compactcatalog.Kind, source, incarnation, directory string) error {
	var after int64
	var total, here, examined int
	want := compactcatalog.Track
	if kind == compactcatalog.Book {
		want = compactcatalog.Part
	}
	for examined <= 2*separateFolderScanLimit {
		var child int64
		err := tx.QueryRowContext(ctx, separateFolderNextChildSQL, container, after).Scan(&child)
		if errors.Is(err, sql.ErrNoRows) {
			if here == 0 || here == total {
				return ErrAdminQuery
			}
			return nil
		}
		if err != nil {
			return err
		}
		examined++
		after = child
		var childKind compactcatalog.Kind
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, child).Scan(&childKind); err != nil {
			return err
		}
		if childKind != want {
			continue
		}
		var assetAfter int64
		for examined <= 2*separateFolderScanLimit {
			var asset int64
			err := tx.QueryRowContext(ctx, `SELECT asset_id FROM catalog_asset_links WHERE entity_id=? AND asset_id>? ORDER BY asset_id LIMIT 1`, child, assetAfter).Scan(&asset)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			examined++
			assetAfter = asset
			total++
			if total > separateFolderScanLimit {
				return ErrAdminQuery
			}
			var token string
			if err := tx.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE id=?`, asset).Scan(&token); err != nil {
				return err
			}
			found, _, _, err := separateFolderFolderObjectTx(ctx, tx, token, source, incarnation, directory)
			if err != nil {
				return err
			}
			if found {
				here++
			}
		}
	}
	return ErrAdminQuery
}

// Retained terminal receipts may identify an original only while both exact
// entity lifetimes remain live. Never reconstruct an original after receipt GC.
func separateFolderReceiptLiveTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) error {
	for _, want := range []struct {
		id                               int64
		public, incarnation, publication string
	}{
		{m.Subject, m.Public, m.Incarnation, m.PublicationIncarnation},
		{m.TargetSubject, m.TargetPublic, m.TargetIncarnation, m.TargetPublicationIncarnation},
	} {
		var public, incarnation, publication string
		var library int64
		var kind compactcatalog.Kind
		err := tx.QueryRowContext(ctx, `SELECT pid(e.public_id),e.library_id,e.kind,COALESCE(ah.incarnation,sh.incarnation,''),COALESCE(ph.incarnation,'') FROM catalog_entities e LEFT JOIN mb_album_heads ah ON ah.album_id=e.id LEFT JOIN mb_song_heads sh ON sh.item_id=e.id LEFT JOIN metadata_publication_heads ph ON ph.item_id=e.id WHERE e.id=? AND e.retired=0`, want.id).Scan(&public, &library, &kind, &incarnation, &publication)
		if err != nil {
			return err
		}
		if public != want.public || library != m.Library || kind != m.Kind || incarnation != want.incarnation || publication != want.publication {
			return compactcatalog.ErrMutationStale
		}
	}
	return nil
}
