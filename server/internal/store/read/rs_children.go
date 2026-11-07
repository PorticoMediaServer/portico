package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RSChildrenFamily selects a compiled direct-membership recipe. ShowEpisodes
// means the show's unseasoned episodes, as on the current public episode route;
// seasoned episodes belong to SeasonEpisodes, not an ID-ordered flattened show.
type RSChildrenFamily uint8

const (
	RSAlbumTracks    RSChildrenFamily = 1
	RSAudiobookFiles RSChildrenFamily = 2
	RSShowSeasons    RSChildrenFamily = 3
	RSShowEpisodes   RSChildrenFamily = 4
	RSSeasonEpisodes RSChildrenFamily = 5
	RSArtistAlbums   RSChildrenFamily = 6
)

type RSChildrenInput struct {
	View   BrowseInput
	Parent int64
	Family RSChildrenFamily
	After  *RSChildrenCursor
}

// RSChildNumber preserves SQL NULL separately from zero and from the effective
// listening-order defaults (disc 1, track/part 0). No display strings are copied.
type RSChildNumber struct {
	Value   int64
	Present bool
}

// RSChild is an authorized hydration input, not a public card. A future card
// hydrator must join this source/cursor fence in the same publication operation;
// it cannot treat these IDs as authority for a later independent lookup.
type RSChild struct {
	ID, Kind, Revision                 int64
	PublicID                           [16]byte
	Disc, Track, Part, Season, Episode RSChildNumber
}

type RSChildrenPage struct {
	Items                 []RSChild
	Next                  *RSChildrenCursor
	Exhausted, Pending    bool
	RawRows, RawPositions int
}

// Private, values-only anchors bind authority, query, root/accepted source and
// library data/order revisions. They are not a network cursor codec.
type RSChildrenCursor struct {
	fence         browseFence
	parent        int64
	family        RSChildrenFamily
	limit         int
	first, second RSChildNumber
	id            int64
	sortKey       string
	orderVersion  uint8
	source        []any
}

type RSChildrenReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewRSChildrenReader(s *governor.Snapshots) (*RSChildrenReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &RSChildrenReader{snapshots: s, pool: s}, nil
}

// Window uses the merged LIVE/output and first/final publication protocol,
// including one complete retry. Default restricted-container refusal remains
// intact until the parent supplies a closed complete-eligibility recipe.
// Qualification entry: each of the six families, first and deep continuation,
// sparse retired/wrong-kind tails, NULL/defaults, source drift and slow consumer.
// B-R10's 1M 25/75ms and 50k VM remain unqualified. On this base the five common
// fence calls plus root, twice, cost 12 statements before the 1..3 stream calls:
// 13..15 total exceeds B-R10's 12-statement target. Whole-response composition must
// resolve that shared-protocol cost; these functional controls do not waive it.
func (r *RSChildrenReader) Window(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSChildrenInput) (RSChildrenPage, BrowseReadResult, error) {
	if r == nil {
		return RSChildrenPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RSChildrenPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.window(ctx, request, budget, output, in)
	})
}

