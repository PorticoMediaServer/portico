package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (d Dependencies) libraryList(w http.ResponseWriter, r *http.Request) {
	d.libraryListAfterRead(w, r, nil)
}

// afterRead is a synchronous interleaving seam for the authority regression
// tests. Production passes nil; no process-wide hook or authority cache exists.
func (d Dependencies) libraryListAfterRead(w http.ResponseWriter, r *http.Request, afterRead func()) {
	generation := dbwork.AuthorityGeneration()
	visible := []catalog.Library{}
	var captured identity.Principal
	var restrictions identity.ContentRestrictions
	err := dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		var err error
		captured, err = d.principal(r.WithContext(dbwork.WithClass(ctx, dbwork.ClassSecurityFence)))
		if err != nil {
			return err
		}
		libraries, err := d.Catalog.WithContext(ctx).LibrariesForViewer(resourceActor(captured))
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(libraries))
		for _, lib := range libraries {
			ids = append(ids, lib.ID)
		}
		allowed, err := d.recordingPolicy().AllowedSetTx(ctx, dbwork.Snapshot(ctx), captured, ids)
		if err != nil {
			return err
		}
		var visibleIDs []string
		for _, lib := range libraries {
			if allowed[lib.ID] {
				visible = append(visible, lib)
				visibleIDs = append(visibleIDs, lib.ID)
			}
		}
		restrictions, err = contentaccess.RestrictionsTx(ctx, dbwork.Snapshot(ctx), captured.Viewer)
		if err != nil {
			return err
		}
		viewer := catalog.Viewer{Profile: identity.PersonalKey(captured.Viewer), Libraries: visibleIDs, Restrictions: restrictions}
		summaries, err := d.Catalog.SummarizeLibraryList(ctx, viewer, visible)
		if err != nil {
			return err
		}
		for i := range visible {
			if summary, ok := summaries[visible[i].ID]; ok {
				visible[i].Summary = &summary
			}
		}
		return nil
	})
	if err == nil && afterRead != nil {
		afterRead()
	}
	if err == nil && dbwork.AuthorityGeneration() != generation {
		// A different user's publication must not deny this viewer. Recheck once
		// against fresh committed authority; another change during it is refused.
		currentGeneration := dbwork.AuthorityGeneration()
		err = dbwork.WithReadSnapshot(dbwork.WithClass(r.Context(), dbwork.ClassSecurityFence), d.DB, func(ctx context.Context) error {
			current, e := d.principal(r.WithContext(ctx))
			if e != nil {
				return e
			}
			if current.Viewer != captured.Viewer {
				return identity.ErrUnauthorized
			}
			live, e := contentaccess.RestrictionsTx(ctx, dbwork.Snapshot(ctx), current.Viewer)
			if e != nil {
				return e
			}
			if catalog.RestrictionFence(live) != catalog.RestrictionFence(restrictions) {
				return identity.ErrForbidden
			}
			ids := make([]string, 0, len(visible))
			for _, lib := range visible {
				ids = append(ids, lib.ID)
			}
			allowed, e := d.recordingPolicy().AllowedSetTx(ctx, dbwork.Snapshot(ctx), current, ids)
			if e != nil {
				return e
			}
			for _, id := range ids {
				if !allowed[id] {
					return identity.ErrForbidden
				}
			}
			return nil
		})
		if err == nil && dbwork.AuthorityGeneration() != currentGeneration {
			err = identity.ErrForbidden
		}
	}
	if err != nil {
		if errors.Is(err, catalog.ErrReadingProjectionChanged) {
			policyError(w, "revision_mismatch")
		} else {
			failure(w, err)
		}
		return
	}
	write(w, http.StatusOK, map[string]any{"items": visible})
}
