package catalog

import (
	"encoding/json"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// New on server is a shelf, including See all: never a library-sized relation.
const homeNewServerCap = 500

// Candidate, edition and member reads have independent budgets. An exhausted
// budget returns the completed works already found; it never enables a deeper
// query. While derivation is pending, published recent_text bounds the order.
type homeNewServerWalk struct {
	s                             *Service
	r                             HomeRequest
	settled                       bool
	candidates, editions, members int
	seen                          map[int64]bool
	groups                        map[string]*homeWalkGroup
}

type homeNewServerWork struct {
	entity             int64
	id, key, published string
}

func (s *Service) homeNewServerSource(r HomeRequest) (homeSource, error) {
	libraries, err := s.homeLibraries(r.Libraries)
	if err != nil {
		return homeSource{}, err
	}
	r.Libraries = nil
	heads := []*homeWalkHead{}
	for _, l := range libraries {
		if l.Kind != "movie" && l.Kind != "tv" && l.Kind != "anime" {
			continue
		}
		r.Libraries = append(r.Libraries, l.ID)
		heads = append(heads, &homeWalkHead{library: l.ID, lastAdded: "￿", lastEntity: 1 << 62})
	}
	settled, err := compactcatalog.RecentOrderSettled(s.Context(), s.read())
	if err != nil {
		return homeSource{}, err
	}
	w := &homeNewServerWalk{s: s, r: r, settled: settled, candidates: homeWalkBudget, editions: homeWalkBudget, members: homeWalkBudget, seen: map[int64]bool{}, groups: map[string]*homeWalkGroup{}}
	for {
		batch := []int64{}
		for len(batch) < homeWalkBatch {
			h, e := w.next(heads)
			if e != nil {
				return homeSource{}, e
			}
			if h == nil {
				break
			}
			row := h.rows[0]
			h.rows = h.rows[1:]
			if !w.seen[row.entity] {
				batch = append(batch, row.entity)
			}
		}
		if len(batch) > 0 {
			complete, e := w.add(batch)
			if e != nil {
				return homeSource{}, e
			}
			if !complete {
				break
			}
		}
		h, e := w.next(heads)
		if e != nil {
			return homeSource{}, e
		}
		bound := ""
		if h != nil {
			bound = h.rows[0].added
		}
		final := 0
		for _, g := range w.groups {
			if g.ord > bound {
				final++
			}
		}
		if final >= homeNewServerCap+1 {
			break
		}
		if h == nil {
			// Undated works sort last. Walk raw work kinds before testing whether
			// their publication is undated: a million episodes cannot become a scan.
			if w.candidates > 0 {
				if err = w.undated(libraries); err != nil {
					return homeSource{}, err
				}
			}
			break
		}
	}
	list := make([]homeWalkGroup, 0, len(w.groups))
	for _, g := range w.groups {
		list = append(list, *g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ord != list[j].ord {
			return list[i].ord > list[j].ord
		}
		return list[i].id > list[j].id
	})
	values := make([][2]string, 0, min(homeNewServerCap, len(list)))
	for _, g := range list[:min(homeNewServerCap, len(list))] {
		values = append(values, [2]string{g.id, g.ord})
	}
	return homeRecentList(values), nil
}

func (w *homeNewServerWalk) next(heads []*homeWalkHead) (*homeWalkHead, error) {
	var best *homeWalkHead
	for _, h := range heads {
		if len(h.rows) == 0 && !h.done && w.candidates > 0 {
			limit := min(128, w.candidates)
			rows, err := w.s.read().Query(`SELECT entity_id,recent_text FROM catalog_browse_rows INDEXED BY catalog_browse_recent_works
 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND recent_text IS NOT NULL AND (recent_text,entity_id)<(?,?)
 ORDER BY recent_text DESC,entity_id DESC LIMIT ?`, h.library, h.lastAdded, h.lastEntity, limit)
			if err != nil {
				return nil, err
			}
			n := 0
			for rows.Next() {
				var row homeWalkRow
				if err = rows.Scan(&row.entity, &row.added); err != nil {
					rows.Close()
					return nil, err
				}
				h.rows = append(h.rows, row)
				h.lastAdded, h.lastEntity = row.added, row.entity
				n++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			w.candidates -= n
			h.done = n < limit
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

func (w *homeNewServerWalk) work(ids []int64) ([]homeNewServerWork, error) {
	raw, _ := json.Marshal(ids)
	rows, err := w.s.read().Query(`SELECT i.id,pid(i.public_id),`+homeRecentWorkKeySQL("i")+`,COALESCE(br.recent_text,'')
 FROM json_each(?) candidate CROSS JOIN catalog_entities i ON i.id=candidate.value
 CROSS JOIN catalog_browse_rows br ON br.entity_id=i.id CROSS JOIN catalog_libraries cl ON cl.id=i.library_id
 WHERE i.kind IN(1,2) AND i.retired=0 AND cl.retired=0 AND cl.library_id IN(SELECT value FROM json_each(?))
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)`, string(raw), idsJSON(w.r.Libraries))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []homeNewServerWork{}
	for rows.Next() {
		var x homeNewServerWork
		if err = rows.Scan(&x.entity, &x.id, &x.key, &x.published); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Finish an edition group before publishing any of its members. If a budget
// ends mid-group, retain previous groups rather than a false canonical id.
func (w *homeNewServerWalk) add(batch []int64) (bool, error) {
	originals, err := w.work(batch)
	if err != nil {
		return false, err
	}
	keys := map[string]bool{}
	movieIDs := []int64{}
	for _, x := range originals {
		keys[x.key] = true
		if len(x.key) >= 6 && x.key[:6] == "movie:" {
			movieIDs = append(movieIDs, x.entity)
		}
	}
	ids := append([]int64{}, batch...)
	in := map[int64]bool{}
	for _, id := range ids {
		in[id] = true
	}
	if len(ids) > w.editions {
		return false, nil
	}
	left := w.editions - len(ids)
	if len(movieIDs) > 0 {
		if left == 0 {
			return false, nil
		}
		raw, _ := json.Marshal(movieIDs)
		rows, e := w.s.read().Query(`SELECT DISTINCT d2.item_id FROM json_each(?) candidate
 CROSS JOIN metadata_details d1 ON d1.item_id=candidate.value
 CROSS JOIN metadata_details d2 INDEXED BY metadata_details_provider ON d2.provider=d1.provider AND d2.provider_id=d1.provider_id
 WHERE d1.provider IN('tmdb','tvdb','anilist') AND d1.provider_id<>'' LIMIT ?`, string(raw), left)
		if e != nil {
			return false, e
		}
		n := 0
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return false, e
			}
			n++
			if !in[id] && !w.seen[id] {
				in[id] = true
				ids = append(ids, id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return false, e
		}
		// Count every returned edition candidate, including prior or repeated
		// works, so a provider fan-out never silently renews the budget.
		if n == left {
			return false, nil
		}
		w.editions -= len(batch) + n
	} else {
		w.editions -= len(batch)
	}
	works, err := w.work(ids)
	if err != nil {
		return false, err
	}
	films := []int64{}
	for _, x := range works {
		if keys[x.key] && len(x.key) >= 6 && x.key[:6] == "movie:" {
			films = append(films, x.entity)
		}
	}
	filmMembers, complete, err := w.movies(films)
	if err != nil || !complete {
		return false, err
	}
	stage := map[string]*homeWalkGroup{}
	for _, x := range works {
		if !keys[x.key] {
			continue
		}
		var ord string
		var visible, complete bool
		var e error
		if film, ok := filmMembers[x.entity]; ok {
			ord, visible, complete = film.ord, film.visible, true
		} else {
			ord, visible, complete, e = w.member(x.entity)
		}
		if e != nil {
			return false, e
		}
		if !complete {
			return false, nil
		}
		if !visible {
			continue
		}
		if !w.settled {
			ord = x.published
		}
		g := stage[x.key]
		if g == nil {
			stage[x.key] = &homeWalkGroup{id: x.id, ord: ord}
		} else {
			g.id = min(g.id, x.id)
			g.ord = max(g.ord, ord)
		}
	}
	for key, g := range stage {
		if prior := w.groups[key]; prior == nil {
			w.groups[key] = g
		} else {
			prior.id = min(prior.id, g.id)
			prior.ord = max(prior.ord, g.ord)
		}
	}
	for _, id := range batch {
		w.seen[id] = true
	}
	for _, x := range works {
		if keys[x.key] {
			w.seen[x.entity] = true
		}
	}
	return true, nil
}

type homeNewServerMember struct {
	ord     string
	visible bool
}

// Films are point checks batched over an already bounded edition list. This
// avoids one SQL execution and LIMIT reprepare per film on every preview.
func (w *homeNewServerWalk) movies(ids []int64) (map[int64]homeNewServerMember, bool, error) {
	out := map[int64]homeNewServerMember{}
	if len(ids) > w.members {
		return nil, false, nil
	}
	if len(ids) == 0 {
		return out, true, nil
	}
	visible, args := w.r.Viewer.itemVisibilitySQL("candidate.value")
	raw, _ := json.Marshal(ids)
	rows, err := w.s.read().Query(`SELECT candidate.value,COALESCE(metric.added,''),COALESCE((
 EXISTS(SELECT 1 FROM catalog_item_availability a WHERE a.entity_id=candidate.value AND a.available=1 AND a.retired=0)
 AND EXISTS(SELECT 1 FROM catalog_browse_memberships current WHERE current.entity_id=candidate.value AND current.item_id=candidate.value)
 AND `+visible+recordingsClause("candidate.value", w.r.Restrictions)+`),0)
 FROM json_each(?) candidate LEFT JOIN catalog_browse_member_metrics metric
 ON metric.entity_id=candidate.value AND metric.item_id=candidate.value AND metric.source=0`, append(append([]any{}, args...), string(raw))...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var m homeNewServerMember
		if err = rows.Scan(&id, &m.ord, &m.visible); err != nil {
			return nil, false, err
		}
		w.members--
		out[id] = m
	}
	return out, true, rows.Err()
}

// The visibility predicate is projected, not placed before LIMIT. Hidden or
// unavailable members consume the same fixed budget as visible members.
func (w *homeNewServerWalk) member(entity int64) (string, bool, bool, error) {
	visible, args := w.r.Viewer.itemVisibilitySQL("m.item_id")
	predicate := `EXISTS(SELECT 1 FROM catalog_entities e CROSS JOIN catalog_item_availability a ON a.entity_id=e.id
 WHERE e.id=m.item_id AND e.retired=0 AND a.available=1 AND a.retired=0) AND EXISTS(SELECT 1 FROM catalog_browse_memberships current WHERE current.entity_id=? AND current.item_id=m.item_id) AND ` + visible + recordingsClause("m.item_id", w.r.Restrictions)
	phases := []string{
		`SELECT m.item_id,m.added FROM catalog_browse_member_metrics m INDEXED BY catalog_browse_metric_added WHERE m.entity_id=? AND m.added IS NOT NULL ORDER BY m.added DESC LIMIT ?`,
		`SELECT m.item_id,'' AS added FROM catalog_browse_member_metrics m WHERE m.entity_id=? AND m.added IS NULL ORDER BY m.item_id,m.source LIMIT ?`,
		`SELECT m.item_id,'' AS added FROM catalog_browse_memberships m WHERE m.entity_id=? ORDER BY m.item_id,m.source LIMIT ?`,
	}
	for _, phase := range phases {
		if w.members == 0 {
			return "", false, false, nil
		}
		rows, err := w.s.read().Query(`SELECT m.added,COALESCE((`+predicate+`),0) FROM (`+phase+`) m`, append(append([]any{entity}, args...), entity, w.members)...)
		if err != nil {
			return "", false, false, err
		}
		for rows.Next() {
			var ord string
			var allowed bool
			if err = rows.Scan(&ord, &allowed); err != nil {
				rows.Close()
				return "", false, false, err
			}
			w.members--
			if allowed {
				rows.Close()
				return ord, true, true, nil
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", false, false, err
		}
	}
	return "", false, true, nil
}

func (w *homeNewServerWalk) undated(libraries []Library) error {
	for _, l := range libraries {
		if l.Kind != "movie" && l.Kind != "tv" && l.Kind != "anime" {
			continue
		}
		for _, kind := range []int{1, 2} {
			last := int64(1 << 62)
			for w.candidates > 0 {
				rows, err := w.s.read().Query(`SELECT i.id,COALESCE(br.recent_text,'') FROM
 (SELECT id FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=? AND id<? ORDER BY id DESC LIMIT ?) candidate
 CROSS JOIN catalog_entities i ON i.id=candidate.id LEFT JOIN catalog_browse_rows br ON br.entity_id=i.id`, l.ID, kind, last, min(homeWalkBatch, w.candidates))
				if err != nil {
					return err
				}
				batch := []int64{}
				n := 0
				for rows.Next() {
					var id int64
					var published string
					if err = rows.Scan(&id, &published); err != nil {
						rows.Close()
						return err
					}
					n++
					last = id
					if published == "" && !w.seen[id] {
						batch = append(batch, id)
					}
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				w.candidates -= n
				if len(batch) > 0 {
					complete, e := w.add(batch)
					if e != nil {
						return e
					}
					if !complete {
						return nil
					}
				}
				if n == 0 {
					break
				}
			}
		}
	}
	return nil
}