func (r *RSChildrenReader) window(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSChildrenInput) (RSChildrenPage, BrowseReadResult, error) {
	recipe, ok := rsChildrenRecipeFor(in.Family)
	if r == nil || !ok || in.Parent <= 0 || in.View.Kind != recipe.parentKind || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return RSChildrenPage{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		rows, err := q(rsChildrenName("source"), in.Parent, in.View.Library, recipe.parentKind)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 || rows[0][4].(int64) != 0 {
			return nil, ErrDenied
		}
		if in.After != nil && !equalSource(in.After.source, rows[0]) {
			return nil, ErrStale
		}
		source = rows[0]
		return source, nil
	}
	if recipe.keys == 2 {
		return r.sortedListeningWindow(ctx, request, budget, output, in, recipe, root, &source)
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[RSChildrenPage]{
		func(ctx context.Context, _ browseQuery, f browseFence, _ *RSChildrenPage) error {
			if in.After != nil && (in.After.fence != f || in.After.parent != in.Parent || in.After.family != in.Family || in.After.limit != in.View.Limit) {
				return ErrStale
			}
			// Keep the current shared default even when composed with a future
			// foundation. This family has no complete restricted-member proof yet.
			if f.maximumAge >= 0 || f.allowUnrated == 0 {
				return ErrUnqualified
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *RSChildrenPage) error {
			var err error
			*d, err = rsReadChildren(ctx, q, f, in, recipe, source)
			return err
		},
	}, root)
}

func rsNumber(value any) RSChildNumber {
	if value == nil {
		return RSChildNumber{}
	}
	return RSChildNumber{Value: value.(int64), Present: true}
}
func (n RSChildNumber) binding() any {
	if !n.Present {
		return nil
	}
	return n.Value
}

func rsReadChildren(ctx context.Context, q browseQuery, f browseFence, in RSChildrenInput, recipe rsChildrenRecipe, source []any) (RSChildrenPage, error) {
	var page RSChildrenPage
	query := func(suffix string, args ...any) ([][]any, error) {
		return q(rsChildrenName(recipe.name+"."+suffix), args...)
	}
	var raw [][]any
	var err error
	if in.After == nil {
		raw, err = query("first", in.Parent, int64(MaxRaw))
	} else {
		a := in.After
		args := []any{in.Parent}
		suffix := "tail"
		if recipe.nullable && !a.first.Present {
			suffix = "tail_null"
		}
		if recipe.keys > 0 && suffix != "tail_null" {
			args = append(args, a.first.binding())
		}
		if recipe.keys > 1 {
			args = append(args, a.second.binding())
		}
		args = append(args, a.id, int64(MaxRaw))
		raw, err = query(suffix, args...)
		if err == nil && recipe.keys > 1 && len(raw) < MaxRaw {
			var next [][]any
			next, err = query("next_second", in.Parent, a.first.binding(), a.second.binding(), int64(MaxRaw-len(raw)))
			raw = append(raw, next...)
		}
		if err == nil && recipe.keys > 0 && len(raw) < MaxRaw {
			suffix := "next"
			args := []any{in.Parent}
			if a.first.Present {
				args = append(args, a.first.Value)
			} else {
				suffix = "next_null"
			}
			args = append(args, int64(MaxRaw-len(raw)))
			var next [][]any
			next, err = query(suffix, args...)
			raw = append(raw, next...)
		}
	}
	if err != nil {
		return RSChildrenPage{}, err
	}
	page.RawRows = len(raw)
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return RSChildrenPage{}, err
		}
		page.RawPositions++
		page.Next = &RSChildrenCursor{fence: f, parent: in.Parent, family: in.Family, limit: in.View.Limit, first: rsNumber(row[17]), second: rsNumber(row[18]), id: row[0].(int64), source: source}
		// Limits apply before current kind/library/live/age rejection. A wrong
		// kind or retired row still consumes and advances the raw stream.
		if row[2].(int64) != in.View.Library || row[3].(int64) != recipe.childKind || row[4].(int64) != 0 {
			continue
		}
		if row[6] == nil && f.allowUnrated == 0 || row[6] != nil && f.maximumAge >= 0 && row[6].(int64) > f.maximumAge {
			continue
		}
		if in.Family == RSSeasonEpisodes && row[7] != source[7] {
			continue
		}
		public, ok := row[1].([]byte)
		if !ok || len(public) != 16 {
			return RSChildrenPage{}, ErrUnqualified
		}
		item := RSChild{ID: row[0].(int64), Kind: row[3].(int64), Revision: row[5].(int64), Disc: rsNumber(row[12]), Track: rsNumber(row[13]), Part: rsNumber(row[14]), Season: rsNumber(row[15]), Episode: rsNumber(row[16])}
		copy(item.PublicID[:], public)
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(raw) && len(raw) < MaxRaw
	page.Pending = !page.Exhausted
	if page.Exhausted {
		page.Next = nil
	}
	return page, nil
}
