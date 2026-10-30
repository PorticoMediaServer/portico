package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The Saved browse's original Viewer is a scope ceiling, never personal-write
// authority. Rehydrate the captured principal and intersect current access in
// the same snapshot before any private reference or ready metadata is read.
func (d Dependencies) savedReadingGroupsAdmission(p identity.Principal, captured catalog.Viewer) catalog.SavedGroupAdmission {
	return func(ctx context.Context, tx *sql.Tx) (catalog.Viewer, error) {
		live, err := d.readingSavedGroupPrincipalTx(ctx, tx, p)
		if err != nil {
			return catalog.Viewer{}, err
		}
		if identity.PersonalKey(live.Viewer) != captured.Profile {
			return catalog.Viewer{}, identity.ErrUnauthorized
		}
		restrictions, err := contentaccess.RestrictionsTx(ctx, tx, live.Viewer)
		if err != nil {
			return catalog.Viewer{}, err
		}
		libraries := make([]string, 0, len(captured.Libraries))
		for _, library := range captured.Libraries {
			err = d.allowedLibraryTx(ctx, live, library, tx)
			if errors.Is(err, identity.ErrNotVisible) || errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return catalog.Viewer{}, err
			}
			libraries = append(libraries, library)
		}
		return catalog.Viewer{Profile: captured.Profile, Fence: captured.Fence, Libraries: libraries, Restrictions: restrictions}, nil
	}
}
