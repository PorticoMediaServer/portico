package playback

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// DiscConditionalSource is the source owner's actual conditional adapter.
// Resolve/Check are DB-only; Open runs outside SQL and owns a SourcePlayback
// lease with the supplied original-operation or ordinary active-pin proof.
// Neither source facts nor the witness string grant permission. Open must
// refuse unless it can retain a genuine conditional version for that source.
type DiscConditionalSource interface {
	ResolveTx(context.Context, *sql.Tx, identity.Principal, string, string) (playbackdisc.Source, error)
	CheckTx(context.Context, *sql.Tx, playbackdisc.Source) error
	Open(context.Context, playbackdisc.Source, func(context.Context, *sql.Tx) error) (storage.RemoteObject, error)
}
