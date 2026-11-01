package catalog

import (
	"encoding/json"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The newest-first walk answers a New on server page from the works at the top
// of catalog_browse_recent_works instead of ordering the whole library.
//
// A work's order value (ord) is its newest member addition that is available
// and visible to the viewer, so it is never above recent_text, the newest
// addition among all its members. Walking recent_text newest first, every work
// not yet reached has ord at most the next recent_text (B). A work found with
// ord above B is therefore final, and once a page's worth of works are final the
// page is exactly the one the full query would return. Editions are folded the
// way the full query folds them: on first sight of a work, every entity sharing
// one of its provider ids is evaluated with it, and the work keeps the smallest
// id and the newest ord.
//
// The upper bound holds only while the catalogue's recent order is settled
// (compactcatalog.RecentOrderSettled). Otherwise, past the dated works, past a
// deep offset, or past the walk budget, the caller runs the full query.

// homeWalkBudget bounds the entities one walk evaluates before it gives up.
const homeWalkBudget = 4096

// homeWalkBatch is how many walked entities are evaluated together (a
// variable so tests can make ties fall across batches).
var homeWalkBatch = 64

// homeWalkMaxOffset is the deepest ranged page the walk serves.
const homeWalkMaxOffset = 256

// homeWalkRequest is one page: skip groups, then take groups; with after set,
// only groups ordered after that (ord, id) key.
type homeWalkRequest struct {
	skip, take int
	after      *[2]string
}

// homeWalker returns up to take ids and their ords, and ok=false when the
// caller must run the full query instead.
type homeWalker func(homeWalkRequest) (ids, ords []string, ok bool, err error)

type homeWalkGroup struct{ id, ord string }

// homeRecentWalker walks r.Libraries for the row whose per-entity projection is
// `SELECT pid(i.public_id),<ord>,<key>,i.id <works>` with args (ordArgs,
// worksArgs…). Only a descending row is walked.
func (s *Service) homeRecentWalker(r HomeRequest, ord, key, works string, ordArgs, worksArgs []any) homeWalker {
	return func(want homeWalkRequest) ([]string, []string, bool, error) {
		if want.skip > homeWalkMaxOffset || want.take < 1 {
			return nil, nil, false, nil
		}
		settled, err := compactcatalog.RecentOrderSettled(s.Context(), s.read())
		if err != nil || !settled {
			return nil, nil, false, err
		}
		evaluate := `SELECT pid(i.public_id),` + ord + `,` + key + `,i.id` + works + ` AND br.entity_id IN(SELECT value FROM json_each(?))`
		heads := make([]*homeWalkHead, 0, len(r.Libraries))
		for _, library := range r.Libraries {
			heads = append(heads, &homeWalkHead{library: library, lastAdded: "￿", lastEntity: 1 << 62})
		}
		groups := map[string]*homeWalkGroup{}
		seen := map[int64]bool{}
		examined := 0
		need := want.skip + want.take
		for {
			batch := []int64{}
			for len(batch) < homeWalkBatch {
				head, err := s.homeWalkNext(heads)
				if err != nil {
					return nil, nil, false, err
				}
				if head == nil {
					break
				}
				row := head.rows[0]
				head.rows = head.rows[1:]
				if !seen[row.entity] {
					batch = append(batch, row.entity)
				}
			}
			if len(batch) > 0 {
				members, err := s.homeWalkEditions(batch, seen)
				if err != nil {
					return nil, nil, false, err
				}
				examined += len(members)
				if examined > homeWalkBudget {
					return nil, nil, false, nil
				}
				raw, _ := json.Marshal(members)
				rows, err := s.read().Query(evaluate, append(append(append([]any{}, ordArgs...), worksArgs...), string(raw))...)
				if err != nil {
					return nil, nil, false, err
				}
				for rows.Next() {
					var id, value, work string
					var entity int64
					if err = rows.Scan(&id, &value, &work, &entity); err != nil {
						rows.Close()
						return nil, nil, false, err
					}
					g := groups[work]
					if g == nil {
						groups[work] = &homeWalkGroup{id: id, ord: value}
						continue
					}
					g.id, g.ord = min(g.id, id), max(g.ord, value)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return nil, nil, false, err
				}
				for _, m := range members {
					seen[m] = true
				}
			}
			// B: the newest recent_text not yet walked. With none left, every
			// remaining work is undated and the walk can finish only on dated
			// works.
			head, err := s.homeWalkNext(heads)
			if err != nil {
				return nil, nil, false, err
			}
			bound, more := "", head != nil
			if more {
				bound = head.rows[0].added
			}
			final := 0
			for _, g := range groups {
				if g.ord > bound && homeWalkAfter(g, want.after) {
					final++
				}
			}
			if final >= need {
				break
			}
			if !more {
				return nil, nil, false, nil
			}
		}
		list := make([]homeWalkGroup, 0, len(groups))
		for _, g := range groups {
			if homeWalkAfter(g, want.after) {
				list = append(list, *g)
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].ord != list[j].ord {
				return list[i].ord > list[j].ord
			}
			return list[i].id > list[j].id
		})
		list = list[want.skip:need]
		ids, ords := make([]string, len(list)), make([]string, len(list))
		for i, g := range list {
			ids[i], ords[i] = g.id, g.ord
		}
		return ids, ords, true, nil
	}
}

