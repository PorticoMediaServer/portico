package httpapi

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Retained profile events confer no authority over their stored title IDs.
// Resolve both library access and item/container visibility in the event page's
// snapshot, including the current profile and member restrictions.
func (d Dependencies) eventContentVisible(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) bool {
	if d.Catalog == nil || tx == nil {
		return false
	}
	ctx, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return false
	}
	var library, kind string
	var playable bool
	if err = tx.QueryRowContext(ctx, `SELECT l.library_id,k.name,k.playable FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?) AND e.retired=0 AND l.retired=0`, id).Scan(&library, &kind, &playable); err != nil {
		return false
	}
	if err = d.allowedLibraryTx(ctx, p, library, tx); err != nil {
		return false
	}
	restrictions, err := contentaccess.RestrictionsTx(ctx, tx, p.Viewer)
	if err != nil {
		return false
	}
	viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Libraries: []string{library}, Restrictions: restrictions}
	cat := d.Catalog.WithContext(ctx)
	if playable || kind == "ebook" || kind == "comic" {
		return cat.VisibleItem(ctx, viewer, id) == nil
	}
	return cat.VisibleEntity(ctx, viewer, kind, id) == nil
}
