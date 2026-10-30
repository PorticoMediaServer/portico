package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type genreValueRef struct{ Base, Value int64 }
type genreArtHead struct {
	Ref   genreArtRef
	Valid bool
}
type genreArtMerge struct {
	refs       []genreValueRef
	heads      []genreArtHead
	role, init int
	result     []genreArtRef
}

func genreValueRefs(ctx context.Context, db *sql.DB, bases []FacetBaseSnapshot, row genreRow) ([]genreValueRef, error) {
	refs := make([]genreValueRef, 0, len(bases))
	for _, s := range bases {
		var id int64
		err := dbwork.QueryRow(ctx, db, `SELECT v.id FROM catalog_facet_base_values v JOIN catalog_facet_base_rows b ON b.base_id=? AND b.value_id=v.id WHERE v.library_id=? AND v.field='genre' AND v.value=(SELECT value FROM catalog_facet_base_values WHERE id=?)`, s.ID, s.Scope.LibraryID, row.Value).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		refs = append(refs, genreValueRef{s.ID, id})
	}
	return refs, nil
}
func (g *genreArtMerge) head(ctx context.Context, db *sql.DB, i int, after int64) error {
	ref := g.refs[i]
	var out genreArtRef
	err := dbwork.QueryRow(ctx, db, `SELECT c.position,c.entity_id,t.rating FROM catalog_search_genre_candidates c JOIN catalog_search_genre_targets t ON t.base_id=c.base_id AND t.entity_id=c.entity_id WHERE c.base_id=? AND c.value_id=? AND c.role=? AND c.position>? ORDER BY c.position LIMIT 1`, ref.Base, ref.Value, g.role, after).Scan(&out.Position, &out.Entity, &out.Rating)
	if errors.Is(err, sql.ErrNoRows) {
		g.heads[i] = genreArtHead{}
		return nil
	}
	if err != nil {
		return err
	}
	out.Base, out.Value, out.Role = ref.Base, ref.Value, g.role
	g.heads[i] = genreArtHead{Ref: out, Valid: true}
	return nil
}
func genreArtBetter(ctx context.Context, db *sql.DB, a, b genreArtRef) (bool, error) {
	if a.Rating != b.Rating {
		return a.Rating > b.Rating, nil
	}
	var less bool
	err := dbwork.QueryRow(ctx, db, `SELECT (a.sort_key,a.entity_id)<(b.sort_key,b.entity_id) FROM catalog_search_genre_targets a JOIN catalog_search_genre_targets b ON b.base_id=? AND b.entity_id=? WHERE a.base_id=? AND a.entity_id=?`, b.Base, b.Entity, a.Base, a.Entity).Scan(&less)
	if errors.Is(err, sql.ErrNoRows) {
		return false, readprep.ErrStale
	}
	return less, err
}
func (g *genreArtMerge) step(ctx context.Context, db *sql.DB) (bool, error) {
	started := time.Now()
	for g.init < len(g.refs) {
		if err := g.head(ctx, db, g.init, 0); err != nil {
			return false, err
		}
		g.init++
		if time.Since(started) >= 8*time.Millisecond {
			return false, nil
		}
	}
	for len(g.result) < 100 {
		best := -1
		for i, h := range g.heads {
			if !h.Valid {
				continue
			}
			if best < 0 {
				best = i
				continue
			}
			better, err := genreArtBetter(ctx, db, h.Ref, g.heads[best].Ref)
			if err != nil {
				return false, err
			}
			if better {
				best = i
			}
		}
		if best < 0 {
			return true, nil
		}
		chosen := g.heads[best].Ref
		g.result = append(g.result, chosen)
		if err := g.head(ctx, db, best, chosen.Position); err != nil {
			return false, err
		}
		if time.Since(started) >= 8*time.Millisecond {
			return false, nil
		}
	}
	return true, nil
}
func chooseGenreArt(candidates []genreArtRef, used map[int64]bool, maximum int, reserve bool) []genreArtRef {
	out := make([]genreArtRef, 0, maximum)
	for _, unusedOnly := range []bool{true, false} {
		for _, candidate := range candidates {
			if len(out) == maximum {
				break
			}
			selected := false
			for _, s := range out {
				selected = selected || s.Entity == candidate.Entity
			}
			if selected || unusedOnly && used[candidate.Entity] {
				continue
			}
			out = append(out, candidate)
			if reserve {
				used[candidate.Entity] = true
			}
		}
	}
	return out
}
func (c *genreComputation) prepareArt(ctx context.Context) (bool, error) {
	for c.artIndex < len(c.rows) {
		if c.artRole == 0 {
			c.artRole = 1
		}
		if c.art == nil {
			refs, err := genreValueRefs(ctx, c.runtime.db, c.bases, c.rows[c.artIndex])
			if err != nil {
				return false, err
			}
			c.art = &genreArtMerge{refs: refs, heads: make([]genreArtHead, len(refs)), role: c.artRole, result: make([]genreArtRef, 0, 100)}
		}
		done, err := c.art.step(ctx, c.runtime.db)
		if err != nil {
			return false, err
		}
		if !done {
			return false, nil
		}
		if c.artRole == 1 {
			chosen := chooseGenreArt(c.art.result, c.usedBackdrops, 1, true)
			if len(chosen) > 0 {
				v := chosen[0]
				c.rows[c.artIndex].Backdrop = &v
			}
			c.artRole = 2
		} else {
			c.rows[c.artIndex].Covers = chooseGenreArt(c.art.result, c.usedCovers, 2, len(c.art.result) >= 2)
			c.artIndex++
			c.artRole = 1
		}
		c.art = nil
		// One completed role yields too, so a 50-tile page cannot monopolize a turn.
		return c.artIndex == len(c.rows), nil
	}
	return true, nil
}
