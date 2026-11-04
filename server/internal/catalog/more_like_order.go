package catalog

import (
	"fmt"
	"sort"
)

func MoreLikeDescription() string {
	return fmt.Sprintf("More like X is a ranked recommendation set, not every title sharing a facet. A same-library set of at most %d works retains its exact ranking; a larger library returns at most %d recommendations from at most %d fully rescored candidates. Total and completion describe the selected ranked set. Continuations exhaust that fixed set without changing prior pages.", recommendationPoolCutover, recRanked, recRescore)
}

// moreLikeOrder ranks a fixed recommendation set, not an exhaustive facet
// listing. Small libraries retain the complete exact ranking; large libraries
// use the existing posting, rescore and ranked-set budgets. complete and Total
// describe this selected set. Page depth never changes its membership or order.
func (s *Service) moreLikeOrder(viewer Viewer, work int64) func(int) (*relationOrdered, error) {
	return func(int) (*relationOrdered, error) {
		pool, large, err := s.moreLikeCandidates(work)
		if err != nil {
			return nil, err
		}
		visible, args := viewer.entityVisibilitySQL("i.id", "l.library_id")
		visible += ` AND NOT EXISTS(SELECT 1 FROM personal_items negative WHERE negative.item_id=i.id AND negative.profile_id=? AND (negative.not_interested=1 OR negative.rating<=4))`
		limit := recommendationPoolCutover
		if large {
			limit = recRanked
		}
		bind := []any{idsJSON64(pool), work, work, work}
		bind = append(bind, args...)
		bind = append(bind, viewer.Profile, limit)
		ids, err := scanInt64s(s.read().Query(`SELECT i.id FROM json_each(?) requested
   CROSS JOIN catalog_entities i ON i.id=requested.value
   CROSS JOIN catalog_libraries l ON l.id=i.library_id
   CROSS JOIN catalog_rec_facets candidate ON candidate.entity_id=i.id
   CROSS JOIN catalog_rec_facets seed ON seed.entity_id=? AND seed.facet=candidate.facet
   CROSS JOIN catalog_rec_postings posting INDEXED BY catalog_rec_postings_entity ON posting.entity_id=i.id AND posting.facet=candidate.facet AND posting.library_id=i.library_id
   WHERE i.library_id=(SELECT library_id FROM catalog_entities WHERE id=?) AND i.id<>? AND `+visible+`
   GROUP BY i.id ORDER BY count(DISTINCT seed.facet) DESC,max(posting.quality) DESC,i.id LIMIT ?`, bind...))
		return &relationOrdered{ids: ids, complete: true}, err
	}
}

func (s *Service) moreLikeCandidates(work int64) ([]int64, bool, error) {
	// One capped library/kind seek decides the cutover without scoring facets.
	small, err := scanInt64s(s.read().Query(`SELECT i.id FROM json_each('[1,2,6,8]') kind
  CROSS JOIN catalog_entities i INDEXED BY catalog_entities_library ON i.library_id=(SELECT library_id FROM catalog_entities WHERE id=?) AND i.kind=kind.value WHERE i.retired=0 LIMIT ?`, work, recommendationPoolCutover+1))
	if err != nil || len(small) <= recommendationPoolCutover {
		return small, false, err
	}
	// Use a stable seed-only facet order within the established source budget.
	// Global facet frequency cannot pin a continuation: another library can
	// change it without publishing a new revision of this library. Full
	// rescoring below still counts every shared facet.
	facets, err := moreLikeFacets(s, work)
	if err != nil {
		return nil, true, err
	}
	type candidate struct {
		id      int64
		shared  int
		quality float64
	}
	found := map[int64]*candidate{}
	for _, facet := range facets {
		rows, err := s.read().Query(`SELECT entity_id,quality FROM catalog_rec_postings
   WHERE facet=? AND library_id=(SELECT library_id FROM catalog_entities WHERE id=?)
   ORDER BY quality DESC,entity_id DESC LIMIT ?`, facet, work, recPostingsHead)
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var id int64
			var quality float64
			if err = rows.Scan(&id, &quality); err != nil {
				break
			}
			if id == work {
				continue
			}
			item := found[id]
			if item == nil {
				item = &candidate{id: id, quality: quality}
				found[id] = item
			}
			item.shared++
			item.quality = max(item.quality, quality)
		}
		if next := rows.Err(); err == nil {
			err = next
		}
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	ranked := make([]*candidate, 0, len(found))
	for _, item := range found {
		ranked = append(ranked, item)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.shared != b.shared {
			return a.shared > b.shared
		}
		if a.quality != b.quality {
			return a.quality > b.quality
		}
		return a.id < b.id
	})
	out := make([]int64, 0, min(recRescore, len(ranked)))
	for _, item := range ranked[:min(recRescore, len(ranked))] {
		out = append(out, item.id)
	}
	return out, true, nil
}

func moreLikeFacets(s *Service, work int64) ([]string, error) {
	rows, err := s.read().Query(`SELECT facet FROM catalog_rec_facets WHERE entity_id=? ORDER BY facet LIMIT ?`, work, recTasteFacets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facets := []string{}
	for rows.Next() {
		var facet string
		if err = rows.Scan(&facet); err != nil {
			return nil, err
		}
		facets = append(facets, facet)
	}
	return facets, rows.Err()
}
