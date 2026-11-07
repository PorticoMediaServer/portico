package read

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// PreparedJobInput selects a job for one currently visible catalogue item.
// Prepared media management remains local-owner-only. Profile in the result
// names the encoding recipe, not an authenticated viewer identity.
type PreparedJobInput struct {
	View BrowseInput
	Job  string
	Item int64
}

type PreparedJobStatus struct {
	ID, State, Phase, ErrorCode                     string
	Generation, CleanedGeneration, Bytes, Revision  int64
	CreatedMS, UpdatedMS                            int64
	Version, Asset, Target, Profile, SourceRevision string
	Item                                            BrowseItem
}

// PreparedJob reads saved work status, not permission to execute a helper or
// open the output. A succeeded job's source revision may be historical; output
// selection/delivery must independently prove its current source and pins.
func (r *ForegroundReader) PreparedJob(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in PreparedJobInput) (PreparedJobStatus, BrowseReadResult, error) {
	if r == nil {
		return PreparedJobStatus{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (PreparedJobStatus, BrowseReadResult, error) {
		in.View.Now = at
		return r.preparedJob(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) preparedJob(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in PreparedJobInput) (PreparedJobStatus, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || len(in.Job) == 0 || len(in.Job) > 128 || !utf8.ValidString(in.Job) || strings.ContainsAny(in.Job, "\x00\r\n") || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return PreparedJobStatus{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		bridge, err := q("library_identity", in.View.Library)
		if err != nil {
			return nil, err
		}
		if len(bridge) != 1 {
			return nil, ErrDenied
		}
		rows, err := q("foreground.mp.prepared_job", in.Job, in.Item, in.View.Library, in.View.Kind, bridge[0][0])
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		source = rows[0]
		return source, nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[PreparedJobStatus]{func(ctx context.Context, _ browseQuery, f browseFence, out *PreparedJobStatus) error {
		v := source
		if f.role != "owner" || !admittedItem(v[15:22], f) {
			return ErrDenied
		}
		item, err := itemValue(v[15:22])
		if err != nil {
			return err
		}
		*out = PreparedJobStatus{ID: v[0].(string), State: v[1].(string), Phase: v[2].(string), Generation: v[3].(int64), CleanedGeneration: v[4].(int64), Bytes: v[5].(int64), ErrorCode: v[6].(string), Revision: v[7].(int64), CreatedMS: v[8].(int64), UpdatedMS: v[9].(int64), Version: v[10].(string), Asset: v[11].(string), Target: v[12].(string), Profile: v[13].(string), SourceRevision: v[14].(string), Item: item}
		if out.Generation < 0 || out.CleanedGeneration < 0 || out.Bytes < 0 || out.Revision <= 0 {
			return ErrUnqualified
		}
		return ctx.Err()
	}}, root)
}

func PreparedJobHarnessDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), mpPreparedDefinitions()...)
}
