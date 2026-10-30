package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// Each membership source has its own indexed driver. In particular artists and
// collections are not unioned/sorted before LIMIT. Source is part of the durable
// continuation even when one item names the same container several ways.
var browseMemberSources = [...]string{
	`SELECT e.id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?1 AND (k.playable=1 OR k.id IN(20,21))`,
	`SELECT show_id FROM catalog_episodes WHERE entity_id=?1`,
	`SELECT season_id FROM catalog_episodes WHERE entity_id=?1 AND season_id IS NOT NULL`,
	`SELECT album_id FROM catalog_songs WHERE entity_id=?1`,
	`SELECT a.artist_id FROM catalog_songs s JOIN catalog_albums a ON a.entity_id=s.album_id WHERE s.entity_id=?1`,
	`SELECT artist_id FROM catalog_song_artists WHERE song_id=?1`,
	`SELECT book_id FROM catalog_book_files WHERE entity_id=?1`,
	`SELECT au.id FROM catalog_book_files f JOIN catalog_books b ON b.entity_id=f.book_id JOIN catalog_libraries cl ON cl.id=b.library_id JOIN catalog_identities i ON i.root=cl.root AND i.source_key=portico_author_key(b.author) JOIN catalog_entities au ON au.public_id=i.public_id WHERE f.entity_id=?1 AND b.author<>''`,
	`SELECT collection_id FROM catalog_collection_members INDEXED BY catalog_collection_members_item WHERE item_id=?1`,
}

func browseDesiredMemberPage(ctx context.Context, tx *sql.Tx, item int64, source int, after int64, limit int) ([]int64, error) {
	if source == 5 {
		return scanIDs(ctx, tx, browseMemberSources[source]+` AND artist_id>?2 ORDER BY artist_id LIMIT ?3`, item, after, limit)
	}
	if source == 8 {
		return scanIDs(ctx, tx, browseMemberSources[source]+` AND collection_id>?2 ORDER BY collection_id LIMIT ?3`, item, after, limit)
	}
	// All other sources are at most one row by their unique owner/identity keys.
	var id sql.NullInt64
	err := tx.QueryRowContext(ctx, browseMemberSources[source], item).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if id.Valid && id.Int64 > after {
		return []int64{id.Int64}, nil
	}
	return nil, nil
}

func deriveBrowseMembersBounded(ctx context.Context, tx *sql.Tx, k Key, limit int) (bool, error) {
	after, source, phase, err := metricCursor(ctx, tx, DomainBrowseEdges, k)
	if err != nil {
		return false, err
	}
	budget := max(1, min(limit, upstreamQuantum))
	for budget > 0 {
		if phase == 0 {
			rows, err := tx.QueryContext(ctx, `SELECT entity_id,source FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item WHERE item_id=? AND (entity_id,source)>(?,?) ORDER BY entity_id,source LIMIT ?`, k.ID, after, source, budget)
			if err != nil {
				return false, err
			}
			var edges []browseEdge
			for rows.Next() {
				var e browseEdge
				if err = rows.Scan(&e.entity, &e.source); err != nil {
					rows.Close()
					return false, err
				}
				edges = append(edges, e)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return false, err
			}
			for _, e := range edges {
				query := browseMemberSources[e.source]
				args := []any{k.ID}
				if e.source == 5 {
					query += ` AND artist_id=?2`
					args = append(args, e.entity)
				}
				if e.source == 8 {
					query += ` AND collection_id=?2`
					args = append(args, e.entity)
				}
				var id sql.NullInt64
				err = tx.QueryRowContext(ctx, query, args...).Scan(&id)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return false, err
				}
				if err != nil || !id.Valid || id.Int64 != e.entity {
					if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_browse_memberships WHERE entity_id=? AND item_id=? AND source=?`, e.entity, k.ID, e.source); err != nil {
						return false, err
					}
					if err = TouchTx(ctx, tx, DomainItemMetrics, k.ID); err != nil {
						return false, err
					}
					if e.entity != k.ID {
						if err = TouchTx(ctx, tx, DomainContainerRefresh, e.entity); err != nil {
							return false, err
						}
					}
				}
				after, source = e.entity, e.source
			}
			eof := len(edges) < budget
			budget -= max(1, len(edges))
			if eof {
				phase = 1
				source = 0
				after = 0
			}
		} else {
			if source < 0 {
				source = 0
			}
			if source >= len(browseMemberSources) {
				return true, finishMetricCursor(ctx, tx, DomainBrowseEdges, k.ID)
			}
			desired, err := browseDesiredMemberPage(ctx, tx, k.ID, source, after, budget)
			if err != nil {
				return false, err
			}
			for _, id := range desired {
				r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_browse_memberships(entity_id,item_id,source) SELECT id,?,? FROM catalog_entities WHERE id=?`, k.ID, source, id)
				if err != nil {
					return false, err
				}
				n, err := r.RowsAffected()
				if err != nil {
					return false, err
				}
				if n > 0 {
					if err = TouchTx(ctx, tx, DomainBrowseRows, id); err != nil {
						return false, err
					}
					if err = TouchTx(ctx, tx, DomainItemMetrics, k.ID); err != nil {
						return false, err
					}
					if id != k.ID {
						if err = TouchTx(ctx, tx, DomainContainerRefresh, id); err != nil {
							return false, err
						}
					}
				}
				after = id
			}
			eof := len(desired) < budget || (source != 5 && source != 8)
			budget -= max(1, len(desired))
			if eof {
				source++
				after = 0
			}
			if source >= len(browseMemberSources) {
				return true, finishMetricCursor(ctx, tx, DomainBrowseEdges, k.ID)
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_browse_metric_cursors SET after_entity=?,after_source=?,after_item=? WHERE domain=? AND entity_id=?`, after, source, phase, DomainBrowseEdges, k.ID)
	return false, err
}
