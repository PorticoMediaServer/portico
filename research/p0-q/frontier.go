package main

import (
	"container/heap"
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
)

// The frontier keeps one integer head per independent source, not its expanded
// entity population and not one open SQL cursor per source. Reads close at once.
type contextStream struct {
	owner, id int
	relation  bool
}
type contextHeap []contextStream

func (h contextHeap) Len() int           { return len(h) }
func (h contextHeap) Less(i, j int) bool { return h[i].id < h[j].id }
func (h contextHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *contextHeap) Push(x any)        { *h = append(*h, x.(contextStream)) }
func (h *contextHeap) Pop() any          { a := *h; n := len(a); x := a[n-1]; *h = a[:n-1]; return x }
func lazyTitleMatch(term string, tier int) string {
	s := prefixMatch(term, tier == 2)
	if tier == 1 {
		s += " NOT initial_prefix_tokens:" + encode(term) + "*"
	}
	return s
}
func (l *lazySearch) initFrontier(ctx context.Context, after int) error {
	if l.frontierReady {
		return nil
	}
	owners := []int{}
	last := -1
	match := encode(l.term) + "*"
	if strings.ContainsFunc(l.term, facettext.CJK) {
		match = encode(cjkSeed(l.term))
	}
	for {
		n := 0
		e := l.reader.read(ctx, `SELECT z.rowid,f.normalized FROM search_fragment_prefix z CROSS JOIN search_fragments f ON f.id=z.rowid WHERE search_fragment_prefix MATCH ? AND z.rowid>? ORDER BY z.rowid LIMIT 64`, []any{match, last}, func(r *sql.Rows) error {
			var id int
			var fold string
			if e := r.Scan(&id, &fold); e != nil {
				return e
			}
			last = id
			n++
			if ownTier(fold, l.term) >= 0 {
				owners = append(owners, id)
			}
			return nil
		})
		if e != nil {
			return e
		}
		if len(owners) > 8192 {
			return fmt.Errorf("context frontier exceeds8192 independent sources; capacity failure")
		}
		if n < 64 {
			break
		}
	}
	addHeads := func(ids []int, relation bool) error {
		table, key, value := "edges", "fragment", "entity"
		if relation {
			table, key, value = "rel", "parent", "child"
		}
		for start := 0; start < len(ids); start += 64 {
			end := min(start+64, len(ids))
			q := `SELECT j.value,(SELECT ` + value + ` FROM ` + table + ` WHERE ` + key + `=j.value AND ` + value + `>? ORDER BY ` + value + ` LIMIT 1) FROM json_each(?) j`
			e := l.reader.read(ctx, q, []any{after, encodedJSON(ids[start:end])}, func(r *sql.Rows) error {
				var owner int
				var id sql.NullInt64
				if e := r.Scan(&owner, &id); e != nil {
					return e
				}
				if id.Valid {
					heap.Push(&l.streams, contextStream{owner, int(id.Int64), relation})
				}
				return nil
			})
			if e != nil {
				return e
			}
		}
		return nil
	}
	if e := addHeads(owners, false); e != nil {
		return e
	}
	parents := []int{}
	seen := map[int]bool{}
	artists := []int{}
	for id, kind := range l.roots {
		parents = append(parents, id)
		seen[id] = true
		if kind == 5 {
			artists = append(artists, id)
		}
	}
	// Artist title inheritance has a second direct level (album -> song). Only
	// album source identities are added, never the song/entity match population.
	for start := 0; start < len(artists); start += 64 {
		end := min(start+64, len(artists))
		lastParent, lastChild := -1, -1
		for {
			n := 0
			e := l.reader.read(ctx, `SELECT r.parent,r.child FROM rel r CROSS JOIN catalog_entities e ON e.id=r.child WHERE r.parent IN(SELECT value FROM json_each(?)) AND (r.parent,r.child)>(?,?) AND e.kind=6 AND e.retired=0 ORDER BY r.parent,r.child LIMIT 64`, []any{encodedJSON(artists[start:end]), lastParent, lastChild}, func(r *sql.Rows) error {
				var p, id int
				if e := r.Scan(&p, &id); e != nil {
					return e
				}
				lastParent, lastChild = p, id
				n++
				if !seen[id] {
					parents = append(parents, id)
					seen[id] = true
				}
				return nil
			})
			if e != nil {
				return e
			}
			if len(parents)+len(owners) > 8192 {
				return fmt.Errorf("hierarchy frontier exceeds8192 source identities; capacity failure")
			}
			if n < 64 {
				break
			}
		}
	}
	if e := addHeads(parents, true); e != nil {
		return e
	}
	if len(l.streams) > 8192 {
		return fmt.Errorf("frontier capacity exceeded")
	}
	l.frontierPeak = len(l.streams)
	l.frontierReady = true
	return nil
}
func (l *lazySearch) contextNext(ctx context.Context, remaining int) (int, bool, error) {
	scanned := 0
	for len(l.streams) > 0 {
		if scanned >= remaining {
			return 0, false, fmt.Errorf("context page exceeds4096 candidate budget")
		}
		scanned++
		if e := ctx.Err(); e != nil {
			return 0, false, e
		}
		id := l.streams[0].id
		for len(l.streams) > 0 && l.streams[0].id == id {
			s := heap.Pop(&l.streams).(contextStream)
			table, key, value := "edges", "fragment", "entity"
			if s.relation {
				table, key, value = "rel", "parent", "child"
			}
			q := `SELECT ` + value + ` FROM ` + table + ` WHERE ` + key + `=? AND ` + value + `>? ORDER BY ` + value + ` LIMIT 1`
			e := l.reader.read(ctx, q, []any{s.owner, id}, func(r *sql.Rows) error {
				var next int
				if e := r.Scan(&next); e != nil {
					return e
				}
				s.id = next
				heap.Push(&l.streams, s)
				return nil
			})
			if e != nil {
				return 0, false, e
			}
		}
		eligible := false
		e := l.reader.read(ctx, `SELECT title_fold,retired FROM catalog_entities WHERE id=?`, []any{id}, func(r *sql.Rows) error {
			var fold string
			var retired int
			if e := r.Scan(&fold, &retired); e != nil {
				return e
			}
			eligible = retired == 0 && ownTier(fold, l.term) < 0 && (!l.restricted || id%100 == 0)
			return nil
		})
		if e != nil {
			return 0, false, e
		}
		l.candidates++
		if eligible {
			return id, false, nil
		}
	}
	return 0, true, nil
}
