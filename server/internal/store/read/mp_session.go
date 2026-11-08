package read

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MediaSessionInput selects one catalogue session owned by the current token's
// device/account/profile. Channels and receiver/group authority need their own
// recipes. No media URL, grant, stored request or presentation body is returned.
type MediaSessionInput struct {
	View    BrowseInput
	Session string
	Item    int64
}

// MediaSessionHead contains durable facts, never a permit to control or deliver
// media. LeaseExpiresMS is a timestamp, not a promise of continued liveness;
// commands must reauthorize and compare the exact current occurrence.
type MediaSessionHead struct {
	ID, Kind, Role, State                         string
	Item                                          BrowseItem
	Version, Queue, Entry, MediaSession           string
	Part, Revision, Generation, MediaGeneration   int64
	PositionMS, StartPositionMS, LastSequence     int64
	LeaseExpiresMS, CreatedMS, UpdatedMS, EndedMS int64
	EndReason                                     string
}

func validMediaSessionInput(in MediaSessionInput) bool {
	return in.Item > 0 && len(in.Session) > 0 && len(in.Session) <= 128 && utf8.ValidString(in.Session) && !strings.ContainsAny(in.Session, "\x00\r\n") && in.View.After == nil && !in.View.Descending && !in.View.KeepArticles
}

// SessionHead reuses RD2's foreground publication and live consumer protocol.
// The selected item's current visibility is checked along with session ownership.
func (r *ForegroundReader) SessionHead(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MediaSessionInput) (MediaSessionHead, BrowseReadResult, error) {
	if r == nil {
		return MediaSessionHead{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MediaSessionHead, BrowseReadResult, error) {
		in.View.Now = at
		return r.sessionHead(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) sessionHead(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MediaSessionInput) (MediaSessionHead, BrowseReadResult, error) {
	if r == nil || !validMediaSessionInput(in) {
		return MediaSessionHead{}, BrowseReadResult{}, ErrInput
	}
	windows := &mpByteWindow{source: r.snapshots, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("foreground.mp.session_source", in.View.TokenHash, in.Session, in.Item, in.View.Library, in.View.Kind)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		return rows[0], nil
	}
	return publishRead(ctx, windows, request, budget, output, in.View, []readStep[MediaSessionHead]{
		func(ctx context.Context, q browseQuery, _ browseFence, _ *MediaSessionHead) error {
			sizes, err := q("foreground.mp.session_head_octets", in.View.TokenHash, in.Session, in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(sizes) != 1 {
				return ErrDenied
			}
			bytes, err := mpPointBytes(sizes[0])
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			return windows.arm(bytes, 3)
		},
		func(ctx context.Context, q browseQuery, f browseFence, out *MediaSessionHead) error {
			rows, err := q("foreground.mp.session_head", in.View.TokenHash, in.Session, in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				if errors.Is(err, governor.ErrSnapshotBudget) {
					return mpByteGrowth(windows, q, "foreground.mp.session_head_octets", in.View.TokenHash, in.Session, in.Item, in.View.Library, in.View.Kind)
				}
				return err
			}
			if len(rows) != 1 {
				return ErrDenied
			}
			v := rows[0]
			if !admittedItem(v[22:29], f) {
				return ErrDenied
			}
			item, err := itemValue(v[22:29])
			if err != nil {
				return err
			}
			*out = MediaSessionHead{ID: v[0].(string), Kind: v[1].(string), Role: v[2].(string), State: v[3].(string), Version: v[4].(string), Queue: v[5].(string), Entry: v[6].(string), MediaSession: v[7].(string), Part: v[8].(int64), Revision: v[9].(int64), Generation: v[10].(int64), MediaGeneration: v[11].(int64), PositionMS: v[12].(int64), StartPositionMS: v[13].(int64), LastSequence: v[14].(int64), LeaseExpiresMS: v[15].(int64), CreatedMS: v[16].(int64), UpdatedMS: v[17].(int64), EndedMS: v[18].(int64), EndReason: v[19].(string), Item: item}
			if out.Revision <= 0 || out.Generation <= 0 || out.MediaGeneration < 0 || out.Part < 0 || out.PositionMS < 0 || out.StartPositionMS < 0 || out.LastSequence < 0 || out.LeaseExpiresMS <= 0 || out.EndedMS < 0 {
				return ErrUnqualified
			}
			return ctx.Err()
		},
	}, root)
}

// MediaSessionHarnessDefinitions is explicit harness composition only; capture
// and register on the actual bounded reader before invoking SessionHead.
func MediaSessionHarnessDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), mpSessionDefinitions()...)
}
