package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type PersonalTitleFilterInput struct {
	View  BrowseInput
	Query json.RawMessage
	After *PersonalTitleFilterCursor
}
type PersonalTitleFilterCursor struct {
	fence browseFence
	query [32]byte
	key   string
	id    int64
}
type PersonalTitleFilterPage struct {
	Items                 []BrowseItem
	After                 *PersonalTitleFilterCursor
	RawRows, RawPositions int
	Exhausted             bool
}

// PersonalTitleFilterWindow is an unwired movie-item source window for the
// three closed personal fields. It is neither a container/member recipe nor an
// exact total. Empty filtered full batches retain genuine raw continuation.
func (r *BrowseReader) PersonalTitleFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in PersonalTitleFilterInput) (PersonalTitleFilterPage, BrowseReadResult, error) {
	if r == nil {
		return PersonalTitleFilterPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (PersonalTitleFilterPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.personalTitleFilterWindow(ctx, request, budget, out, in)
	})
}
func (r *BrowseReader) personalTitleFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in PersonalTitleFilterInput) (PersonalTitleFilterPage, BrowseReadResult, error) {
	if in.View.After != nil || in.View.policy != (readPolicyBinding{}) || request.Budget.RawRows < 2*MaxRaw+5 || len(in.Query) > 64<<10 || !utf8.Valid(in.Query) {
		return PersonalTitleFilterPage{}, BrowseReadResult{}, ErrInput
	}
	if in.View.Kind != 1 {
		return PersonalTitleFilterPage{}, BrowseReadResult{}, ErrUnqualified
	}
	s, err := newSavedViewSession(&SavedViewReader{snapshots: r.snapshots, pool: r.pool}, ctx, request, budget, out, in.View)
	if err != nil {
		return PersonalTitleFilterPage{}, BrowseReadResult{}, err
	}
	if err = s.reserve((1 << 20) + (32 << 10) + 16*int64(len(in.Query))); err != nil {
		return PersonalTitleFilterPage{}, s.result(), err
	}
	body := bytes.Clone(in.Query)
	var first browseFence
	var filter rbPersonalFilter
	var hash [32]byte
	ready := false
	err = s.window(false, 0, func(q browseQuery) error {
		var e error
		first, e = readBrowseFence(q, s.input(in.View))
		if e != nil {
			return e
		}
		filter, e = compileRBPersonalFilter(body)
		if e != nil {
			return e
		}
		canonical, e := json.Marshal(filter.node)
		if e != nil {
			return e
		}
		hash = sha256.Sum256(canonical)
		if in.After != nil && (in.After.fence != first || in.After.query != hash) {
			return ErrStale
		}
		ready = true
		return nil
	})
	var rows [][]any
	var page PersonalTitleFilterPage
	if err == nil {
		err = s.window(false, 0, func(q browseQuery) error {
			var e error
			rows, e = readRBPersonalTitleRows(s, q, in, first)
			if e != nil {
				return e
			}
			page, e = rbPersonalTitlePage(ctx, in, first, filter, hash, rows)
			return e
		})
	}
	if ready {
		prior := err
		// Five current fence calls plus at most four thin/payload seek legs.
		final := s.window(true, 12, func(q browseQuery) error {
			fresh, e := readBrowseFence(q, s.input(in.View))
			if e != nil {
				return e
			}
			if fresh != first {
				return ErrStale
			}
			current, e := readRBPersonalTitleRows(s, q, in, fresh)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(rows, current) {
				return ErrStale
			}
			return nil
		})
		err = s.finish(first, prior, final)
	}
	if err != nil {
		return PersonalTitleFilterPage{}, s.result(), err
	}
	return page, s.result(), nil
}

func readRBPersonalTitleRows(s *savedViewSession, q browseQuery, in PersonalTitleFilterInput, f browseFence) ([][]any, error) {
	name := "rb.filter.personal.title.ignore.asc."
	if in.View.KeepArticles {
		name = "rb.filter.personal.title.keep.asc."
	}
	if in.View.Descending {
		name = "rb.filter.personal.title.ignore.desc."
		if in.View.KeepArticles {
			name = "rb.filter.personal.title.keep.desc."
		}
	}
	leg := func(part string, args ...any) ([][]any, error) {
		sizes, e := q(name+part+".bytes", args...)
		if e != nil {
			return nil, e
		}
		if len(sizes) > MaxRaw {
			return nil, ErrUnqualified
		}
		if len(sizes) == 0 {
			return nil, nil
		}
		var total int64
		for _, row := range sizes {
			if len(row) != 3 {
				return nil, ErrUnqualified
			}
			for _, v := range row[1:] {
				n, ok := v.(int64)
				if !ok || n < 0 || n > min(s.nativeLimit, s.copyLimit/2)-total {
					return nil, governor.ErrSnapshotBudget
				}
				total += n
			}
		}
		if 8192+int64(len(sizes))*1024+2*total > s.copyLimit-s.copied {
			return nil, governor.ErrSnapshotBudget
		}
		payloadArgs := append([]any{f.profile}, args...)
		rows, e := q(name+part, payloadArgs...)
		if e != nil {
			return nil, e
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 13 || row[0] != sizes[i][0] {
				return nil, ErrStale
			}
			for k, pos := range []int{1, 3} {
				v, ok := row[pos].(string)
				if !ok || int64(len(v)) != sizes[i][k+1] {
					return nil, ErrStale
				}
			}
		}
		return rows, nil
	}
	if in.After == nil {
		return leg("first", in.View.Library, in.View.Kind, int64(MaxRaw))
	}
	rows, e := leg("tail", in.View.Library, in.View.Kind, in.After.key, in.After.id, int64(MaxRaw))
	if e != nil {
		return nil, e
	}
	if len(rows) < MaxRaw {
		more, e := leg("next", in.View.Library, in.View.Kind, in.After.key, int64(MaxRaw-len(rows)))
		if e != nil {
			return nil, e
		}
		rows = append(rows, more...)
	}
	return rows, nil
}

func rbPersonalTitlePage(ctx context.Context, in PersonalTitleFilterInput, f browseFence, filter rbPersonalFilter, hash [32]byte, rows [][]any) (PersonalTitleFilterPage, error) {
	page := PersonalTitleFilterPage{RawRows: len(rows)}
	for _, row := range rows {
		if e := ctx.Err(); e != nil {
			return PersonalTitleFilterPage{}, e
		}
		page.RawPositions++
		page.After = &PersonalTitleFilterCursor{f, hash, row[1].(string), row[0].(int64)}
		if !admittedItem(row, f) {
			continue
		}
		facts := rbPersonalFilterFacts{}
		if row[7] != nil {
			public, ok := row[8].([]byte)
			item, valid := row[2].([]byte)
			if !ok || !valid || !bytes.Equal(public, item) || row[9] != f.libraryID {
				return PersonalTitleFilterPage{}, ErrUnqualified
			}
			facts.present = true
			facts.favorite = row[10] == int64(1)
			facts.watchlisted = row[11] == int64(1)
			if row[12] != nil {
				v, ok := row[12].(int64)
				if !ok || v < 0 || v > 10 {
					return PersonalTitleFilterPage{}, ErrUnqualified
				}
				facts.rating = &v
			}
		}
		if !filter.matches(facts) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return PersonalTitleFilterPage{}, e
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(rows) && len(rows) < MaxRaw
	if page.Exhausted {
		page.After = nil
	}
	return page, nil
}
