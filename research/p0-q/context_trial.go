package main

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
)

// Try a bounded entity window before opening the subject frontier. Dense terms
// can return a ranked page without enumerating every matching source. A sparse
// window falls through to the exact source merge AFTER every examined entity.
func (l *lazySearch) contextTrial(ctx context.Context, after, need, budget int) ([]int, int, int, bool, error) {
	out := []int{}
	scanned := 0
	last := after
	for scanned < min(512, budget) {
		type candidate struct {
			id      int
			fold    string
			retired int
		}
		batch := []candidate{}
		err := l.reader.read(ctx, `WITH c AS MATERIALIZED(SELECT id FROM q_items WHERE id>? ORDER BY id LIMIT 64) SELECT c.id,e.title_fold,e.retired FROM c CROSS JOIN catalog_entities e ON e.id=c.id ORDER BY c.id`, []any{last}, func(r *sql.Rows) error {
			var c candidate
			if e := r.Scan(&c.id, &c.fold, &c.retired); e != nil {
				return e
			}
			batch = append(batch, c)
			return nil
		})
		if err != nil {
			return nil, last, scanned, false, err
		}
		if len(batch) == 0 {
			return out, last, scanned, true, nil
		}
		ids := []int{}
		for _, c := range batch {
			if c.retired == 0 && ownTier(c.fold, l.term) < 0 && (!l.restricted || c.id%100 == 0) {
				ids = append(ids, c.id)
			}
		}
		matched := map[int]bool{}
		if len(ids) > 0 {
			match := encode(l.term) + "*"
			if strings.ContainsFunc(l.term, facettext.CJK) {
				match = encode(cjkSeed(l.term))
			}
			for _, kind := range []string{"person", "entity"} {
				owner := "owner_" + kind
				key := kind + "_id"
				query := `SELECT x.entity,f.normalized FROM q_edges x INDEXED BY q_edge_target CROSS JOIN search_publish_jobs j ON j.` + key + `=x.` + owner + ` AND j.search_generation_id=(SELECT active_generation_id FROM search_revision WHERE singleton=1) CROSS JOIN search_fragments f ON f.` + owner + `=x.` + owner + ` AND f.search_generation_id=j.search_generation_id AND f.generation=j.published_generation CROSS JOIN search_fragment_prefix z ON z.rowid=f.id WHERE x.entity IN(SELECT value FROM json_each(?)) AND x.` + owner + ` IS NOT NULL AND f.` + owner + ` IS NOT NULL AND search_fragment_prefix MATCH ?`
				err = l.reader.read(ctx, query, []any{encodedJSON(ids), match}, func(r *sql.Rows) error {
					var id int
					var fold string
					if e := r.Scan(&id, &fold); e != nil {
						return e
					}
					if ownTier(fold, l.term) >= 0 {
						matched[id] = true
					}
					return nil
				})
				if err != nil {
					return nil, last, scanned, false, err
				}
			}
			query := `SELECT r.child,e.title_fold,e.retired FROM rel r CROSS JOIN catalog_entities e ON e.id=r.parent WHERE r.child IN(SELECT value FROM json_each(?)) UNION ALL SELECT r.child,e.title_fold,e.retired FROM rel r CROSS JOIN rel p ON p.child=r.parent CROSS JOIN catalog_entities e ON e.id=p.parent WHERE r.child IN(SELECT value FROM json_each(?)) AND e.kind=5`
			err = l.reader.read(ctx, query, []any{encodedJSON(ids), encodedJSON(ids)}, func(r *sql.Rows) error {
				var id, retired int
				var fold string
				if e := r.Scan(&id, &fold, &retired); e != nil {
					return e
				}
				if retired == 0 && ownTier(fold, l.term) >= 0 {
					matched[id] = true
				}
				return nil
			})
			if err != nil {
				return nil, last, scanned, false, err
			}
		}
		for _, c := range batch {
			if scanned >= budget {
				return out, last, scanned, false, nil
			}
			last = c.id
			scanned++
			l.candidates++
			l.trialCandidates++
			if matched[c.id] {
				out = append(out, c.id)
				if len(out) == need {
					return out, last, scanned, false, nil
				}
			}
		}
		if len(batch) < 64 {
			return out, last, scanned, true, nil
		}
	}
	return out, last, scanned, false, nil
}
