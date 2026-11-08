package read

import (
	"bytes"
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// UOMembershipFlag selects exactly one closed sparse personal flag. The caller
// can never supply a column, predicate, SQL name or profile ID.
type UOMembershipFlag uint8

const (
	UOMembershipWatchlist UOMembershipFlag = iota + 1
	UOMembershipFavorite
	UOMembershipNotInterested
)

type MembershipInput struct {
	View  BrowseInput
	Flag  UOMembershipFlag
	After *MembershipCursor
}

// MembershipCursor is a values-only public-ID keyset position. Its private
// fields bind one viewer/flag/library/kind fence, so another viewer's cursor
// cannot continue this sparse membership.
type MembershipCursor struct {
	fence  browseFence
	flag   UOMembershipFlag
	public [16]byte
}

type MembershipPage struct {
	Items []BrowseItem
	// Exhausted proves the whole remaining raw membership stream was consumed.
	// An empty non-exhausted page is pending work, never a zero-match answer.
	Exhausted    bool
	Next         *MembershipCursor
	RawRows      int
	RawPositions int
	Statements   int
}

// MembershipWindow reads one of the three closed sparse personal flag
// memberships for the current viewing token's own profile. It walks at most64
// raw (profile_id,item_public_id) positions per turn and delivers at most the
// caller's bounded page limit. Raw positions precede bounded entity/library/
// kind/current-policy authorization; hidden, retired, other-library and other-
// kind subjects still advance the continuation. It never materializes the
// profile's catalogue and never adds a catalogue sort pivot: RB owns those.
// Harness-only: ConsumeForeground must keep output charged through the
// consumer's return.
func (r *ForegroundReader) MembershipWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MembershipInput) (MembershipPage, BrowseReadResult, error) {
	if r == nil || !in.Flag.valid() || in.View.After != nil || in.View.Descending || in.View.KeepArticles || request.Budget.RawRows < MaxRaw {
		return MembershipPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MembershipPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.membershipWindow(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) membershipWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MembershipInput) (page MembershipPage, result BrowseReadResult, err error) {
	if r == nil || !in.Flag.valid() || in.View.After != nil || in.View.Descending || in.View.KeepArticles || request.Budget.RawRows < MaxRaw {
		return MembershipPage{}, BrowseReadResult{}, ErrInput
	}
	base := "foreground.uo.membership." + in.Flag.membershipName()
	// The shared publication helper re-reads this exact root in its fresh final
	// snapshot. It performs the same-snapshot octet preflight BEFORE the payload,
	// so an oversized title budget refuses without fetching the payload.
	var sizes, rows [][]any
	var source []any
	root := func(q browseQuery) ([]any, error) {
		source = nil
		var e error
		if in.After == nil {
			sizes, e = q(base+".first.bytes", in.View.TokenHash)
		} else {
			sizes, e = q(base+".tail.bytes", in.View.TokenHash, in.After.public[:])
		}
		if e != nil {
			return nil, e
		}
		if len(sizes) > MaxRaw {
			return nil, ErrUnqualified
		}
		var total int64
		for _, row := range sizes {
			if len(row) != 3 {
				return nil, ErrUnqualified
			}
			public, ok := row[0].([]byte)
			if !ok || len(public) != 16 {
				return nil, ErrUnqualified
			}
			if row[2] == nil {
				continue
			}
			n, ok := row[2].(int64)
			if !ok || n < 0 {
				return nil, ErrUnqualified
			}
			total += n
		}
		// Reserve the actual bounded payload octets before any payload query.
		limit := output.Request.Need[governor.MemoryBytes] / 2
		if limit <= 0 || 8192+int64(len(sizes))*1024+2*total > limit {
			return nil, governor.ErrSnapshotBudget
		}
		if in.After == nil {
			rows, e = q(base+".first", in.View.TokenHash)
		} else {
			rows, e = q(base+".tail", in.View.TokenHash, in.After.public[:])
		}
		if e != nil {
			return nil, e
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 10 {
				return nil, ErrUnqualified
			}
			public, ok := row[0].([]byte)
			if !ok || len(public) != 16 || !bytes.Equal(public, sizes[i][0].([]byte)) {
				return nil, ErrStale
			}
			if row[1] != sizes[i][1] {
				return nil, ErrStale
			}
			if sizes[i][2] == nil {
				if row[4] != nil {
					return nil, ErrStale
				}
			} else {
				title, ok := row[4].(string)
				if !ok || int64(len(title)) != sizes[i][2].(int64) {
					return nil, ErrStale
				}
			}
			source = append(source, row...)
		}
		return source, nil
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[MembershipPage]{func(ctx context.Context, _ browseQuery, f browseFence, draft *MembershipPage) error {
		if in.After != nil && (in.After.fence != f || in.After.flag != in.Flag) {
			return ErrStale
		}
		draft.RawRows = len(rows)
		for _, row := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			public, ok := row[0].([]byte)
			if !ok || len(public) != 16 {
				return ErrUnqualified
			}
			var key [16]byte
			copy(key[:], public)
			draft.Next = &MembershipCursor{fence: f, flag: in.Flag, public: key}
			draft.RawPositions++
			// Eligibility is authorized only after this raw ordered position.
			if row[8] == nil || row[9] == nil {
				continue
			}
			if row[8].(int64) != in.View.Library || row[9].(int64) != in.View.Kind {
				continue
			}
			if !admittedItem(row[1:8], f) || row[6].(int64) <= 0 {
				continue
			}
			item, e := itemValue(row[1:8])
			if e != nil {
				return e
			}
			// Retained membership cannot attach to a reassigned internal ID.
			if item.PublicID != key {
				return ErrUnqualified
			}
			draft.Items = append(draft.Items, item)
			if len(draft.Items) == in.View.Limit {
				break
			}
		}
		draft.Exhausted = draft.RawPositions == len(rows) && len(rows) < MaxRaw
		if draft.Exhausted {
			draft.Next = nil
		}
		return nil
	}}, root)
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return page, result, err
}
