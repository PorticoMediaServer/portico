package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Exact-occurrence action hooks.
//
// FileInfo supplies a supplementary proof of the surface the owner selected. It
// is never a grant: every hook re-runs the command's live owner/resource
// authority and the command's own selection, custody and confirmation checks.
// The proof is resolved once for a point target and again inside the accepting
// transaction through dbwork.BindSnapshot, so a mutation cannot commit against
// a different link, asset or title than the one the caller saw.

type versionDeletePreviewRequest struct {
	// FileInfo optionally names the exact member shown on the FileInfo surface.
	FileInfo *catalog.FileInfoActionProof `json:"fileInfo,omitempty"`
}

// versionDeleteInput embeds the administration DTO so the administration
// package keeps no dependency on the catalogue. encoding/json promotes the
// embedded fields, preserving the existing confirmation/operationId/previewKey
// body for clients that know nothing about FileInfo.
type versionDeleteInput struct {
	administration.VersionDeleteRequest
	FileInfo *catalog.FileInfoActionProof `json:"fileInfo,omitempty"`
}

type fileInfoSeparateRequest struct {
	FileInfo *catalog.FileInfoActionProof `json:"fileInfo"`
}

type fileInfoSeparateResponse struct {
	ItemID string `json:"itemId"`
}

type fileInfoActionScope struct {
	caller readprep.Caller
	guard  catalog.FileInfoAuthorityCheck
}

func validFileInfoActionProof(proof *catalog.FileInfoActionProof) bool {
	return proof != nil &&
		fileInfoTextToken(proof.Opening, fileInfoOpeningText) &&
		fileInfoTextToken(proof.Occurrence, fileInfoOccurrenceText)
}

func (d Dependencies) fileInfoActionScope(p identity.Principal, r *http.Request) (fileInfoActionScope, error) {
	if d.Catalog == nil {
		return fileInfoActionScope{}, catalog.ErrFileInfoAuthorityUnavailable
	}
	caller, err := d.fileInfoCaller(r.Context(), p)
	if err != nil {
		return fileInfoActionScope{}, err
	}
	return fileInfoActionScope{caller: caller, guard: d.fileInfoAuthorityCheck(p, caller)}, nil
}

// resolveFileInfoAction is the point resolution used to learn the exact target
// before a command chooses its own selection. It is not trusted on its own.
func (d Dependencies) resolveFileInfoAction(ctx context.Context, scope fileInfoActionScope, proof catalog.FileInfoActionProof) (catalog.FileInfoActionTarget, error) {
	if d.FileInfoRuntime == nil {
		return catalog.FileInfoActionTarget{}, catalog.ErrFileInfoAuthorityUnavailable
	}
	return d.FileInfoRuntime.ResolveAction(catalog.WithFileInfoAuthority(ctx, scope.guard), scope.caller, proof)
}

// reproveFileInfoAction repeats the same proof inside the command's accepting
// transaction. BindSnapshot carries that exact transaction into the runtime's
// reads; the captured guard is reinstalled because it must also rebuild live
// session, profile, library and owner facts from the same snapshot.
func (d Dependencies) reproveFileInfoAction(ctx context.Context, tx *sql.Tx, scope fileInfoActionScope, proof catalog.FileInfoActionProof) (catalog.FileInfoActionTarget, error) {
	if d.FileInfoRuntime == nil {
		return catalog.FileInfoActionTarget{}, catalog.ErrFileInfoAuthorityUnavailable
	}
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return catalog.FileInfoActionTarget{}, err
	}
	return d.FileInfoRuntime.ResolveAction(catalog.WithFileInfoAuthority(bound, scope.guard), scope.caller, proof)
}

// fileInfoActionAuthority wraps the command's existing Authorize callback. The
// proof is checked only after the command's own authority has passed, then the
// resolved library's live resource access is rechecked, and the exact-target
// predicate decides whether the resolved link is the one this route/body is
// allowed to act on.
func (d Dependencies) fileInfoActionAuthority(base administration.Authorize, resource func(context.Context, *sql.Tx, string) error, scope fileInfoActionScope, proof catalog.FileInfoActionProof, exact func(catalog.FileInfoActionTarget) bool) administration.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		if base != nil {
			if err := base(ctx, tx); err != nil {
				return err
			}
		}
		target, err := d.reproveFileInfoAction(ctx, tx, scope, proof)
		if err != nil {
			return fileInfoActionError(err)
		}
		if resource != nil {
			if err := resource(ctx, tx, target.LibraryID); err != nil {
				return err
			}
		}
		if !exact(target) {
			return &apierror.Error{Code: "invalid_request"}
		}
		return nil
	}
}

func (d Dependencies) fileInfoActionResource(p identity.Principal) func(context.Context, *sql.Tx, string) error {
	return func(ctx context.Context, tx *sql.Tx, library string) error {
		return d.allowedLibraryTx(ctx, p, library, tx)
	}
}

