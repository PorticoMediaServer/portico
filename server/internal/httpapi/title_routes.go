package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Owner routes for titles and libraries as things in their own right: the
// list of titles whose files are gone, emptying it, saying two titles are one,
// and removing a library.

type missingRemovalBefore string

func (value *missingRemovalBefore) UnmarshalJSON(raw []byte) error {
	var decoded *string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded == nil || len(*decoded) < 20 || len(*decoded) > 64 {
		return errors.New("before must be a timestamp string")
	}
	if _, err := time.Parse(time.RFC3339Nano, *decoded); err != nil {
		return errors.New("before must be a timestamp string")
	}
	*value = missingRemovalBefore(*decoded)
	return nil
}
func (missingRemovalBefore) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "format": "date-time", "minLength": 20, "maxLength": 64}
}

type emptyMissingRequest struct {
	// Before narrows this explicit removal to absences strictly older than a server list observation.
	Before missingRemovalBefore `json:"before,omitempty"`
	// LibraryID limits the emptying to one library; empty means every library.
	LibraryID string `json:"libraryId,omitempty"`
}

type mergeTitlesRequest struct {
	// FromID is merged into IntoID and removed; IntoID keeps its id.
	FromID string `json:"fromId"`
	IntoID string `json:"intoId"`
	// FileInfo optionally names the exact source member shown on the FileInfo
	// surface. It never changes the merge target.
	FileInfo *catalog.FileInfoActionProof `json:"fileInfo,omitempty"`
}

type mergeTitlesResponse struct {
	ItemID string `json:"itemId"`
}

type separateArtistRequest struct {
	// AlbumIDs are albums of one artist (at most 100) that are by someone
	// else of the same name.
	AlbumIDs []string `json:"albumIds" ref:"title"`
}

type separateArtistResponse struct {
	ArtistID string `json:"artistId"`
}

