package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// PersonalStateInput is a point lookup in a currently authorized library/kind.
// The profile comes from the current viewing token, never a caller's profile ID.
type PersonalStateInput struct {
	View BrowseInput
	Item int64
}

// PersonalState is sparse current state, separate from immutable play history.
// Rating preserves the distinction between unrated (nil) and a rating of zero.
type PersonalState struct {
	PublicID [16]byte
	UserState
	NotInterested bool
	Rating        *int64
}

// PersonalState returns default state for a live authorized title without a
// personal row. Retired, hidden and missing titles are denied. Harness-only:
// ConsumeForeground must keep output charged through the consumer's return.
func (r *ForegroundReader) PersonalState(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in PersonalStateInput) (PersonalState, BrowseReadResult, error) {
	if r == nil {
		return PersonalState{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (PersonalState, BrowseReadResult, error) {
		in.View.Now = at
		return r.uoPersonalState(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) uoPersonalState(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in PersonalStateInput) (PersonalState, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return PersonalState{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("foreground.uo.personal.point", in.Item, in.View.TokenHash, in.View.Library, in.View.Kind)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		source = rows[0]
		return source, nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[PersonalState]{func(ctx context.Context, _ browseQuery, fence browseFence, draft *PersonalState) error {
		if !admittedItem(source[:7], fence) {
			return ErrDenied
		}
		public, ok := source[2].([]byte)
		if !ok || len(public) != 16 {
			return ErrUnqualified
		}
		copy(draft.PublicID[:], public)
		if source[7] == nil {
			return ctx.Err()
		}
		// Retained state cannot attach to a different public identity merely
		// because an internal ID happens to match.
		personalPublic, ok := source[17].([]byte)
		if !ok || !uoPersonalPublicIDMatches(personalPublic, draft.PublicID) {
			return ErrUnqualified
		}
		draft.UserState = stateValue(source[7:15])
		draft.NotInterested = source[15].(int64) != 0
		if source[16] != nil {
			rating := source[16].(int64)
			draft.Rating = &rating
		}
		return ctx.Err()
	}}, root)
}
