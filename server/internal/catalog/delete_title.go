package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// MaxDeleteMembers bounds how many playable titles one deleted container may
// hold (a show's episodes, an artist's tracks). A larger one is deleted a
// season or an album at a time.
const MaxDeleteMembers = 5000

// TitleDeletePlan captures the public identity and exact file associations
// approved for removal. It is internal server state, never a client contract.
type TitleDeletePlan struct {
	ItemID string
	Files  string
}

func CaptureTitleDeleteTx(ctx context.Context, tx *sql.Tx, entity int64) (TitleDeletePlan, error) {
	var plan TitleDeletePlan
	var library, kind int64
	var parent sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT pid(public_id),library_id,kind,parent_id FROM catalog_entities WHERE id=?`, entity).Scan(&plan.ItemID, &library, &kind, &parent); err != nil {
		return plan, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.token,a.path,a.size,a.modified_ns,l.part_index,l.start_seconds,l.end_seconds,l.edition,COALESCE(o.id,''),COALESCE(o.revision,''),COALESCE(o.root_incarnation,''),COALESCE(o.object_identity,''),COALESCE(s.id,''),COALESCE(s.generation,0),COALESCE(s.incarnation,''),COALESCE(s.root_identity,''),COALESCE(s.root,'')
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id LEFT JOIN inventory_objects o ON o.asset_id=a.token LEFT JOIN library_sources s ON s.id=o.source_id WHERE l.entity_id=? ORDER BY a.token,o.id`, entity)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	var incarnation, publication string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(ah.incarnation,sh.incarnation,''),COALESCE(ph.incarnation,'') FROM catalog_entities e LEFT JOIN mb_album_heads ah ON ah.album_id=e.id LEFT JOIN mb_song_heads sh ON sh.item_id=e.id LEFT JOIN metadata_publication_heads ph ON ph.item_id=e.id WHERE e.id=?`, entity).Scan(&incarnation, &publication); err != nil {
		return plan, err
	}
	if err := enc.Encode([]any{library, kind, parent, incarnation, publication}); err != nil {
		return plan, err
	}
	for rows.Next() {
		var token, path, edition, object, objectRevision, rootIncarnation, objectIdentity, source, sourceIncarnation, rootIdentity, sourceRoot string
		var sourceGeneration int64
		var size, modified int64
		var part int
		var start float64
		var end sql.NullFloat64
		if err := rows.Scan(&token, &path, &size, &modified, &part, &start, &end, &edition, &object, &objectRevision, &rootIncarnation, &objectIdentity, &source, &sourceGeneration, &sourceIncarnation, &rootIdentity, &sourceRoot); err != nil {
			return plan, err
		}
		if err := enc.Encode([]any{token, path, size, modified, part, start, end, edition, object, objectRevision, rootIncarnation, objectIdentity, source, sourceGeneration, sourceIncarnation, rootIdentity, sourceRoot}); err != nil {
			return plan, err
		}
	}
	if err := rows.Err(); err != nil {
		return plan, err
	}
	plan.Files = hex.EncodeToString(h.Sum(nil))
	return plan, nil
}

// DeleteApprovedTitleTx never expands an approved delete to newly attached
// files, and resolves a public ID again so reused integer IDs are harmless.
func DeleteApprovedTitleTx(ctx context.Context, tx *sql.Tx, plan TitleDeletePlan) (bool, error) {
	entity, err := entityid.Resolve(ctx, tx, plan.ItemID)
	if errors.Is(err, entityid.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	current, err := CaptureTitleDeleteTx(ctx, tx, entity)
	if err != nil {
		return false, err
	}
	if current != plan {
		return false, nil
	}
	return true, RemoveTitleTx(ctx, tx, entity)
}

// ErrTooManyMembers: the container holds more titles than one delete takes.
var ErrTooManyMembers = errors.New("too many titles for one delete")

// titleMembersSQL lists the media titles a delete of ?1 covers. Reading is a
// nonplayable leaf, including when its final attachment has gone.
const titleMembersSQL = `SELECT e.id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?1 AND (k.playable=1 OR k.id IN(20,21))
 UNION SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=?1
 UNION SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_season WHERE season_id=?1
 UNION SELECT entity_id FROM catalog_songs INDEXED BY catalog_songs_album_order WHERE album_id=?1
 UNION SELECT s.entity_id FROM catalog_albums a CROSS JOIN catalog_songs s INDEXED BY catalog_songs_album_order ON s.album_id=a.entity_id WHERE a.artist_id=?1
 UNION SELECT entity_id FROM catalog_book_files INDEXED BY catalog_book_files_order WHERE book_id=?1`

// TitleMembersTx returns the playable titles a delete of an entity covers.
func TitleMembersTx(ctx context.Context, tx *sql.Tx, entity int64) ([]int64, error) {
	members, err := scanInt64s(tx.QueryContext(ctx, titleMembersSQL+` ORDER BY 1 LIMIT ?2`, entity, MaxDeleteMembers+1))
	if err != nil {
		return nil, err
	}
	if len(members) > MaxDeleteMembers {
		return nil, ErrTooManyMembers
	}
	return members, nil
}

// DeleteTitleMembersTx removes up to limit of the playable titles under an
// entity and reports how many remain. A long delete is several short
// transactions, so nothing else waits on it for long.
func DeleteTitleMembersTx(ctx context.Context, tx *sql.Tx, item string, limit int) (int, error) {
	if limit == 0 {
		return deleteEmptyTitleMembersTx(ctx, tx, item, 0)
	}
	var ref compactcatalog.MutationRef
	err := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind='title' AND owner_id=? AND parent_mutation_id IS NULL AND status<>'terminal' ORDER BY mutation_id LIMIT 1`, item).Scan(&ref.ID, &ref.Generation)
	if err == nil {
		step, e := RemoveTitleStepTx(ctx, tx, ref)
		if e != nil {
			return 0, e
		}
		if step.More {
			return 1, nil
		}
		if e = acknowledgeMutationTx(ctx, tx, ref); e != nil {
			return 0, e
		}
		if step.Outcome == "stale" {
			return -1, nil
		}
		return 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var entity int64
	var playable bool
	err = tx.QueryRowContext(ctx, `SELECT e.id,(k.playable=1 OR k.id IN(20,21)) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, item).Scan(&entity, &playable)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	mode := "members"
	if playable {
		mode = "remove"
	}
	// Each new command gets its own owner generation; retries use the active
	// root above. A restored public identity never replays a terminal deletion.
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(owner_generation),0)+1 FROM catalog_entity_mutations WHERE owner_kind='title' AND owner_id=?`, item).Scan(&generation); err != nil {
		return 0, err
	}
	_, err = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: entity, OwnerKind: "title", OwnerID: item, OwnerGeneration: generation, Mode: mode})
	return 1, err
}

