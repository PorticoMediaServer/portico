package read

import (
	"cmp"
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"slices"
	"strings"
	"time"
)

// RSListeningChildCap bounds COMPLETE raw membership, including retired and
// wrong-kind rows. The cap+1 probe refuses overflow before publishing a prefix.
// Native Query's existing copy budget bounds decoding; the separate prospective
// sort charge below bounds retained keys, cursor copies and workspace. The
// actual-byte preflight is the separately tracked #1339 cutover prerequisite.
const RSListeningChildCap = 512
const rsListeningSortVersion = 1

func (r *RSChildrenReader) sortedListeningWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSChildrenInput, recipe rsChildrenRecipe, root rootRead, source *[]any) (RSChildrenPage, BrowseReadResult, error) {
	if in.After != nil && in.After.orderVersion != rsListeningSortVersion {
		return RSChildrenPage{}, BrowseReadResult{}, ErrStale
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[RSChildrenPage]{func(ctx context.Context, q browseQuery, f browseFence, p *RSChildrenPage) error {
		if in.After != nil && (in.After.fence != f || in.After.parent != in.Parent || in.After.family != in.Family || in.After.limit != in.View.Limit) {
			return ErrStale
		}
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return ErrUnqualified
		}
		raw, err := q(rsChildrenName(recipe.name+".first"), in.Parent, int64(RSListeningChildCap+1))
		if err != nil {
			return err
		}
		if len(raw) > RSListeningChildCap {
			return ErrUnqualified
		}
		// The shared query copier independently limits SQL-row copies to half the
		// issued output grant. Sorting reserves a further quarter:128B per raw row
		// plus twice actual key bytes for key/cursor overlap. In-place sorting adds
		// no second complete member slice. No grant or admission limit is increased.
		charge := int64(len(raw)) * 128
		limit := output.Request.Need[governor.MemoryBytes] / 4
		if charge > limit {
			return governor.ErrSnapshotBudget
		}
		for _, row := range raw {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := int64(len(row[19].(string)))
			if n > (limit-charge)/2 {
				return governor.ErrSnapshotBudget
			}
			charge += 2 * n
		}
		slices.SortFunc(raw, func(a, b []any) int {
			for _, pair := range [][2]int64{{a[17].(int64), b[17].(int64)}, {a[18].(int64), b[18].(int64)}} {
				if c := cmp.Compare(pair[0], pair[1]); c != 0 {
					return c
				}
			}
			if c := rsListeningKeyCompare(a[19].(string), b[19].(string)); c != 0 {
				return c
			}
			return cmp.Compare(a[0].(int64), b[0].(int64))
		})
		if err := ctx.Err(); err != nil {
			return err
		}
		p.RawRows = len(raw)
		p.Exhausted = true
		for index, row := range raw {
			key := row[19].(string)
			if a := in.After; a != nil {
				order := cmp.Compare(row[17].(int64), a.first.Value)
				if order == 0 {
					order = cmp.Compare(row[18].(int64), a.second.Value)
				}
				if order == 0 {
					order = rsListeningKeyCompare(key, a.sortKey)
				}
				if order == 0 {
					order = cmp.Compare(row[0].(int64), a.id)
				}
				if order <= 0 {
					continue
				}
			}
			p.RawPositions++
			p.Next = &RSChildrenCursor{fence: f, parent: in.Parent, family: in.Family, limit: in.View.Limit, first: rsNumber(row[17]), second: rsNumber(row[18]), id: row[0].(int64), sortKey: strings.Clone(key), orderVersion: rsListeningSortVersion, source: *source}
			if row[2].(int64) != in.View.Library || row[3].(int64) != recipe.childKind || row[4].(int64) != 0 {
				continue
			}
			public, ok := row[1].([]byte)
			if !ok || len(public) != 16 {
				return ErrUnqualified
			}
			item := RSChild{ID: row[0].(int64), Kind: row[3].(int64), Revision: row[5].(int64), Disc: rsNumber(row[12]), Track: rsNumber(row[13]), Part: rsNumber(row[14]), Season: rsNumber(row[15]), Episode: rsNumber(row[16])}
			copy(item.PublicID[:], public)
			p.Items = append(p.Items, item)
			if len(p.Items) == in.View.Limit {
				p.Exhausted = index == len(raw)-1
				break
			}
		}
		p.Pending = !p.Exhausted
		if p.Exhausted {
			p.Next = nil
		}
		return ctx.Err()
	}}, root)
}

// SQLite NOCASE folds ASCII only, stops comparison at NUL, then compares the
// original byte lengths. Unicode folding and ordinary byte order differ.
func rsListeningKeyCompare(a, b string) int {
	for n := 0; n < min(len(a), len(b)); n++ {
		x, y := a[n], b[n]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
		if x == 0 {
			break
		}
	}
	return cmp.Compare(len(a), len(b))
}
