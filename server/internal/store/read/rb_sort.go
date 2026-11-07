package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ScalarSort is a closed accepted source order, not an SQL identifier.
type ScalarSort string

const (
	SortYear            ScalarSort = "year"
	SortDuration        ScalarSort = "duration"
	SortCommunityRating ScalarSort = "communityRating"
	SortCriticRating    ScalarSort = "criticRating"
	SortEpisodeCount    ScalarSort = "episodeCount"
	SortLatestAired     ScalarSort = "latestAired"
	SortReleaseDate     ScalarSort = "releaseDate"
	SortAired           ScalarSort = "aired"
	SortTrackNumber     ScalarSort = "trackNumber"
	SortFileSize        ScalarSort = "fileSize"
)

type ScalarInput struct {
	View  BrowseInput // View.After must be nil; use this order's own cursor
	Sort  ScalarSort
	After *ScalarCursor
}

// ScalarCursor is values-only, private and bound to exact viewer/query/order
// dependencies. A cutover adapter still needs the authenticated wire codec.
type ScalarCursor struct {
	fence browseFence
	sort  ScalarSort
	key   any // exact native int64/float64/string, never formatted across types
	id    int64
}

type ScalarPage struct {
	Items                             []BrowseItem
	Next                              *ScalarCursor
	Exhausted                         bool
	RawRows, RawPositions, Statements int
}

// SortedWindow uses the merged LIVE/output and first/final publication protocol.
// Only the existing supported browse authority/policy envelope is claimed.
// Unsupported kinds/policies/owner-only sorts fail closed, without SQL fallback.
func (r *BrowseReader) SortedWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ScalarInput) (ScalarPage, BrowseReadResult, error) {
	if r == nil {
		return ScalarPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (ScalarPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.sortedWindow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) sortedWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ScalarInput) (page ScalarPage, result BrowseReadResult, err error) {
	if r == nil || in.View.After != nil {
		return page, result, ErrInput
	}
	spec, ok := rbScalarSpec(in.Sort)
	if !ok {
		return page, result, ErrInput
	}
	if !spec.supports(in.View.Kind) {
		return page, result, ErrUnqualified
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[ScalarPage]{func(ctx context.Context, query browseQuery, f browseFence, draft *ScalarPage) error {
		if in.After != nil && (in.After.fence != f || in.After.sort != in.Sort) {
			return ErrStale
		}
		if in.Sort == SortFileSize && f.role != "owner" {
			return ErrDenied
		}
		var e error
		*draft, e = readScalarPage(ctx, query, in, f)
		return e
	}})
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return
}
func readScalarPage(ctx context.Context, query browseQuery, in ScalarInput, f browseFence) (ScalarPage, error) {
	prefix := "rb.scalar." + string(in.Sort) + ".asc."
	if in.View.Descending {
		prefix = "rb.scalar." + string(in.Sort) + ".desc."
	}
	args := []any{in.View.Library, in.View.Kind}
	name := "first"
	if in.After != nil {
		name = "tail"
		args = append(args, in.After.key, in.After.id)
	}
	raw, err := query(prefix+name, append(args, int64(MaxRaw))...)
	if err != nil {
		return ScalarPage{}, err
	}
	if in.After != nil && len(raw) < MaxRaw {
		next, e := query(prefix+"next", in.View.Library, in.View.Kind, in.After.key, int64(MaxRaw-len(raw)))
		if e != nil {
			return ScalarPage{}, e
		}
		raw = append(raw, next...)
	}
	page := ScalarPage{RawRows: len(raw)}
	for _, row := range raw {
		if e := ctx.Err(); e != nil {
			return ScalarPage{}, e
		}
		page.RawPositions++
		page.Next = &ScalarCursor{fence: f, sort: in.Sort, key: row[1], id: row[0].(int64)}
		if !admittedItem(row, f) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return ScalarPage{}, e
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(raw) && len(raw) < MaxRaw
	if page.Exhausted {
		page.Next = nil
	}
	return page, nil
}
