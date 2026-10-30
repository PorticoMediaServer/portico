package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"strconv"
)

func (d Dependencies) detailAccess(r *http.Request) (identity.Principal, string, string, error) {
	p, e := d.principal(r)
	if e != nil {
		return p, "", "", e
	}
	if e = d.restrictedItem(r, p, r.PathValue("id")); e != nil {
		return p, "", "", e
	}
	library, fence, e := d.detailFenceFor(r, p)
	return p, library, fence, e
}

// detailFenceFor is detailAccess with the principal already resolved. The
// post-read proof uses it: a detail page is re-authorised, not re-composed.
func (d Dependencies) detailFenceFor(r *http.Request, p identity.Principal) (string, string, error) {
	library, e := d.Catalog.WithContext(r.Context()).LibraryForItem(r.PathValue("id"))
	if e != nil {
		return "", "", e
	}
	return d.detailLibraryFenceFor(r, p, library)
}

func (d Dependencies) detailLibraryFenceFor(r *http.Request, p identity.Principal, library string) (string, string, error) {
	if e := d.allowedLibrary(r.Context(), p, library); e != nil {
		return "", "", e
	}
	var policy, restriction int64
	if p.Authority != "local" {
		e := d.DB.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), p.ProfileID).Scan(&policy, &restriction)
		if e != nil {
			return "", "", e
		}
	}
	_, contentFence, e := d.viewerRestrictions(r, p)
	if e != nil {
		return "", "", e
	}
	fence := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d:%s", presentationIdentity(p), p.Epoch, policy, restriction, contentFence))))
	return library, fence, nil
}

// markerAccess builds the analysis access for a viewer read. Authorization is
// the same library decision the rest of this request already made, re-run inside
// the marker transaction so a revoked viewer cannot read segments from a cached
// route decision.
func (d Dependencies) markerAccess(p identity.Principal, library, item, fence string) mediaanalysis.Access {
	return mediaanalysis.Access{
		ServerID: d.Identity.ID(), LibraryID: library, ItemID: item, ViewerFence: fence,
		Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, SessionHash: p.Hash,
		Owner:     p.Authority == "local" && p.Role == "owner",
		Authorize: func(tx *sql.Tx) error { return d.allowedLibraryLegacyTx(p, library, tx) },
	}
}
func (d Dependencies) detailRoutes(mux *http.ServeMux) {
	personalRate := &personalLimiter{}
	d.projectedHandle(mux, "GET /v1/items/{id}/detail", d.detailReadHandler)
	// A title's cast or crew, complete, by cursor (the detail carries the first
	// page of the cast and the key crew).
	d.httpHandle(mux, "GET /v1/items/{id}/credits", func(w http.ResponseWriter, r *http.Request) {
		p, library, fence, e := d.detailAccess(r)
		if e != nil {
			failure(w, e)
			return
		}
		restrictions, _, e := d.viewerRestrictions(r, p)
		if e != nil {
			failure(w, e)
			return
		}
		viewer, e := d.catalogViewer(r, p, []string{library}, fence)
		if e != nil {
			failure(w, e)
			return
		}
		q := r.URL.Query()
		// An absent or out-of-range limit is the default page (as other lists).
		limit, _ := strconv.Atoi(q.Get("limit"))
		var out catalog.CreditPage
		e = d.compose(r, restrictions, func(cat *catalog.Service) error {
			var composeErr error
			out, composeErr = cat.ItemCredits(viewer, r.PathValue("id"), q.Get("group"), q.Get("cursor"), limit)
			return composeErr
		})
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.projectedHandle(mux, "PUT /v1/items/{id}/personal-state", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		if receipt, missing, err := d.personalMutationReplay(r.Context(), p); err != nil {
			failure(w, err)
			return
		} else if receipt != nil && missing {
			write(w, 200, catalog.PersonalReceipt{ServerID: d.Identity.ID(), OperationID: receipt.Key, ItemID: receipt.TargetID, Personal: catalog.PersonalState{Revision: receipt.Revision, Status: "current", Conflicts: []catalog.PersonalConflict{}}, ReceiptLifetimeMs: 2592000000})
			return
		}
		p, library, originalFence, e := d.detailAccess(r)
		if e != nil {
			failure(w, e)
			return
		}
		if !personalRate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many personal-state requests. Try again shortly.", "retryable": true}})
			return
		}
		var body catalog.PersonalMutation
		if e = decodeSavedRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		personal, e := d.Catalog.WithContext(r.Context()).SetPersonal(p.Authority+":"+p.AccountID, identity.PersonalKey(p.Viewer), r.PathValue("id"), body, func(tx *sql.Tx) error {
			var actualLibrary string
			if e := tx.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, r.PathValue("id")).Scan(&actualLibrary); e != nil {
				return e
			}
			if actualLibrary != library {
				return identity.ErrNotVisible
			}
			return d.resourceAuthorize(tx, p, library)
		})
		mutationErr := e
		_, currentLibrary, fence, e := d.detailAccess(r)
		if e != nil {
			failure(w, e)
			return
		}
		if fence != originalFence {
			failure(w, catalog.ErrStaleContinuation)
			return
		}
		current, e := d.Catalog.WithContext(r.Context()).Personal(identity.PersonalKey(p.Viewer), r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		if mutationErr != nil {
			code := ""
			switch {
			case errors.Is(mutationErr, catalog.ErrPersonalConflict):
				code = "personal_state_conflict"
			case errors.Is(mutationErr, catalog.ErrPersonalResolution):
				code = "personal_needs_resolution"
			case errors.Is(mutationErr, catalog.ErrPersonalReview):
				code = "personal_needs_review"
			case errors.Is(mutationErr, catalog.ErrOperationExpired):
				code = "operation_expired"
			}
			if code != "" {
				write(w, 409, map[string]any{"error": map[string]any{"code": code, "message": mutationErr.Error(), "retryable": false}, "serverId": d.Identity.ID(), "viewerFence": fence, "itemId": r.PathValue("id"), "current": current})
				return
			}
			failure(w, mutationErr)
			return
		}
		write(w, 200, catalog.PersonalReceipt{ServerID: d.Identity.ID(), OperationID: body.OperationID, ItemID: r.PathValue("id"), LibraryID: currentLibrary, ViewerFence: fence, Personal: personal, Current: &current, ReceiptLifetimeMs: 2592000000})
	})
}

