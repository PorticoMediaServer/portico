package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Only document reads use the container/Reading fallback. Byte, personal and
// timed routes retain detailFenceFor's strict playable-kind gate.
func (d Dependencies) detailContentFenceFor(r *http.Request, p identity.Principal) (string, string, error) {
	library, namespace, e := d.Catalog.WithContext(r.Context()).LibraryForDetail(r.PathValue("id"))
	if e != nil {
		return "", "", e
	}
	library, fence, e := d.detailLibraryFenceFor(r, p, library)
	if e != nil {
		return "", "", e
	}
	viewer, e := d.catalogViewer(r, p, []string{library}, fence)
	if e != nil {
		return "", "", e
	}
	e = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		var kind int
		if e := dbwork.Snapshot(ctx).QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?) AND retired=0`, r.PathValue("id")).Scan(&kind); e != nil {
			return e
		}
		if kind == 20 || kind == 21 {
			return d.savedReadingItemAuthorityTx(ctx, dbwork.Snapshot(ctx), p, r.PathValue("id"))
		}
		return d.Catalog.WithContext(ctx).VisibleEntity(ctx, viewer, namespace, r.PathValue("id"))
	})
	return library, fence, e
}
func (d Dependencies) detailContentAccess(r *http.Request) (identity.Principal, string, string, error) {
	p, e := d.principal(r)
	if e != nil {
		return p, "", "", e
	}
	library, fence, e := d.detailContentFenceFor(r, p)
	return p, library, fence, e
}
