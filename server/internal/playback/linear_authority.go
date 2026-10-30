package playback

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// LinearAuthority is what the linear runtime needs from a v1 channel session
// (channel_sessions.go): the work to run, buffer and producer fencing, media
// authority, status, and the schedule's retunes.
type LinearAuthority interface {
	LinearWorkIDs(ctx context.Context, after string, limit int) ([]string, error)
	LinearWork(ctx context.Context, id string) (LinearWork, error)
	WithLinearWriteTx(ctx context.Context, id string, use func(*sql.Tx, LinearWork) error) error
	BeginLinearBuffer(ctx context.Context, id string) (LinearWork, error)
	BeginLinearProducer(ctx context.Context, id string, sourceRevision, bufferOrdinal int64) (LinearWork, error)
	CheckLinearProducer(ctx context.Context, w LinearWork) error
	AuthorizeLinearMedia(ctx context.Context, id string, bufferOrdinal int64, selection *LinearSelection) (identity.Principal, error)
	RefreshLinearSelection(ctx context.Context, id string) (bool, error)
	RestartLinearSource(ctx context.Context, id string) error
	LinearStatus(ctx context.Context, w LinearWork, status, stage, code string) error
}

var _ LinearAuthority = (*ChannelSessions)(nil)
