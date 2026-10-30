package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// These permanent domains are source maintenance, never query jobs. A source
// write queues one key; its dependants are visited later under the writer gate.
const (
	DomainFacetTermFanout           = 36
	DomainFacetAssetFanout          = 37
	DomainFacetAttributeTermFanout  = 38
	DomainFacetAttributeFieldFanout = 39
	DomainFacetShowGenreFanout      = 40
	facetSourceQuantum              = 64
)

// The WITHOUT ROWID secondary index appends the remaining PK columns
// (provider,source_id). Retain the complete raw key: many source rows can belong
// to one holder. Do not DISTINCT the holder or filter overrides before LIMIT.
const facetTermFanoutSQL = `SELECT entity_id,provider,source_id
 FROM catalog_term_sources INDEXED BY catalog_term_sources_term
 WHERE term_id=? AND (entity_id,provider,source_id)>(?,?,?)
 ORDER BY entity_id,provider,source_id LIMIT ?`

const facetAssetFanoutSQL = `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset
 WHERE asset_id=? AND entity_id>? ORDER BY entity_id LIMIT ?`

// drainFacetSource advances at most limit raw edges across the ENTIRE call,
// rather than limit edges per source key. The dirty revision fences the cursor.
// Cursor writes and accepted downstream markers share the caller's transaction;
// rollback cannot skip an edge, and a source re-touch restarts from the beginning.
// Insertions/deletions/moves of edges independently queue both affected holders.
// That covers edits behind an in-progress cursor as well as source ID reuse.
func drainFacetSource(ctx context.Context, tx *sql.Tx, domain int, keys []Key, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	remaining := min(limit, facetSourceQuantum)
	if domain == DomainFacetShowGenreFanout {
		remaining = min(remaining, upstreamQuantum)
	}
	scalarBytes := 0
	done := make([]int64, 0, min(len(keys), remaining))
	for _, key := range keys {
		if remaining == 0 {
			break
		}
		var revision, entity int64
		var hasAfter bool
		var provider, source string
		err := tx.QueryRowContext(ctx, `SELECT revision,has_after,after_entity,after_provider,after_source
 FROM catalog_facet_source_fanouts WHERE domain=? AND source_id=?`, domain, key.ID).Scan(&revision, &hasAfter, &entity, &provider, &source)
		if errors.Is(err, sql.ErrNoRows) || err == nil && revision != key.Revision {
			hasAfter = false
			entity, provider, source = 0, "", ""
			_, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_source_fanouts(domain,source_id,revision) VALUES(?,?,?)
 ON CONFLICT(domain,source_id) DO UPDATE SET revision=excluded.revision,has_after=0,after_entity=0,after_provider='',after_source=''`, domain, key.ID, key.Revision)
		}
		if err != nil {
			return nil, err
		}
		if domain == DomainFacetShowGenreFanout {
			var number int64
			if source != "" {
				number, err = strconv.ParseInt(source, 10, 64)
				if err != nil {
					return nil, err
				}
			}
			edges, err := queryShowEpisodeEdges(ctx, tx, key.ID, hasAfter, entity, number, provider, remaining)
			if err != nil {
				return nil, err
			}
			ids := make([]int64, 0, len(edges))
			for _, edge := range edges {
				ids = append(ids, edge.id)
			}
			if err = TouchTx(ctx, tx, DomainFacetCounts, ids...); err != nil {
				return nil, err
			}
			if len(edges) == 0 {
				remaining -= max(1, len(edges))
				if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_source_fanouts WHERE domain=? AND source_id=? AND revision=?`, domain, key.ID, key.Revision); err != nil {
					return nil, err
				}
				done = append(done, key.ID)
			} else {
				remaining -= len(edges)
				last := edges[len(edges)-1]
				if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_source_fanouts SET has_after=1,after_entity=?,after_provider=?,after_source=? WHERE domain=? AND source_id=? AND revision=?`, last.id, last.key, strconv.FormatInt(last.aux, 10), domain, key.ID, key.Revision); err != nil {
					return nil, err
				}
			}
			continue
		}
		var rows *sql.Rows
		if !hasAfter {
			if domain == DomainFacetAttributeFieldFanout {
				rows, err = tx.QueryContext(ctx, `SELECT id,value_key FROM catalog_attribute_terms WHERE field_id=? ORDER BY value_key,id LIMIT ?`, key.ID, remaining)
			} else if domain == DomainFacetAttributeTermFanout {
				rows, err = tx.QueryContext(ctx, `SELECT item_id FROM catalog_item_attribute_edges INDEXED BY catalog_item_attribute_edges_term WHERE term_id=? ORDER BY item_id LIMIT ?`, key.ID, remaining)
			} else if domain == DomainFacetTermFanout {
				rows, err = tx.QueryContext(ctx, `SELECT entity_id,provider,source_id FROM catalog_term_sources INDEXED BY catalog_term_sources_term WHERE term_id=? ORDER BY entity_id,provider,source_id LIMIT ?`, key.ID, remaining)
			} else {
				rows, err = tx.QueryContext(ctx, `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? ORDER BY entity_id LIMIT ?`, key.ID, remaining)
			}
		} else if domain == DomainFacetAttributeFieldFanout {
			rows, err = tx.QueryContext(ctx, `SELECT id,value_key FROM catalog_attribute_terms WHERE field_id=? AND (value_key,id)>(?,?) ORDER BY value_key,id LIMIT ?`, key.ID, provider, entity, remaining)
		} else if domain == DomainFacetAttributeTermFanout {
			rows, err = tx.QueryContext(ctx, `SELECT item_id FROM catalog_item_attribute_edges INDEXED BY catalog_item_attribute_edges_term WHERE term_id=? AND item_id>? ORDER BY item_id LIMIT ?`, key.ID, entity, remaining)
		} else if domain == DomainFacetTermFanout {
			rows, err = tx.QueryContext(ctx, facetTermFanoutSQL, key.ID, entity, provider, source, remaining)
		} else {
			rows, err = tx.QueryContext(ctx, facetAssetFanoutSQL, key.ID, entity, remaining)
		}
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, remaining)
		exhausted := true
		for rows.Next() {
			nextEntity, nextProvider, nextSource := entity, provider, source
			if domain == DomainFacetAttributeFieldFanout {
				err = rows.Scan(&nextEntity, &nextProvider)
			} else if domain == DomainFacetTermFanout {
				err = rows.Scan(&nextEntity, &nextProvider, &nextSource)
			} else {
				err = rows.Scan(&nextEntity)
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			width := len(nextProvider) + len(nextSource)
			if scalarBytes > 0 && scalarBytes+width > facetScalarBudget {
				exhausted = false
				break
			}
			entity, provider, source = nextEntity, nextProvider, nextSource
			ids = append(ids, entity)
			scalarBytes += width
			// One legal oversized raw PK is accepted on its own. Every overridden
			// or rejected semantic row still advances the complete raw cursor.
			if scalarBytes >= facetScalarBudget {
				exhausted = false
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		target := DomainFacetCounts
		if domain == DomainFacetAttributeFieldFanout {
			target = DomainFacetAttributeTermFanout
		}
		if err = TouchTx(ctx, tx, target, ids...); err != nil {
			return nil, err
		}
		if domain == DomainFacetTermFanout {
			for _, id := range ids {
				if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_dirty(domain,entity_id,revision)
     SELECT 40,?1,1 WHERE EXISTS(SELECT 1 FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=?1)
     ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`, id); err != nil {
					return nil, err
				}
			}
		}
		if exhausted && len(ids) < remaining {
			// Empty sources still consume one unit, so zero-edge keys cannot
			// turn this into an unbounded checkpoint/acknowledgement batch.
			remaining -= max(1, len(ids))
			if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_source_fanouts WHERE domain=? AND source_id=? AND revision=?`, domain, key.ID, key.Revision); err != nil {
				return nil, err
			}
			done = append(done, key.ID)
		} else if len(ids) > 0 {
			remaining -= len(ids)
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_source_fanouts SET has_after=1,after_entity=?,after_provider=?,after_source=?
 WHERE domain=? AND source_id=? AND revision=?`, entity, provider, source, domain, key.ID, key.Revision); err != nil {
				return nil, err
			}
		}
		if !exhausted {
			break
		}
	}
	return done, nil
}

func init() {
	for _, domain := range []int{DomainFacetTermFanout, DomainFacetAssetFanout, DomainFacetAttributeTermFanout, DomainFacetAttributeFieldFanout, DomainFacetShowGenreFanout} {
		Register(domain, Derivation{
			Version: 1,
			Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
				return drainFacetSource(ctx, tx, domain, keys, limit)
			},
			Backfill: func(context.Context, *sql.Tx, int64, int) ([]int64, error) {
				// Domain33 rebuilds holder facts, including previous owners.
				return nil, nil
			},
		})
	}
}
