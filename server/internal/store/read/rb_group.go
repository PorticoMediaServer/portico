package read

import (
	"context"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RBGroupFamily names compiled harness recipes. Candidate streams are not a
// ready global groupOrder, rank, total, or proof of aggregate visibility.
type RBGroupFamily uint8

const (
	RBGroupShowSeasons RBGroupFamily = iota + 1
	RBGroupShowEpisodes
	RBGroupSeasonEpisodes
	RBGroupArtistAlbums
	RBGroupAlbumSongs
	RBGroupAuthorBooks
	RBGroupBookParts
	RBGroupCollectionMembers
	RBGroupPlaylistEntries
	RBGroupArtistCredits
)

type RBGroupInput struct {
	author  string
	View    BrowseInput
	Family  RBGroupFamily
	Subject int64
	After   *RBGroupCursor
}
type RBGroupCursor struct {
	fence   browseFence
	family  RBGroupFamily
	subject int64
	keys    []any
}

type RBGroupPage struct {
	// Retain the exact internal publication fence even for an exhausted window.
	fence browseFence
	Items []BrowseItem
	// SourceOrdered is true only when the physical stream matches the direct
	// group's public order. False requires complete exact preparation before
	// claiming groupOrder; this page is only an admitted source window.
	SourceOrdered                     bool
	Exhausted                         bool
	Next                              *RBGroupCursor
	RawPositions, RawRows, Statements int
}

// RBGroupWindow requires ConsumeForeground custody, including final consumer
// lifetime. It is harness-only and has no product registration.
func (r *BrowseReader) RBGroupWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBGroupInput) (RBGroupPage, BrowseReadResult, error) {
	if r == nil {
		return RBGroupPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBGroupPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.rbGroupWindow(ctx, request, budget, output, in)
	})
}

func (r *BrowseReader) rbGroupWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBGroupInput) (page RBGroupPage, result BrowseReadResult, err error) {
	spec, ok := rbGroupSpecFor(in.Family)
	if !ok || in.Subject <= 0 || in.View.After != nil || !rbGroupKind(spec, in.View.Kind) {
		return page, result, ErrInput
	}
	// An author requires a current eligible-book proof. The initial generic
	// member source does not establish that proof, so it cannot expose authors
	// through collection/playlist candidates until the author recipe is complete.
	if in.View.Kind == 13 {
		return page, result, ErrUnqualified
	}
	if in.After != nil && (in.After.family != in.Family || in.After.subject != in.Subject) {
		return page, result, ErrStale
	}
	in.View.policy = readPolicyBinding{recipe: rbGroupPolicy(in.Family), subject: in.Subject}
	// Keep fence and business in separate quanta: full authority + subject proof
	// uses six calls; the source has up to three disjoint ordered seek legs.
	steps := []readStep[RBGroupPage]{
		func(_ context.Context, query browseQuery, f browseFence, _ *RBGroupPage) error {
			if in.After != nil && in.After.fence != f {
				return ErrStale
			}
			if in.Family == RBGroupAuthorBooks {
				rows, e := query("rb.group.subject", in.Subject)
				if e != nil {
					return e
				}
				if len(rows) != 1 {
					return ErrDenied
				}
				in.author = rows[0][13].(string)
				if e := rbAuthorIndexedName(in.author); e != nil {
					return e
				}
			}
			return nil
		},
		func(ctx context.Context, query browseQuery, f browseFence, draft *RBGroupPage) error {
			p, e := rbReadGroup(ctx, query, in, f, spec)
			*draft = p
			return e
		},
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, steps)
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return
}

func rbReadGroup(ctx context.Context, query browseQuery, in RBGroupInput, f browseFence, spec rbGroupSpec) (page RBGroupPage, err error) {
	name := "rb.group." + spec.name + ".asc."
	if in.View.Descending {
		name = "rb.group." + spec.name + ".desc."
	}
	var raw [][]any
	if in.After == nil {
		args := rbGroupScopeArgs(in)
		raw, err = query(name+"first", append(args, int64(MaxRaw))...)
	} else {
		// Lexicographic continuation: equality prefix + strict range on one key.
		// No row-value expression can hide a linear walk across a far tie.
		for level := len(spec.keys) - 1; level >= 0 && len(raw) < MaxRaw; level-- {
			args := rbGroupScopeArgs(in)
			args = append(args, in.After.keys[:level+1]...)
			rows, e := query(fmt.Sprintf("%sseek%d", name, level), append(args, int64(MaxRaw-len(raw)))...)
			if e != nil {
				return RBGroupPage{}, e
			}
			raw = append(raw, rows...)
		}
	}
	if err != nil {
		return RBGroupPage{}, err
	}
	page.RawRows = len(raw)
	page.fence = f
	page.SourceOrdered = spec.ordered
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return RBGroupPage{}, err
		}
		page.RawPositions++
		page.Next = &RBGroupCursor{fence: f, family: in.Family, subject: in.Subject, keys: append([]any(nil), row[9:]...)}
		// Raw positions (including retired, foreign-library/kind and NULL playlist
		// entries) advance BEFORE eligibility. A subject proof does not admit a grid.
		if row[0] == nil || row[7] != in.View.Library || row[8] != in.View.Kind || !admittedItem(row, f) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return RBGroupPage{}, e
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
func rbGroupScopeArgs(in RBGroupInput) []any {
	if in.Family == RBGroupAuthorBooks {
		return []any{in.View.Library, in.author}
	}
	return []any{in.Subject}
}
