package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// homeActionAuthority uses the command services' live session/owner/library
// checks. A role string or a readable Home alone does not authorize a command.
// The caller must be inside Home's read snapshot, before its post-read fence.
func (d Dependencies) homeActionAuthority(ctx context.Context, p identity.Principal) (catalog.HomeActionAuthority, error) {
	out := catalog.HomeActionAuthority{}
	tx := dbwork.Snapshot(ctx)
	if tx == nil || d.Identity == nil {
		return out, identity.ErrUnauthorized
	}
	// The current command family seam supports local device sessions. Other
	// admitted read authorities receive no inferred device-command grants.
	if p.Authority != "local" {
		return out, nil
	}
	if _, err := d.Identity.SessionFamilyTx(ctx, tx, p); err != nil {
		return out, err
	}
	if err := d.allowedLibraryTx(ctx, p, "", tx); err != nil {
		return out, err
	}
	out.Personal = true
	out.Organize = d.Catalog != nil
	out.ReadingCollections = d.Catalog != nil && identity.CanUsePersonalState(p)
	out.Bulk = d.Scheduler != nil
	out.Playback = d.v1 != nil && d.Playback != nil
	out.Queue = d.v1 != nil
	if d.Downloads != nil {
		var err error
		out.Downloads, err = downloads.ProfileAllowsDownloads(tx, p.Viewer)
		if err != nil {
			return out, err
		}
	}
	_, metadataErr := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin)
	if metadataErr == nil {
		out.Metadata = d.Metadata != nil
		out.Refresh = d.Metadata != nil
	} else if !errors.Is(metadataErr, identity.ErrUnauthorized) && !errors.Is(metadataErr, identity.ErrForbidden) {
		return out, metadataErr
	}
	ownerErr := d.ownerAuthorityTx(ctx, tx, p)
	if ownerErr != nil {
		if errors.Is(ownerErr, identity.ErrUnauthorized) || errors.Is(ownerErr, identity.ErrForbidden) {
			return out, nil
		}
		return out, ownerErr
	}
	if d.Administration != nil {
		out.DeleteLibrary = func(library string) (bool, error) {
			if err := d.allowedLibraryTx(ctx, p, library, tx); err != nil {
				return false, err
			}
			document, err := d.Administration.LibrarySettingsFor(ctx, d.administrationAuthority(p), library)
			return document.Settings.AllowMediaDeletion, err
		}
	}
	return out, nil
}

func (d Dependencies) projectHomeActions(ctx context.Context, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, rows []catalog.HomeRow, spotlight []catalog.HomeSpotlight) error {
	authority, err := d.homeActionAuthority(ctx, p)
	if err != nil {
		return err
	}
	return cat.ProjectHomeActions(viewer, authority, rows, spotlight)
}

// Check the command family against live state after leaving composition. The
// normal Home fence still proves library/content policy; no cached principal
// may preserve offers when its family was revoked during the read snapshot.
func (d Dependencies) homeActionFenceAfter(r *http.Request, p identity.Principal) (string, error) {
	if p.Authority == "local" {
		if err := dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
			_, err := d.Identity.SessionFamilyTx(ctx, dbwork.Snapshot(ctx), p)
			return err
		}); err != nil {
			return "", err
		}
	}
	return d.homeFenceAfter(r, p)
}
