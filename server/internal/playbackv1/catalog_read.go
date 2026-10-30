package playbackv1

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// readItemKind hydrates one catalogue identity's kind name. Facts are written
// synchronously, so there is no unpublished change to wait for; an unknown id
// simply finds no row.
func readItemKind(ctx context.Context, q compactcatalog.ReadQuery, item string) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx, `SELECT k.name FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 WHERE e.public_id=pid_blob(?)`, item).Scan(&kind)
	return kind, err
}
