package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// LocalArtworkCandidateCursor is both an accepted order record and the exact
// private continuation tuple. It is not a public DTO or source-access grant.
type LocalArtworkCandidateCursor struct {
	SourceRank int64
	SourceName string
	Key        string
}

func validArtworkCursor(c LocalArtworkCandidateCursor) bool {
	name, rank, err := assets.NormalizeLocalArtworkSource(c.SourceName, c.SourceRank)
	if err != nil || name != c.SourceName || rank != c.SourceRank {
		return false
	}
	key, err := hex.DecodeString(c.Key)
	return err == nil && len(key) == 32
}

// ReadLocalArtworkCandidatePageTx retains the existing current-source and
// actual asset-to-entity association checks in the caller-authorized snapshot.
// DISTINCT collapses identical tuples from multiple actual carrier copies.
// The consumer's stable local:hash identity also coalesces identical image bytes
// retained under different accepted source names; no hash encodes their order.
func ReadLocalArtworkCandidatePageTx(ctx context.Context, tx *sql.Tx, kind string, entity int64, role string, after *LocalArtworkCandidateCursor, limit int) ([]LocalArtworkCandidateCursor, error) {
	return readLocalArtworkCandidatePageTx(ctx, tx, kind, entity, role, "", after, limit)
}

// ReadUnmaterializedLocalArtworkCandidatePageTx excludes only the exact local
// origin already materialized for this target/slot under the caller's current
// fence. The database supplies durable progress: start with after=nil on every
// committed worker pass, so restarts and late lower/middle arrivals are found.
// after is only for a sweep within the same captured transaction, not a cursor
// to retain across changing input. No historical-origin map or prefix walk is
// needed in the caller; the existing origin index serves the anti-lookup.
func ReadUnmaterializedLocalArtworkCandidatePageTx(ctx context.Context, tx *sql.Tx, kind string, entity int64, role, fence string, after *LocalArtworkCandidateCursor, limit int) ([]LocalArtworkCandidateCursor, error) {
	if fence == "" {
		return nil, ErrAdminQuery
	}
	return readLocalArtworkCandidatePageTx(ctx, tx, kind, entity, role, fence, after, limit)
}

func readLocalArtworkCandidatePageTx(ctx context.Context, tx *sql.Tx, kind string, entity int64, role, unmaterializedFence string, after *LocalArtworkCandidateCursor, limit int) ([]LocalArtworkCandidateCursor, error) {
	if tx == nil || entity <= 0 || limit < 1 || limit > 128 || (kind != "item" && kind != "show" && kind != "season") || (role != "poster" && role != "backdrop" && role != "logo") {
		return nil, ErrAdminQuery
	}
	lower := LocalArtworkCandidateCursor{SourceRank: -1}
	if after != nil {
		if !validArtworkCursor(*after) {
			return nil, ErrAdminQuery
		}
		lower = *after
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT c.source_rank,c.source_name,c.art_key
 FROM inventory_artwork_candidates c INDEXED BY inventory_artwork_candidates_order
 CROSS JOIN inventory_objects o ON o.id=c.object_id AND o.revision=c.source_revision AND o.asset_id=c.asset_id
 CROSS JOIN library_sources s ON s.id=o.source_id
 WHERE c.entity_id=? AND c.kind=? AND c.role=? AND (c.source_rank,c.source_name,c.art_key)>(?,?,?)
 AND (?7='' OR NOT EXISTS(SELECT 1 FROM artwork_candidates m INDEXED BY artwork_candidates_source
 WHERE m.provider='local' AND m.origin='local:'||c.art_key AND m.kind=c.kind AND m.entity_id=c.entity_id
 AND m.role=c.role AND m.subject='' AND m.source_fence=?7))
 AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.root_incarnation=s.incarnation
 AND s.enabled=1 AND s.health NOT IN('offline','removing','root_changed')
 AND (
 (c.kind='item' AND EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id WHERE a.token=c.asset_id AND l.entity_id=c.entity_id)) OR
 (c.kind='show' AND EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_episodes ep ON ep.entity_id=l.entity_id WHERE a.token=c.asset_id AND ep.show_id=c.entity_id)) OR
 (c.kind='season' AND EXISTS(SELECT 1 FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_episodes ep ON ep.entity_id=l.entity_id JOIN catalog_seasons se ON se.show_id=ep.show_id WHERE a.token=c.asset_id AND se.entity_id=c.entity_id))
 )
 ORDER BY c.source_rank,c.source_name,c.art_key LIMIT ?`, entity, kind, role, lower.SourceRank, lower.SourceName, lower.Key, unmaterializedFence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocalArtworkCandidateCursor{}
	for rows.Next() {
		var c LocalArtworkCandidateCursor
		if err = rows.Scan(&c.SourceRank, &c.SourceName, &c.Key); err != nil {
			return nil, err
		}
		if !validArtworkCursor(c) {
			return nil, ErrAdminQuery
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReadDefaultLocalArtworkCandidateTx supplies the ordered automatic default.
// Owner selections (including unlocked choices) and pending owner jobs win.
// Legacy rows without a basename supply choices, not a guessed named default;
// callers retain the existing primary-art fallback until acceptance adds facts.
// This reader never changes a selection or queues a rendering job.
func ReadDefaultLocalArtworkCandidateTx(ctx context.Context, tx *sql.Tx, kind string, entity int64, role string) (*LocalArtworkCandidateCursor, error) {
	page, err := ReadLocalArtworkCandidatePageTx(ctx, tx, kind, entity, role, nil, 1)
	if err != nil || len(page) == 0 {
		return nil, err
	}
	if page[0].SourceName == "" {
		return nil, nil
	}
	var ownerHeld bool
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject='' AND (locked=1 OR actor<>''))
 OR EXISTS(SELECT 1 FROM artwork_jobs WHERE kind=? AND entity_id=? AND role=? AND subject='' AND preview=0 AND (locked=1 OR actor<>'') AND status IN('pending','running','retry'))`, kind, entity, role, kind, entity, role).Scan(&ownerHeld)
	if err != nil || ownerHeld {
		return nil, err
	}
	return &page[0], nil
}
