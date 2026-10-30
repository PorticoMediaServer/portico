package metadata

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Source-head invalidation is deferred, but accepting an in-flight provider
// result is not allowed through that interval. This is a background publication
// barrier, not authority: owner/status reads and live root/grant checks stay
// unchanged and immediate. Durable failures remain fail closed.
func publicationSourcesReady(ctx context.Context, tx *sql.Tx) (bool, error) {
	return compactcatalog.PublicationSourcesReady(ctx, tx)
}