// homeWalkAfter reports whether g is ordered after the descending keyset key.
func homeWalkAfter(g *homeWalkGroup, after *[2]string) bool {
	return after == nil || g.ord < after[0] || g.ord == after[0] && g.id < after[1]
}

type homeWalkRow struct {
	entity int64
	added  string
}

// homeWalkHead is one library's position in the walk and its read-ahead.
type homeWalkHead struct {
	library    string
	rows       []homeWalkRow
	lastAdded  string
	lastEntity int64
	done       bool
}

// homeWalkNext returns the head whose next row is newest across libraries,
// reading ahead where a library's buffer is empty, or nil when all are spent.
func (s *Service) homeWalkNext(heads []*homeWalkHead) (*homeWalkHead, error) {
	var best *homeWalkHead
	for _, h := range heads {
		if len(h.rows) == 0 && !h.done {
			if err := s.homeWalkRead(h); err != nil {
				return nil, err
			}
		}
		if len(h.rows) == 0 {
			continue
		}
		row := h.rows[0]
		if best == nil || row.added > best.rows[0].added || row.added == best.rows[0].added && row.entity > best.rows[0].entity {
			best = h
		}
	}
	return best, nil
}

func (s *Service) homeWalkRead(h *homeWalkHead) error {
	rows, err := s.read().Query(`SELECT entity_id,recent_text FROM catalog_browse_rows INDEXED BY catalog_browse_recent_works
 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND recent_text IS NOT NULL AND (recent_text,entity_id)<(?,?)
 ORDER BY recent_text DESC,entity_id DESC LIMIT 128`, h.library, h.lastAdded, h.lastEntity)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var row homeWalkRow
		if err = rows.Scan(&row.entity, &row.added); err != nil {
			return err
		}
		h.rows = append(h.rows, row)
		h.lastAdded, h.lastEntity = row.added, row.entity
		n++
	}
	h.done = n < 128
	return rows.Err()
}

// homeWalkEditions is batch plus every entity not yet seen that shares one of
// a batch entity's provider ids, the ids the work key folds editions by.
func (s *Service) homeWalkEditions(batch []int64, seen map[int64]bool) ([]int64, error) {
	raw, _ := json.Marshal(batch)
	rows, err := s.read().Query(`SELECT DISTINCT d2.item_id FROM metadata_details d1
 CROSS JOIN metadata_details d2 INDEXED BY metadata_details_provider ON d2.provider=d1.provider AND d2.provider_id=d1.provider_id
 WHERE d1.item_id IN(SELECT value FROM json_each(?)) AND d1.provider IN('tmdb','tvdb','anilist') AND d1.provider_id<>''`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := append([]int64{}, batch...)
	in := map[int64]bool{}
	for _, id := range batch {
		in[id] = true
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if !in[id] && !seen[id] {
			in[id] = true
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
