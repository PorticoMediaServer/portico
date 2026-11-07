package read

import (
	"context"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Similar and the More like row of Related use the exact same candidate set,
// scorer, exclusion and continuation. No second stored similar/taste image.
func (r *ForegroundReader) Similar(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RelatedInput, w *RelatedWork) (RelatedPage, BrowseReadResult, error) {
	return r.relatedPublic(ctx, request, budget, output, in, w, true)
}
func relatedBefore(a, b *relatedCandidate) bool {
	if x, y := relatedShared(a), relatedShared(b); x != y {
		return x > y
	}
	if x, y := a.entity.row[9].(float64), b.entity.row[9].(float64); x != y {
		return x > y
	}
	return a.entity.id() < b.entity.id()
}
func relatedRows(s *relatedState, in RelatedInput, similar bool) ([]RelatedRow, error) {
	keys := []RelatedKey{{}}
	if in.Row != nil {
		keys = []RelatedKey{*in.Row}
	} else if !similar {
		// Fixed presentation order: More like, two genre/tag rows, one credited
		// person, then collection/typed parent rows, at most six nonempty rows.
		term, person := 0, 0
		for _, f := range s.features {
			if f.key.Kind == 7 {
				continue
			}
			if f.key.Kind == 1 {
				if term == 2 {
					continue
				}
				term++
			}
			if f.key.Kind == 2 {
				if person == 1 {
					continue
				}
				person++
			}
			keys = append(keys, f.key)
		}
	}
	if similar {
		keys = []RelatedKey{{}}
	}
	if in.After != nil && (len(keys) != 1 || keys[0] != in.After.row) {
		return nil, ErrStale
	}
	var out []RelatedRow
	for _, key := range keys {
		fi := -1
		if key.Kind != 0 {
			for n, f := range s.features {
				if f.key == key {
					fi = n
					break
				}
			}
			if fi < 0 {
				return nil, ErrStale
			}
		}
		var candidates []*relatedCandidate
		for _, c := range s.candidates {
			if c.visible && (fi < 0 || relatedHas(c, fi)) {
				candidates = append(candidates, c)
			}
		}
		slices.SortFunc(candidates, func(a, b *relatedCandidate) int {
			if relatedBefore(a, b) {
				return -1
			}
			if relatedBefore(b, a) {
				return 1
			}
			return 0
		})
		row := RelatedRow{Key: key, Total: len(candidates), Exhausted: true}
		for _, c := range candidates {
			shared, quality := relatedShared(c), c.entity.row[9].(float64)
			if in.After != nil {
				a := in.After
				if shared > a.shared || shared == a.shared && (quality > a.quality || quality == a.quality && c.entity.id() <= a.id) {
					continue
				}
			}
			if len(row.Entries) == in.View.Limit {
				row.Exhausted = false
				break
			}
			e := c.entity
			entry := RelatedEntry{ID: e.id(), Title: s.cards[e.id()], Kind: e.kind(), Revision: e.row[5].(int64), Year: e.row[8].(int64), Quality: quality, Shared: shared, Watched: relatedInt(e.row[26]) != 0, Favorite: relatedInt(e.row[27]) != 0, Watchlisted: relatedInt(e.row[28]) != 0, PersonalRevision: relatedInt(e.row[23])}
			copy(entry.PublicID[:], e.row[1].([]byte))
			for n, f := range s.features {
				if relatedHas(c, n) {
					entry.Reasons = append(entry.Reasons, f.key)
				}
			}
			row.Entries = append(row.Entries, entry)
		}
		if !row.Exhausted {
			last := row.Entries[len(row.Entries)-1]
			row.Next = &RelatedCursor{fence: s.fence, source: slices.Clone(s.source), item: in.Item, row: key, shared: last.Shared, quality: last.Quality, id: last.ID, algorithm: rsRelatedAlgorithm}
		}
		if len(row.Entries) > 0 || in.Row != nil || similar {
			out = append(out, row)
		}
		if len(out) == 6 {
			break
		}
	}
	return out, nil
}