func (d Dependencies) detailReadHandler(w http.ResponseWriter, r *http.Request) {
	p, library, fence, e := d.detailContentAccess(r)
	if e != nil {
		failure(w, e)
		return
	}
	r, ready, proceed := d.prepareFileInfoDocument(w, r, "detail")
	if !proceed {
		return
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	viewer, e := d.catalogViewer(r, p, []string{library}, fence)
	if e != nil {
		failure(w, e)
		return
	}
	var out catalog.Detail
	var markerRevision string
	e = d.compose(r, restrictions, func(cat *catalog.Service) error {
		var composeErr error
		var readingKind int
		if err := dbwork.Snapshot(cat.Context()).QueryRowContext(cat.Context(), `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, r.PathValue("id")).Scan(&readingKind); err != nil {
			return err
		}
		if readingKind == 20 || readingKind == 21 {
			out, composeErr = cat.ReadingDetail(viewer, d.Identity.ID(), r.PathValue("id"), d.savedReadingGroupsAdmission(p, viewer))
			if composeErr != nil {
				return composeErr
			}
			authority, err := d.homeActionAuthority(cat.Context(), p)
			if err != nil {
				return err
			}
			if authority.ReadingCollections {
				out.Actions = append(out.Actions, catalog.DetailAction{ID: "add_to_collection", LabelKey: "action.add_to_collection", Enabled: true})
			}
			return nil
		}
		out, composeErr = cat.DetailWithRelated(viewer, d.Identity.ID(), r.PathValue("id"), p.Authority == "local" && p.Role == "owner", r.URL.Query().Get("related") != "none")
		if composeErr != nil {
			return composeErr
		}
		markers, markerErr := mediaanalysis.ViewerMarkers(cat.Context(), d.DB, d.markerAccess(p, library, r.PathValue("id"), fence), "")
		if markerErr != nil {
			return markerErr
		}
		out.Markers = markers.Markers
		// Queue admission is resolved independently in this same read snapshot.
		// A readable/playable title does not itself grant device commands.
		authority, authorityErr := d.homeActionAuthority(cat.Context(), p)
		if authorityErr != nil {
			return authorityErr
		}
		if authority.Queue && out.Item.Available && (out.Item.Kind == "movie" || out.Item.Kind == "episode" || out.Item.Kind == "song" || out.Item.Kind == "audiobook_file") {
			zero := float64(0)
			out.Actions = append(out.Actions, catalog.DetailAction{ID: "enqueue", LabelKey: "entry.addToQueue", Enabled: true,
				Playback: &catalog.ContentPlayback{ItemID: out.Item.ID, StartSeconds: &zero}})
		}
		if ready != nil {
			token, err := d.FileInfoRuntime.AcceptPreparedOpening(cat.Context(), ready.caller, *ready.ready, out.Revision)
			if err != nil {
				return err
			}
			out.FileInfoOpening = token
		}
		var err error
		markerRevision, err = mediaanalysis.ViewerMarkerRevision(cat.Context(), d.DB, d.markerAccess(p, library, r.PathValue("id"), fence), "")
		return err
	})
	if e != nil {
		failure(w, e)
		return
	}
	_, after, e := d.detailContentFenceFor(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	if after != fence {
		failure(w, catalog.ErrStaleContinuation)
		return
	}
	if out.Item.Kind == "movie" || out.Item.Kind == "episode" || out.Item.Kind == "song" || out.Item.Kind == "audiobook_file" {
		out.Actions = append(out.Actions, catalog.DetailAction{ID: "add_to_playlist", LabelKey: "action.add_to_playlist", Enabled: true}, catalog.DetailAction{ID: "add_to_collection", LabelKey: "action.add_to_collection", Enabled: true})
	}
	if identity.Administrative(p.Role) && d.Metadata != nil && out.Item.Kind == "movie" {
		out.Actions = append(out.Actions, catalog.DetailAction{ID: "metadata_refresh", LabelKey: "action.metadata_refresh", Enabled: true})
	}
	if p.Role == identity.TierAdmin && d.Metadata != nil && (out.Item.Kind == "movie" || out.Item.Kind == "episode" || out.Item.Kind == "song" || out.Item.Kind == "audiobook_file") {
		out.Actions = append(out.Actions, catalog.DetailAction{ID: "metadata_edit", LabelKey: "action.metadata_edit", Enabled: true})
	}
	// Earlier clients validate related rows as movie-only. New clients opt in
	// so a server update cannot make their song/book detail reads fail.
	supportedRelated := out.Item.Kind == "movie" || out.Item.Kind == "episode" || out.Item.Kind == "song" || out.Item.Kind == "audiobook_file"
	if !supportedRelated || (out.Item.Kind != "movie" && r.URL.Query().Get("related") != "all") {
		out.Related = nil
	}
	if conditional(w, r, responseTag("detail", r.PathValue("id"), fence, number(out.Revision.Catalog), number(out.Revision.Viewer), markerRevision, r.URL.Query().Get("related"), out.FileInfoOpening)) {
		return
	}
	write(w, 200, out)
}
