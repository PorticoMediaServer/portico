package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type orderedArtworkCarrier struct {
	sidecarCarrier
	name string
	rank int64
}

func artworkCarrierBefore(a, b orderedArtworkCarrier) bool {
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	if a.name != b.name {
		return a.name < b.name
	}
	return a.key < b.key
}

func (s *Service) commitInventoryArtworkCandidatesTx(ctx context.Context, tx *sql.Tx, source LibrarySource, object, revision, asset, kind string, candidates []assets.LocalArtworkCandidate) error {
	if tx == nil || object == "" || revision == "" || asset == "" || (kind != "movie" && kind != "tv" && kind != "anime") {
		return ErrAdminQuery
	}
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id JOIN libraries l ON l.id=s.library_id
 WHERE o.id=? AND o.source_id=? AND o.revision=? AND o.asset_id=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason=''
 AND s.library_id=? AND l.kind=? AND s.enabled=1 AND s.generation=? AND s.incarnation=? AND o.root_incarnation=s.incarnation
 AND s.root_identity=? AND s.health NOT IN('offline','removing','root_changed'))`, object, source.ID, revision, asset, source.LibraryID, kind, source.Generation, source.Incarnation, source.RootIdentity).Scan(&current)
	if err != nil {
		return err
	}
	if !current {
		return storage.ErrInventoryChanged
	}
	byCarrier := map[sidecarCarrier]orderedArtworkCarrier{}
	for _, candidate := range candidates {
		raw, err := hex.DecodeString(candidate.Key)
		if err != nil || len(raw) != 32 {
			return errors.New("invalid local artwork candidate key")
		}
		if !validVideoArtworkCandidateSlot(kind, candidate.Slot) {
			return errors.New("invalid local artwork candidate slot")
		}
		name, rank, err := assets.NormalizeLocalArtworkSource(candidate.SourceName, candidate.SourceRank)
		if err != nil {
			return err
		}
		err = s.visitAssetSidecars(ctx, tx, source.LibraryID, kind, asset, map[string]string{candidate.Slot: candidate.Key}, func(_ context.Context, _ *sql.Tx, _ string, found []sidecarCarrier) error {
			for _, c := range found {
				value := orderedArtworkCarrier{c, name, rank}
				old, exists := byCarrier[c]
				if !exists || artworkCarrierBefore(value, old) {
					byCarrier[c] = value
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		// Unmatched episodes have no show/season link yet. Keep their media
		// facts and omit the unattached image; never invent a parent or fail
		// the whole import merely because the filename did not match.
	}
	carriers := make([]orderedArtworkCarrier, 0, len(byCarrier))
	for _, c := range byCarrier {
		carriers = append(carriers, c)
	}
	// Candidate-only changes have no local_artwork carrier trigger. Notify the
	// affected old and new heads in this same transaction, including removals
	// and reassociations; leave unchanged accepted observations quiet.
	pending := make(map[orderedArtworkCarrier]bool, len(carriers))
	for _, c := range carriers {
		pending[c] = true
	}
	type head struct {
		kind   string
		entity int64
	}
	dirty := map[head]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank FROM inventory_artwork_candidates WHERE object_id=?`, object)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c orderedArtworkCarrier
		var oldRevision, oldAsset string
		if err = rows.Scan(&oldRevision, &oldAsset, &c.kind, &c.entity, &c.role, &c.key, &c.name, &c.rank); err != nil {
			rows.Close()
			return err
		}
		if pending[c] && oldRevision == revision && oldAsset == asset {
			delete(pending, c)
		} else {
			dirty[head{c.kind, c.entity}] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for c := range pending {
		dirty[head{c.kind, c.entity}] = true
	}
	if len(dirty) == 0 {
		return nil
	}
	// Only a complete image observation reaches this method. The transaction's
	// current inventory/policy fence remains the authority for replacement.
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_artwork_candidates WHERE object_id=?`, object); err != nil {
		return err
	}
	for _, c := range carriers {
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES(?,?,?,?,?,?,?,?,?)`, object, revision, asset, c.kind, c.entity, c.role, c.key, c.name, c.rank); err != nil {
			return err
		}
	}
	heads := make([]head, 0, len(dirty))
	for h := range dirty {
		heads = append(heads, h)
	}
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].kind != heads[j].kind {
			return heads[i].kind < heads[j].kind
		}
		return heads[i].entity < heads[j].entity
	})
	for _, h := range heads {
		if err = markSidecarArtworkDirtyTx(ctx, tx, h.kind, h.entity); err != nil {
			return err
		}
	}
	return nil
}

// ReadLocalArtworkCandidatesTx reads one bounded page of accepted image keys
// in the caller's authorized transaction. It supplies no access or selection
// authority. A changed/retired source object cannot contribute stale choices.
func ReadLocalArtworkCandidatesTx(ctx context.Context, tx *sql.Tx, kind string, entity int64, role, after string, limit int) ([]string, error) {
	if tx == nil || entity <= 0 || limit < 1 || limit > 128 || (kind != "item" && kind != "show" && kind != "season") || (role != "poster" && role != "backdrop" && role != "logo") {
		return nil, ErrAdminQuery
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT c.art_key FROM inventory_artwork_candidates c INDEXED BY inventory_artwork_candidates_entity
 CROSS JOIN inventory_objects o ON o.id=c.object_id AND o.revision=c.source_revision AND o.asset_id=c.asset_id
 CROSS JOIN library_sources s ON s.id=o.source_id
 WHERE c.entity_id=? AND c.kind=? AND c.role=? AND c.art_key>?
 AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.root_incarnation=s.incarnation
 AND s.enabled=1 AND s.health NOT IN('offline','removing','root_changed')
 AND (
 (c.kind='item' AND EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id WHERE a.token=c.asset_id AND l.entity_id=c.entity_id)) OR
 (c.kind='show' AND EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_episodes ep ON ep.entity_id=l.entity_id WHERE a.token=c.asset_id AND ep.show_id=c.entity_id)) OR
 (c.kind='season' AND EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_episodes ep ON ep.entity_id=l.entity_id JOIN catalog_seasons se ON se.show_id=ep.show_id WHERE a.token=c.asset_id AND se.entity_id=c.entity_id))
 )
 ORDER BY c.art_key LIMIT ?`, entity, kind, role, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func validVideoArtworkCandidateSlot(kind, slot string) bool {
	if kind == "movie" {
		return slot == "item/poster" || slot == "item/backdrop" || slot == "item/logo"
	}
	if kind != "tv" && kind != "anime" {
		return false
	}
	if slot == "show/poster" || slot == "show/backdrop" || slot == "show/logo" {
		return true
	}
	_, ok := sidecarSeasonNumber(slot)
	return ok
}