// fileInfoActionAuthorityFirst is for a command that walks several bounded
// mutation steps in one request. The proof is the selection the caller accepted
// before the first step; later steps keep rechecking the command's own live
// authority but cannot re-prove a link the earlier steps legitimately moved.
func (d Dependencies) fileInfoActionAuthorityFirst(base administration.Authorize, resource func(context.Context, *sql.Tx, string) error, scope fileInfoActionScope, proof catalog.FileInfoActionProof, exact func(catalog.FileInfoActionTarget) bool) administration.Authorize {
	proven := false
	var library string
	return func(ctx context.Context, tx *sql.Tx) error {
		if base != nil {
			if err := base(ctx, tx); err != nil {
				return err
			}
		}
		if !proven {
			target, err := d.reproveFileInfoAction(ctx, tx, scope, proof)
			if err != nil {
				return fileInfoActionError(err)
			}
			if !exact(target) {
				return &apierror.Error{Code: "invalid_request"}
			}
			library = target.LibraryID
		}
		if resource != nil {
			if err := resource(ctx, tx, library); err != nil {
				return err
			}
		}
		proven = true
		return nil
	}
}

func exactFileInfoTarget(want catalog.FileInfoActionTarget) func(catalog.FileInfoActionTarget) bool {
	return func(got catalog.FileInfoActionTarget) bool {
		return got.Item == want.Item && got.Asset == want.Asset &&
			got.ItemID == want.ItemID && got.AssetID == want.AssetID && got.LibraryID == want.LibraryID
	}
}

func fileInfoActionPathTarget(item, asset string) func(catalog.FileInfoActionTarget) bool {
	return func(got catalog.FileInfoActionTarget) bool {
		return got.ItemID == item && got.AssetID == asset
	}
}

// fileInfoActionError maps only the proof/runtime sentinels. Command errors keep
// their existing meaning and mapping.
func fileInfoActionError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, readprep.ErrInvalid):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, readprep.ErrStale), errors.Is(err, catalog.ErrStaleContinuation), errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "stale_continuation", Cause: err}
	case errors.Is(err, readprep.ErrCapacity), errors.Is(err, readprep.ErrClosed), errors.Is(err, readprep.ErrUncached), errors.Is(err, catalog.ErrFileInfoAuthorityUnavailable):
		return &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1, Cause: err}
	}
	return err
}

// fileInfoActionFailure finishes a resolved command error. Source custody keeps
// its existing refusal; its code is the registered 409 form used by the admin
// family.
func fileInfoActionFailure(err error) error {
	err = fileInfoActionError(err)
	if err == nil {
		return nil
	}
	var public *apierror.Error
	if errors.As(err, &public) {
		return err
	}
	if errors.Is(err, catalog.ErrSourceBusy) || errors.Is(err, catalog.ErrSourceChanged) {
		return &apierror.Error{Code: "source_changed", Reason: "source_busy", Cause: err}
	}
	return identityRouteError(err)
}

func (d Dependencies) fileInfoSeparateAction(ctx context.Context, q *http.Request, in fileInfoSeparateRequest) (fileInfoSeparateResponse, error) {
	p, err := d.owner(q)
	if err != nil {
		return fileInfoSeparateResponse{}, err
	}
	if !validFileInfoActionProof(in.FileInfo) {
		return fileInfoSeparateResponse{}, &apierror.Error{Code: "invalid_request"}
	}
	if d.FileInfoRuntime == nil || d.Catalog == nil {
		return fileInfoSeparateResponse{}, &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1}
	}
	scope, err := d.fileInfoActionScope(p, q)
	if err != nil {
		return fileInfoSeparateResponse{}, fileInfoActionError(err)
	}
	proof := *in.FileInfo
	target, err := d.resolveFileInfoAction(ctx, scope, proof)
	if err != nil {
		return fileInfoSeparateResponse{}, fileInfoActionError(err)
	}
	if target.ItemID != q.PathValue("id") {
		// The opening may legitimately name a parent, but the resolved file must
		// still be the member the route names.
		return fileInfoSeparateResponse{}, &apierror.Error{Code: "invalid_request"}
	}
	base := d.administrationAuthority(p)
	resource := d.fileInfoActionResource(p)
	item, err := d.Catalog.SeparateFileInfoVersion(ctx, target, d.fileInfoActionAuthority(base, resource, scope, proof, exactFileInfoTarget(target)))
	if err != nil {
		return fileInfoSeparateResponse{}, fileInfoActionFailure(err)
	}
	return fileInfoSeparateResponse{ItemID: item}, nil
}

func registerFileInfoActionRoutes(r *apikit.Registry, d Dependencies) {
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "metadata_unavailable", "source_changed", "internal"}
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy, ID: "separate_item_file_info", Method: "POST", Path: "/v1/admin/items/{id}/file-info/separate",
		Summary: "Make one exact file version its own title; the file keeps its facts and the title it leaves keeps its other versions and personal state",
		Access:  apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 4096, Status: 200, Errors: failures},
		func(ctx context.Context, q *http.Request, in fileInfoSeparateRequest) (fileInfoSeparateResponse, error) {
			return d.fileInfoSeparateAction(ctx, q, in)
		})
}