func deleteEmptyTitleMembersTx(ctx context.Context, tx *sql.Tx, item string, limit int) (int, error) {
	var entity int64
	var kind compactcatalog.Kind
	err := tx.QueryRowContext(ctx, `SELECT id,kind FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&entity, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	members, err := hasTitleMembersTx(ctx, tx, entity, kind)
	if err != nil {
		return 0, err
	}
	if members {
		return 1, nil
	}
	return 0, settleRemovedMembersTx(ctx, tx, entity, kind)
}

// removeTitleBatch is how many members one removal transaction deletes.
const removeTitleBatch = 12

// titleRemovalPlan bounds one removal batch. Removing even one playable item
// takes its derived rows (browse, sort, search and listening projections)
// with it through triggers: hundreds of rows for a single song.
var titleRemovalPlan = dbwork.WritePlan{Name: "title-removal", Rows: 8192, Pages: 2048, VMOps: 2_000_000, Why: "an item's delete cascades through its derived rows by trigger; child-first removal of that fan-out is writer-discipline item H (parent cascades)"}

// RemoveTitle removes a title (and everything that plays beneath it) from the
// catalogue in short transactions. Each profile's state is kept under the
// title's identities. Files on disk are not touched: the owner's delete route
// removes those first.
func (s *Service) RemoveTitle(ctx context.Context, item string) error {
	ctx = MutationWriteContext(ctx)
	for {
		remaining := 0
		err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
			var e error
			remaining, e = DeleteTitleMembersTx(ctx, tx, item, removeTitleBatch)
			return e
		})
		if remaining < 0 && err == nil {
			return compactcatalog.ErrMutationStale
		}
		if err != nil || remaining == 0 {
			return err
		}
		dbwork.Yield(ctx)
	}
}
