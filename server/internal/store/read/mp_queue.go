package read

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MediaReader supplies harness-only playback and queue reads. It mints no
// media grant and never retains a snapshot through media or consumer IO.
type MediaReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewMediaReader(s *governor.Snapshots) (*MediaReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &MediaReader{snapshots: s, pool: s}, nil
}

// MediaQueueInput selects a device-owned queue in an authorized library scope.
// Group queues require a separate group-authority recipe. This read does not
// authorize the current entry's media or derive a total from an incomplete layout.
type MediaQueueInput struct {
	View  BrowseInput
	Queue string
}

type MediaQueueHead struct {
	ID, CurrentEntry, Session, Repeat string
	Revision, CustodyGeneration       int64
	Position, AutomaticAdvances       int64
	Shuffle                           bool
	ShuffleSeed, ShuffleLap           int64
	ShuffleDomain, ShuffleFirst       int64
	CreatedMS, UpdatedMS              int64
}

func validMediaID(s string) bool {
	if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// QueueHead publishes only after a fresh principal/library and exact queue-head
// comparison. The authenticated token's device, account and profile own the
// queue; callers cannot supply those ownership fields.
func (r *MediaReader) QueueHead(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MediaQueueInput) (MediaQueueHead, BrowseReadResult, error) {
	if r == nil {
		return MediaQueueHead{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MediaQueueHead, BrowseReadResult, error) {
		in.View.Now = at
		return r.queueHead(ctx, request, budget, output, in)
	})
}

func (r *MediaReader) queueHead(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MediaQueueInput) (MediaQueueHead, BrowseReadResult, error) {
	if r == nil || !validMediaID(in.Queue) || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return MediaQueueHead{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("foreground.mp.queue_head", in.View.TokenHash, in.Queue)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		source = rows[0]
		return source, nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[MediaQueueHead]{
		func(ctx context.Context, _ browseQuery, _ browseFence, out *MediaQueueHead) error {
			v := source
			*out = MediaQueueHead{ID: v[0].(string), Revision: v[1].(int64), CustodyGeneration: v[2].(int64), CurrentEntry: v[3].(string), Position: v[4].(int64), Repeat: v[5].(string), Shuffle: v[6].(int64) == 1, ShuffleSeed: v[7].(int64), ShuffleLap: v[8].(int64), ShuffleDomain: v[9].(int64), ShuffleFirst: v[10].(int64), Session: v[11].(string), AutomaticAdvances: v[12].(int64), CreatedMS: v[13].(int64), UpdatedMS: v[14].(int64)}
			if out.Revision <= 0 || out.CustodyGeneration < 0 || out.Position < 0 || out.AutomaticAdvances < 0 || v[6].(int64) < 0 || v[6].(int64) > 1 || (out.Repeat != "off" && out.Repeat != "one" && out.Repeat != "all") {
				return ErrUnqualified
			}
			return ctx.Err()
		},
	}, root)
}

// MediaHarnessDefinitions composes the unchanged common/RD2 definitions with
// MP's own statements. HN must Capture them on the actual reader before use.
func MediaHarnessDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), mpQueueDefinitions()...)
}