type removeLibraryRequest struct {
	// Confirmation is the library's name, typed back.
	Confirmation     string `json:"confirmation"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

type removeLibraryResponse struct {
	LibraryID string `json:"libraryId"`
	Removing  bool   `json:"removing"`
}

// removalEnqueue queues background removal work in the caller's transaction.
func (d Dependencies) removalEnqueue(p identity.Principal) func(context.Context, *sql.Tx, string, string) error {
	if d.Scheduler == nil {
		return nil
	}
	return func(ctx context.Context, tx *sql.Tx, kind, resource string) error {
		_, err := d.Scheduler.EnqueueDomainTx(ctx, tx, kind, resource, administrationActor(p))
		return err
	}
}

func identityRouteError(err error) error {
	var public *apierror.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &public):
		return err
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, entityid.ErrNotFound), errors.Is(err, catalog.ErrLibraryRemoving), errors.Is(err, identity.ErrNotVisible):
		return &apierror.Error{Code: "not_found", Cause: err}
	case errors.Is(err, catalog.ErrLibraryConfigurationConflict):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, catalog.ErrConfirmationMismatch):
		return &apierror.Error{Code: "confirmation_mismatch", Cause: err}
	case errors.Is(err, catalog.ErrAdminQuery), errors.Is(err, catalog.ErrNotMergeable):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, identity.ErrForbidden):
		return &apierror.Error{Code: "not_permitted", Cause: err}
	case errors.Is(err, identity.ErrUnauthorized):
		return &apierror.Error{Code: "unauthorized", Cause: err}
	}
	return &apierror.Error{Code: "internal", Cause: err}
}

func registerIdentityRoutes(r *apikit.Registry, d Dependencies) {
	for _, preview := range []bool{true, false} {
		path, id, method, status := "/v1/admin/items/{id}/files/{fileId}", "delete_title_version", "DELETE", 200
		if preview {
			path += "/delete/preview"
			id = "preview_delete_title_version"
			method = "POST"
		}
		meta := apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy, ID: id, Method: method, Path: path, Summary: "Delete one file version while keeping its title and personal state", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, Status: status, BodyLimit: 4096, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_mismatch", "confirmation_mismatch", "administration_unavailable", "stale_continuation", "metadata_unavailable", "internal"}}
		if preview {
			meta.OptionalBody = true
			v1Route(r, meta, func(ctx context.Context, q *http.Request, in versionDeletePreviewRequest) (administration.VersionDeletePreview, error) {
				p, e := d.owner(q)
				if e != nil {
					return administration.VersionDeletePreview{}, e
				}
				if d.Administration == nil {
					return administration.VersionDeletePreview{}, &apierror.Error{Code: "administration_unavailable"}
				}
				authority := d.administrationAuthority(p)
				if in.FileInfo != nil {
					if !validFileInfoActionProof(in.FileInfo) {
						return administration.VersionDeletePreview{}, &apierror.Error{Code: "invalid_request"}
					}
					if d.FileInfoRuntime == nil {
						return administration.VersionDeletePreview{}, &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1}
					}
					scope, e := d.fileInfoActionScope(p, q)
					if e != nil {
						return administration.VersionDeletePreview{}, fileInfoActionError(e)
					}
					authority = d.fileInfoActionAuthority(authority, d.fileInfoActionResource(p), scope, *in.FileInfo, fileInfoActionPathTarget(q.PathValue("id"), q.PathValue("fileId")))
				}
				out, e := d.Administration.PreviewDeleteVersion(ctx, authority, q.PathValue("id"), q.PathValue("fileId"))
				return out, versionDeletionError(e)
			})
		} else {
			v1Route(r, meta, func(ctx context.Context, q *http.Request, in versionDeleteInput) (administration.DeleteResult, error) {
				p, e := d.owner(q)
				if e != nil {
					return administration.DeleteResult{}, e
				}
				if d.Administration == nil {
					return administration.DeleteResult{}, &apierror.Error{Code: "administration_unavailable"}
				}
				authority := d.administrationAuthority(p)
				var selection administration.Authorize
				if in.FileInfo != nil {
					if !validFileInfoActionProof(in.FileInfo) {
						return administration.DeleteResult{}, &apierror.Error{Code: "invalid_request"}
					}
					if d.FileInfoRuntime == nil {
						return administration.DeleteResult{}, &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1}
					}
					scope, e := d.fileInfoActionScope(p, q)
					if e != nil {
						return administration.DeleteResult{}, fileInfoActionError(e)
					}
					selection = d.fileInfoActionAuthority(nil, d.fileInfoActionResource(p), scope, *in.FileInfo, fileInfoActionPathTarget(q.PathValue("id"), q.PathValue("fileId")))
				}
				out, e := d.Administration.DeleteVersionChecked(ctx, authority, selection, q.PathValue("id"), q.PathValue("fileId"), in.VersionDeleteRequest)
				if e == nil && out.Removed > 0 && d.Catalog != nil && d.Catalog.MissingRule(ctx).Mode == "immediately" {
					if _, err := d.Catalog.EmptyMissingDue(ctx); err != nil {
						return out, err
					}
				}
				return out, versionDeletionError(e)
			})
		}
	}

	registerFileInfoActionRoutes(r, d)

	registerStorageStatus(r, d)
	registerDataStorage(r, d)
	registerAdminAccessEvents(r, d)
	registerLiveChoices(r, d)
	registerInventoryRead(r, d)
	registerPlaybackControls(r, d)
	registerDownloadProfileRevoke(r, d)
	registerDownloadProfileCache(r, d)
	ownerErrors := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "internal"}
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "list_missing_titles", Method: "GET", Path: "/v1/admin/missing-titles", Summary: "List titles whose files are no longer on disk, oldest first, with when each will be removed",
		Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"libraryId", "cursor", "limit"}, QueryIntegers: emptyLimitQuery(50, 200), Errors: ownerErrors},
		func(ctx context.Context, q *http.Request, _ noBody) (catalog.MissingTitlesPage, error) {
			limit := 50
			if raw := q.URL.Query().Get("limit"); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil || n < 1 || n > 200 {
					return catalog.MissingTitlesPage{}, &apierror.Error{Code: "invalid_request"}
				}
				limit = n
			}
			p, _ := d.foundationCaller(q)
			library := q.URL.Query().Get("libraryId")
			out, err := d.Catalog.MissingTitlesAuthorized(ctx, library, q.URL.Query().Get("cursor"), limit, d.missingLibraryAuthority(p, library))
			return out, identityRouteError(err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy, ID: "empty_missing_titles", Method: "POST", Path: "/v1/admin/missing-titles/empty", Summary: "Remove missing titles and missing files now, whatever the owner's rule says; call again while more is true",
		Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 4096, OptionalBody: true, Status: 200, Errors: ownerErrors},
		func(ctx context.Context, q *http.Request, in emptyMissingRequest) (catalog.MissingEmptied, error) {
			p, _ := d.foundationCaller(q)
			var total catalog.MissingEmptied
			// A few short transactions per call keeps the request brief; the
			// caller repeats while there is more.
			deadline := time.Now().Add(8 * time.Second)
			cutoff := time.Now()
			if in.Before != "" {
				before, err := time.Parse(time.RFC3339Nano, string(in.Before))
				if err != nil || before.UnixMilli() <= 0 || before.After(cutoff) {
					return total, &apierror.Error{Code: "invalid_request"}
				}
				// Missing timestamps have millisecond precision. Exclude the observed
				// millisecond too; a scan may publish another absence during that tick.
				cutoff = time.UnixMilli(before.UnixMilli() - 1)
			}
			for {
				step, err := d.Catalog.EmptyMissingAuthorized(ctx, in.LibraryID, cutoff, d.missingLibraryAuthority(p, in.LibraryID))
				if err != nil {
					return total, identityRouteError(err)
				}
				total.Titles += step.Titles
				total.Files += step.Files
				total.More = step.More && (step.Titles > 0 || step.Files > 0)
				if !total.More || time.Now().After(deadline) || ctx.Err() != nil {
					return total, nil
				}
			}
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy, ID: "merge_titles", Method: "POST", Path: "/v1/admin/titles/merge", Summary: "Say that two titles of one library are the same title: the first becomes part of the second, with its files as versions (an album, book, artist or author: its songs, files, albums or books) and everyone's state combined",
		Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 4096, Status: 200, Errors: append(append([]string{}, ownerErrors...), "stale_continuation", "metadata_unavailable")},
		func(ctx context.Context, q *http.Request, in mergeTitlesRequest) (mergeTitlesResponse, error) {
			p, e := d.foundationCaller(q)
			if e != nil {
				return mergeTitlesResponse{}, e
			}
			authorize := func(ctx context.Context, tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") }
			if in.FileInfo != nil {
				if !validFileInfoActionProof(in.FileInfo) {
					return mergeTitlesResponse{}, &apierror.Error{Code: "invalid_request"}
				}
				if d.FileInfoRuntime == nil {
					return mergeTitlesResponse{}, &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1}
				}
				scope, e := d.fileInfoActionScope(p, q)
				if e != nil {
					return mergeTitlesResponse{}, fileInfoActionError(e)
				}
				// The FileInfo cohort supplies the exact source member. The merge
				// target selection stays the caller's fromId/intoId; a proof for
				// any other title is a substituted selection, not a merge grant.
				authorize = d.fileInfoActionAuthorityFirst(authorize, d.fileInfoActionResource(p), scope, *in.FileInfo, func(t catalog.FileInfoActionTarget) bool {
					return t.ItemID == in.FromID
				})
			}
			err := d.Catalog.MergeTitles(ctx, in.FromID, in.IntoID, func(tx *sql.Tx) error { return authorize(ctx, tx) })
			if in.FileInfo != nil {
				return mergeTitlesResponse{ItemID: in.IntoID}, fileInfoActionFailure(err)
			}
			return mergeTitlesResponse{ItemID: in.IntoID}, identityRouteError(err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "separate_artist", Method: "POST", Path: "/v1/admin/titles/separate-artist", Summary: "Say that some albums are not by the artist they are filed under: they move, with their songs' credits, to a new artist of the same name that is never joined back automatically; at least one album stays",
		Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 8192, Status: 200, Errors: ownerErrors},
		func(ctx context.Context, q *http.Request, in separateArtistRequest) (separateArtistResponse, error) {
			p, _ := d.foundationCaller(q)
			artist, err := d.Catalog.SeparateArtist(ctx, in.AlbumIDs, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
			return separateArtistResponse{ArtistID: artist}, identityRouteError(err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "remove_library", Method: "DELETE", Path: "/v1/admin/libraries/{id}", Summary: "Remove a library: it disappears at once and is taken apart in the background; files on disk are not touched and each profile's state is kept in case its titles return",
		Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 4096, Status: 202, Errors: append([]string{"revision_mismatch", "confirmation_mismatch", "administration_unavailable"}, ownerErrors...)},
		func(ctx context.Context, q *http.Request, in removeLibraryRequest) (removeLibraryResponse, error) {
			p, _ := d.foundationCaller(q)
			id := q.PathValue("id")
			enqueue := d.removalEnqueue(p)
			if enqueue == nil {
				return removeLibraryResponse{}, &apierror.Error{Code: "administration_unavailable"}
			}
			err := d.Catalog.RemoveLibrary(ctx, id, in.Confirmation, in.ExpectedRevision, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(ctx, tx, p, id) }, enqueue)
			if err != nil {
				return removeLibraryResponse{}, identityRouteError(err)
			}
			if d.Ingestion != nil {
				d.Ingestion.InterruptLibrary(ctx, id)
			}
			return removeLibraryResponse{LibraryID: id, Removing: true}, nil
		})
}

func versionDeletionError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, administration.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found", Cause: err}
	case errors.Is(err, administration.ErrDenied):
		return &apierror.Error{Code: "not_permitted", Cause: err}
	case errors.Is(err, administration.ErrCursor):
		return &apierror.Error{Code: "invalid_cursor", Cause: err}
	case errors.Is(err, administration.ErrInput):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, administration.ErrConflict), errors.Is(err, administration.ErrIdempotencyKeyReused):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, administration.ErrConfirmation):
		return &apierror.Error{Code: "confirmation_mismatch", Cause: err}
	}
	return err
}
