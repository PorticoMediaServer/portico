package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// Availability (domain 20) maintains the last accepted file evidence through
// inventory_item_availability. Real file changes, source configuration, trash
// and DVR retirement queue bounded work. Transient offline/online is only a
// source fact; playback checks it directly and it queues no item maintenance.

const (
	DomainLibraryAvailability = 28
	DomainAssetIssues         = 29
)

// availabilityStaleSQL queues (domain ?1) the items of ?2 whose browse row's
// availability or availability state differs from their facts.
const availabilityStaleSQL = `INSERT INTO catalog_dirty(domain,entity_id,revision)
 SELECT ?1,r.entity_id,1 FROM json_each(?2) j JOIN catalog_browse_rows r ON r.entity_id=j.value
 LEFT JOIN catalog_item_availability v ON v.entity_id=r.entity_id LEFT JOIN catalog_browse_member_links ml ON ml.entity_id=r.entity_id
 WHERE r.available IS NOT COALESCE(v.available,0)
  OR r.availability_state IS NOT CASE WHEN COALESCE(ml.available,0)<=0 THEN 'unavailable' WHEN ml.unavailable>0 THEN 'partial' ELSE 'available' END
 ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`

// deriveLibraryAvailability queues the availability of limit more items of a
// library; true when the library is done.
func deriveLibraryAvailability(ctx context.Context, tx *sql.Tx, k Key, limit int) (bool, error) {
	var after, revision int64
	var hasAfter bool
	err := tx.QueryRowContext(ctx, `SELECT after,revision,has_after FROM catalog_fanouts WHERE domain=? AND key=?`, DomainLibraryAvailability, k.ID).Scan(&after, &revision, &hasAfter)
	if errors.Is(err, sql.ErrNoRows) || err == nil && revision != k.Revision {
		after = 0
		hasAfter = false
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_fanouts(domain,key,revision,after) VALUES(?,?,?,0) ON CONFLICT(domain,key) DO UPDATE SET revision=excluded.revision,after=0,has_after=0`, DomainLibraryAvailability, k.ID, k.Revision)
	}
	if err != nil {
		return false, err
	}
	// Index order is (library,id), not (library,kind,id). Raw rejects consume
	// the quantum and its cursor; no kind filter/sort precedes LIMIT.
	q := `SELECT e.id,k.playable=1 OR k.id IN(20,21) FROM catalog_entities e INDEXED BY catalog_entities_library_id
 CROSS JOIN catalog_kinds k ON k.id=e.kind WHERE e.library_id=?`
	args := []any{k.ID}
	if hasAfter {
		q += ` AND e.id>?`
		args = append(args, after)
	}
	q += ` ORDER BY e.id LIMIT ?`
	args = append(args, min(limit, upstreamQuantum))
	edges, err := querySourceEdges(ctx, tx, 3, q, args...)
	if err != nil {
		return false, err
	}
	for _, edge := range edges {
		if edge.flag {
			if err = TouchTx(ctx, tx, DomainAvailability, edge.id); err != nil {
				return false, err
			}
		}
		after = edge.id
	}
	if len(edges) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_fanouts WHERE domain=? AND key=? AND revision=?`, DomainLibraryAvailability, k.ID, k.Revision)
		return true, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_fanouts SET after=?,has_after=1 WHERE domain=? AND key=? AND revision=?`, after, DomainLibraryAvailability, k.ID, k.Revision)
	return false, err
}

// deriveAssetIssues mirrors an audio file's source issue into the catalogue.
func deriveAssetIssues(ctx context.Context, tx *sql.Tx, ids string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_asset_issues WHERE asset_id IN(SELECT value FROM json_each(?))`, ids); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_asset_issues(library_id,asset_id,issue)
 SELECT cl.id,a.id,m.issue FROM json_each(?) j JOIN catalog_assets a ON a.id=j.value
 JOIN audio_source_metadata m ON m.asset_id=a.token JOIN catalog_libraries cl ON cl.library_id=m.library_id`, ids)
	return err
}

// deriveRelated keeps a movie's related-title facets: its genres (by provider
// id and by name) and its cast (by provider person id). CROSS JOIN fixes the
// join order to start from the batch: left to itself the planner starts from
// every term of the vocabulary and walks every entity that carries one, which
// grows with the library.
func deriveRelated(ctx context.Context, tx *sql.Tx, ids string) error {
	for _, q := range []string{
		`DELETE FROM catalog_related_facets WHERE entity_id IN(SELECT value FROM json_each(?1))`,
		`INSERT INTO catalog_related_facets(entity_id,library_id,relation,provider,facet_id,year)
 SELECT e.id,e.library_id,1,s.provider,s.source_id,e.year FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=j.value AND e.kind=1 AND e.retired=0
 CROSS JOIN catalog_term_sources s ON s.entity_id=e.id CROSS JOIN catalog_terms t ON t.id=s.term_id AND t.vocab=1
 ON CONFLICT(entity_id,relation,provider,facet_id) DO NOTHING`,
		`INSERT INTO catalog_related_facets(entity_id,library_id,relation,provider,facet_id,year)
 SELECT e.id,e.library_id,1,'name',t.key,e.year FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=j.value AND e.kind=1 AND e.retired=0
 CROSS JOIN catalog_entity_terms m ON m.entity_id=e.id CROSS JOIN catalog_terms t ON t.id=m.term_id AND t.vocab=1
 ON CONFLICT(entity_id,relation,provider,facet_id) DO NOTHING`,
		`INSERT INTO catalog_related_facets(entity_id,library_id,relation,provider,facet_id,year)
 SELECT e.id,e.library_id,2,c.provider,c.provider_person_id,e.year FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=j.value AND e.kind=1 AND e.retired=0
 CROSS JOIN catalog_credits c ON c.entity_id=e.id CROSS JOIN catalog_credit_labels d ON d.id=c.department_id
 WHERE c.provider_person_id<>'' AND d.label='Acting'
 ON CONFLICT(entity_id,relation,provider,facet_id) DO NOTHING`,
	} {
		if _, err := tx.ExecContext(ctx, q, ids); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	Register(DomainAvailability, Derivation{
		Version: 2,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			if len(keys) == 0 {
				return nil, nil
			}
			// One holder per turn: its raw cursor is the unit of work, and the
			// rest of the queue waits behind it rather than piling up a pass
			// per item in one transaction.
			done, _, err := deriveAvailabilityKey(ctx, tx, keys[0], limit)
			if err != nil || !done {
				return nil, err
			}
			return []int64{keys[0].ID}, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT e.id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE (k.playable=1 OR k.id IN(20,21)) AND e.id>? ORDER BY e.id LIMIT ?`, after, limit)
		},
	})
	Register(DomainLibraryAvailability, Derivation{
		Version: 3,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			var done []int64
			for _, k := range keys[:min(1, len(keys))] {
				finished, err := deriveLibraryAvailability(ctx, tx, k, limit)
				if err != nil {
					return nil, err
				}
				if finished {
					done = append(done, k.ID)
				}
			}
			return done, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return nil, nil // availability rebuilds from items
		},
	})
	Register(DomainAssetIssues, Derivation{
		Version: 1,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			ids := keyIDs(keys)
			return ids, deriveAssetIssues(ctx, tx, mustJSON(ids))
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_assets WHERE id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
	Register(DomainRelated, Derivation{
		Version: 1,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			ids := keyIDs(keys)
			return ids, deriveRelated(ctx, tx, mustJSON(ids))
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE kind=1 AND id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
}
