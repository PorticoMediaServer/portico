package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Bounded FileInfo lanes.
//
// Every lane is one ready window or the shared preparation carrier. The runtime
// owns the private readprep handles, continuation cursors and opening proof; this
// adapter owns the finite route parameter sets, the 200/202/304 response
// semantics, live authority reconstruction and ETags.
//
// The token widths below mirror the catalogue codecs. They are checked before a
// token is handed to the runtime so an attacker-sized value is never decoded:
//   - opening: 183 characters (an authenticated original Detail capture)
//   - cursor:  154 characters (a signed lane continuation)
//   - occurrence/group/around: 271 characters (an authenticated exact locator)
//   - query id: 338 characters, one 183-character opening, '.', one 154-character
//     anchor
const (
	fileInfoOpeningText    = 183
	fileInfoCursorText     = 154
	fileInfoOccurrenceText = 271
	fileInfoQueryIDText    = 338
)

// fileInfoLaneSpec is the finite parameter set of one registered lane. It is not
// a query language: each route accepts exactly the named inputs.
type fileInfoLaneSpec struct {
	kind       string
	group      bool
	occurrence bool
	lane       bool
	around     bool
}

func fileInfoTextToken(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validFileInfoQueryID(id string) bool {
	// An opening preparation handle names the same-store opening namespace. Its
	// exact payload belongs to the runtime; the adapter accepts only a bounded
	// path-safe form so a giant or traversal-shaped value is refused before use.
	if rest, ok := strings.CutPrefix(id, "open."); ok {
		if len(rest) == 0 || len(id) > 512 {
			return false
		}
		for i := 0; i < len(rest); i++ {
			c := rest[i]
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
		return true
	}
	if len(id) != fileInfoQueryIDText || id[fileInfoOpeningText] != '.' {
		return false
	}
	return fileInfoTextToken(id[:fileInfoOpeningText], fileInfoOpeningText) &&
		fileInfoTextToken(id[fileInfoOpeningText+1:], fileInfoCursorText)
}

// fileInfoLimit reads the one declared integer input. An absent or empty value
// keeps the runtime default and, on a continuation, inherits the original
// window's limit. An explicit zero, a syntax error or an out-of-range value is a
// 400 rather than a silent substitution.
func fileInfoLimit(query url.Values) (int, bool, error) {
	if !query.Has("limit") {
		return 0, false, nil
	}
	raw := query.Get("limit")
	if raw == "" {
		return 0, false, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > catalog.FileInfoMaximumLimit {
		return 0, false, readprep.ErrInvalid
	}
	return limit, true, nil
}

// fileInfoLaneRequest validates one route's finite inputs and builds the runtime
// descriptor. Unknown parameters are already refused by the registry.
func fileInfoLaneRequest(r *http.Request, spec fileInfoLaneSpec) (catalog.FileInfoReadRequest, error) {
	query := r.URL.Query()
	for _, values := range query {
		if len(values) != 1 {
			return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
		}
	}
	request := catalog.FileInfoReadRequest{Kind: spec.kind, Cursor: query.Get("cursor")}
	if !fileInfoTextToken(query.Get("opening"), fileInfoOpeningText) {
		return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
	}
	request.Opening = query.Get("opening")
	if request.Cursor != "" && !fileInfoTextToken(request.Cursor, fileInfoCursorText) {
		return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
	}
	if spec.around {
		request.Around = query.Get("around")
		if request.Around != "" && !fileInfoTextToken(request.Around, fileInfoOccurrenceText) {
			return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
		}
	}
	if spec.group {
		request.Group = query.Get("group")
		if !fileInfoTextToken(request.Group, fileInfoOccurrenceText) {
			return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
		}
	}
	if spec.occurrence {
		request.Occurrence = query.Get("occurrence")
		if !fileInfoTextToken(request.Occurrence, fileInfoOccurrenceText) {
			return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
		}
	}
	if spec.lane {
		request.Lane = query.Get("lane")
		if request.Lane != "audio" && request.Lane != "subtitles" {
			return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
		}
	}
	if request.Around != "" && request.Cursor != "" {
		// An explicit re-anchor and a continuation are different requests.
		return catalog.FileInfoReadRequest{}, readprep.ErrInvalid
	}
	limit, present, err := fileInfoLimit(query)
	if err != nil {
		return catalog.FileInfoReadRequest{}, err
	}
	request.Limit, request.LimitPresent = limit, present
	return request, nil
}

// fileInfoFailure maps the runtime's raw sentinels to the frozen FileInfo error
// table. Anything the shared writer already knows (401/403/404, no rows, a
// revoked family) is preserved.
func fileInfoFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, readprep.ErrInvalid), errors.Is(err, catalog.ErrCursor), requestinput.Is(err):
		failure(w, requestinput.New("Choose a valid file information window."))
	case errors.Is(err, readprep.ErrStale):
		failure(w, catalog.ErrStaleContinuation)
	case errors.Is(err, compactcatalog.ErrInfoPreparing), errors.Is(err, readprep.ErrCapacity), errors.Is(err, readprep.ErrClosed),
		errors.Is(err, readprep.ErrUncached), errors.Is(err, catalog.ErrFileInfoAuthorityUnavailable):
		fileInfoUnavailable(w, err)
	default:
		failure(w, err)
	}
}

// fileInfoUnavailable answers temporary capacity, a closed store or a missing
// runtime. It never degrades to an empty page.
func fileInfoUnavailable(w http.ResponseWriter, cause error) {
	policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "metadata_unavailable", RetryAfterSeconds: 1, Cause: cause})
}

func (d Dependencies) fileInfoLaneHandler(spec fileInfoLaneSpec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		item := r.PathValue("id")
		if _, ok := entityid.Decode(item); !ok {
			fileInfoFailure(w, sql.ErrNoRows)
			return
		}
		request, err := fileInfoLaneRequest(r, spec)
		if err != nil {
			fileInfoFailure(w, err)
			return
		}
		if d.FileInfoRuntime == nil {
			fileInfoUnavailable(w, catalog.ErrFileInfoAuthorityUnavailable)
			return
		}
		// Issuing one belongs to the live owner alone. The runtime rechecks the
		// same fact inside the lane; refusing here keeps a known admitted surface
		// from answering 409 where the frozen table says 403.
		if spec.kind == "disc-titles" {
			if _, err = d.owner(r); err != nil {
				fileInfoFailure(w, err)
				return
			}
		}
		ctx, caller, err := d.fileInfoContext(r)
		if err != nil {
			fileInfoFailure(w, err)
			return
		}
		// The guard deliberately does not bind the route path: an action may
		// legitimately name a parent album opening beside a child member path.
		// Lane reads are item-bound, so the route checks the opening's public
		// item against its own path here.
		if err = d.FileInfoRuntime.MatchOpeningItem(caller, request.Opening, item); err != nil {
			fileInfoFailure(w, err)
			return
		}
		out, err := d.FileInfoRuntime.Read(ctx, caller, request)
		if err != nil {
			out.Release()
			fileInfoFailure(w, err)
			return
		}
		d.writeFileInfoWindow(w, r, out)
	}
}

func (d Dependencies) fileInfoQueryHandler(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 || !validFileInfoQueryID(r.PathValue("queryId")) {
		fileInfoFailure(w, readprep.ErrInvalid)
		return
	}
	if d.FileInfoRuntime == nil {
		fileInfoUnavailable(w, catalog.ErrFileInfoAuthorityUnavailable)
		return
	}
	ctx, caller, err := d.fileInfoContext(r)
	if err != nil {
		fileInfoFailure(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		// Cancel is idempotent and private: an unknown, foreign or already
		// retired handle and a repeated cancel all answer 204.
		if err = d.FileInfoRuntime.Cancel(ctx, caller, r.PathValue("queryId")); err != nil {
			fileInfoFailure(w, err)
			return
		}
		write(w, http.StatusNoContent, nil)
		return
	}
	out, err := d.FileInfoRuntime.Poll(ctx, caller, r.PathValue("queryId"))
	if err != nil {
		out.Release()
		fileInfoFailure(w, err)
		return
	}
	d.writeFileInfoWindow(w, r, out)
}

// writeFileInfoWindow serialises exactly one ready page or the preparation
// carrier. The ready page's lease stays held through serialization and is
// released after the bytes are written. A preparation never carries an ETag,
// and a ready 304 is only decided after the runtime's live authority and FI1
// readiness checks have already run.
func (d Dependencies) writeFileInfoWindow(w http.ResponseWriter, r *http.Request, out catalog.FileInfoWindowResult) {
	defer out.Release()
	if out.Opening != nil {
		// A ready Get/Detail whose opening was minted in the accepted read
		// snapshot. The parent owns that body and its conditional validator; it
		// must compose and mint before any 304 decision.
		d.writeFileInfoOpening(w, r, out)
		return
	}
	if out.Preparing != nil {
		preparing := out.Preparing
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Location", "/v1/file-info-queries/"+preparing.QueryID)
		w.Header().Set("Retry-After", strconv.Itoa(max(1, (preparing.RetryAfterMs+999)/1000)))
		write(w, http.StatusAccepted, preparing)
		return
	}
	page, err := fileInfoReadyPage(out)
	if err != nil {
		failure(w, err)
		return
	}
	raw, err := json.Marshal(page)
	if err != nil {
		failure(w, err)
		return
	}
	sum := sha256.Sum256(raw)
	tag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("Cache-Control", revalidate)
	w.Header().Set("ETag", tag)
	if matchesETag(r.Header.Get("If-None-Match"), tag) {
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func fileInfoReadyPage(out catalog.FileInfoWindowResult) (any, error) {
	pages := make([]any, 0, 7)
	if out.Files != nil {
		pages = append(pages, out.Files)
	}
	if out.Groups != nil {
		pages = append(pages, out.Groups)
	}
	if out.Members != nil {
		pages = append(pages, out.Members)
	}
	if out.Tracks != nil {
		pages = append(pages, out.Tracks)
	}
	if out.HeadTracks != nil {
		pages = append(pages, out.HeadTracks)
	}
	if out.Sources != nil {
		pages = append(pages, out.Sources)
	}
	if out.Disc != nil {
		pages = append(pages, out.Disc)
	}
	if len(pages) != 1 || out.Preparing != nil {
		return nil, errors.New("invalid file information window result")
	}
	return pages[0], nil
}

// fileInfoContext is the reusable live authority installation for this family.
// Get, Detail and the opening preparation call it too, so an opening minted
// there carries the same private caller the lanes replay.
//
// It captures only the authenticated principal and the derived caller. The guard
// closure never holds the request, its body, a route item, a request snapshot or
// a transaction. Each invocation reconstructs the live session family, profile,
// library grants, owner role, the original Detail fence and the FileInfo audience
// before returning admission. The shared runtime invokes that guard before and
// after every ready or pending response, and the lane worker calls it through the
// captured dispatcher; no current content revision is consulted.
func (d Dependencies) fileInfoContext(r *http.Request) (context.Context, readprep.Caller, error) {
	p, err := d.principal(r)
	if err != nil {
		return nil, "", err
	}
	caller, err := d.fileInfoCaller(r.Context(), p)
	if err != nil {
		return nil, "", err
	}
	guard := d.fileInfoAuthorityCheck(p, caller)
	return catalog.WithFileInfoAuthority(r.Context(), guard), caller, nil
}

func (d Dependencies) fileInfoCaller(ctx context.Context, p identity.Principal) (readprep.Caller, error) {
	var caller readprep.Caller
	err := dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
		var err error
		caller, _, err = d.fileInfoCallerTx(ctx, p)
		return err
	})
	return caller, err
}

// fileInfoCallerTx derives the private FileInfo caller from live session and
// audience facts. It deliberately does not include the access-token hash: the
// session family survives token rotation, while the family, principal
// presentation, epoch and live library/policy fence do not.
func (d Dependencies) fileInfoCallerTx(ctx context.Context, p identity.Principal) (readprep.Caller, string, error) {
	tx := dbwork.Snapshot(ctx)
	if d.Identity == nil || tx == nil {
		return "", "", identity.ErrUnauthorized
	}
	family, err := d.Identity.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return "", "", err
	}
	// homeScopeFor only reads the request's context, so a minimal context-only
	// request stands in for the original without capturing it.
	live := (&http.Request{}).WithContext(ctx)
	_, fence, err := d.homeScopeFor(live, p)
	if err != nil {
		return "", "", err
	}
	raw, err := json.Marshal([]any{"file-info-v1", p.ServerID, family.ID, presentationIdentity(p), p.Epoch, fence})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return readprep.Caller(hex.EncodeToString(sum[:])), fence, nil
}

func (d Dependencies) fileInfoAuthorityCheck(p identity.Principal, caller readprep.Caller) catalog.FileInfoAuthorityCheck {
	return func(ctx context.Context, actual readprep.Caller, id catalog.FileInfoIdentity) (catalog.FileInfoAdmission, error) {
		if actual != caller {
			// A continuation, opening or query handle is private to the caller
			// that minted it.
			return catalog.FileInfoAdmission{}, readprep.ErrStale
		}
		public := entityid.Encode(id.PublicItem[:])
		if public == "" {
			return catalog.FileInfoAdmission{}, sql.ErrNoRows
		}
		// The Detail audience and the FileInfo audience are separate. The
		// opening must echo the Detail's own accepted scope, so rebuild that
		// fence exactly as the Detail page did (before composing, outside any
		// read snapshot). The caller and FileInfo witness stay on the home
		// audience below.
		library, _, err := d.Catalog.WithContext(ctx).LibraryForDetail(public)
		if err != nil {
			return catalog.FileInfoAdmission{}, err
		}
		detailFence, err := d.fileInfoDetailFence(ctx, p, library)
		if err != nil {
			return catalog.FileInfoAdmission{}, err
		}
		var admission catalog.FileInfoAdmission
		err = dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
			liveCaller, fence, err := d.fileInfoCallerTx(ctx, p)
			if err != nil {
				return err
			}
			if liveCaller != caller {
				return readprep.ErrStale
			}
			tx := dbwork.Snapshot(ctx)
			if tx == nil {
				return sql.ErrNoRows
			}
			owner := false
			switch err := d.ownerAuthorityTx(ctx, tx, p); {
			case err == nil:
				owner = true
			case errors.Is(err, identity.ErrForbidden):
				// A live non-owner is admitted as a viewer, never as an owner.
			default:
				return err
			}
			if err := d.allowedLibraryTx(ctx, p, library, tx); err != nil {
				return err
			}
			live := (&http.Request{}).WithContext(ctx)
			viewer, err := d.catalogViewer(live, p, []string{library}, fence)
			if err != nil {
				return err
			}
			actor := subtitles.ViewerKey(p.Authority, p.AccountID, p.ProfileID, p.ServerID)
			discSubtitles := false
			if d.Subtitles != nil {
				_, discSubtitles = d.Subtitles.Extractor.(subtitles.DiscSubtitleExtractor)
			}
			admission, err = d.Catalog.WithContext(ctx).CaptureFileInfoAdmission(ctx, viewer, d.Identity.ID(), public, owner, actor, discSubtitles)
			if err != nil {
				return err
			}
			// Capture computes the separate FileInfo audience from the home
			// viewer. The Detail scope must instead carry the Detail page's fence.
			admission.DetailScope.ViewerFence = detailFence
			if admission.Identity != id {
				// The token names a different live row, library or public id.
				return readprep.ErrStale
			}
			return nil
		})
		if err != nil {
			return catalog.FileInfoAdmission{}, err
		}
		return admission, nil
	}
}

// fileInfoDetailFence rebuilds the Detail page's viewer fence. detailLibraryFenceFor
// is the same function the accepted Detail used, and it is computed before that
// page composes, so it must not inherit this check's read snapshot: the snapshot
// path would omit the account-limit narrowing the Detail page applied.
func (d Dependencies) fileInfoDetailFence(ctx context.Context, p identity.Principal, library string) (string, error) {
	base, cancel := context.WithCancel(context.Background())
	if deadline, ok := ctx.Deadline(); ok {
		cancel()
		base, cancel = context.WithDeadline(context.Background(), deadline)
	}
	defer cancel()
	_, fence, err := d.detailLibraryFenceFor((&http.Request{}).WithContext(base), p, library)
	return fence, err
}

func registerFileInfoWindows(registry *apikit.Registry, d Dependencies) error {
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "metadata_unavailable", "internal"}
	headers := fileInfoWindowHeaders()
	limit := []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: catalog.FileInfoDefaultLimit, Maximum: catalog.FileInfoMaximumLimit, EmptyIsDefault: true}}
	metadata := func(id string, audience apikit.Audience, path, summary string, query []string) apikit.Metadata {
		return apikit.Metadata{Audience: audience, Admission: laneBrowsing, ID: id, Method: "GET", Path: path, Summary: summary,
			Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized,
			Status: 200, SuccessStatuses: []int{202}, ResponseStatuses: []int{304}, ResponseHeaders: headers,
			Query: query, QueryIntegers: limit, Errors: failures}
	}
	if err := registerFileInfoLane[fileInfoFilesResponse](registry, metadata("get_items_id_file_info", apikit.Integrations, "/v1/items/{id}/file-info", "Read the item's flat file window", []string{"opening", "cursor", "limit", "around"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "files", around: true})); err != nil {
		return err
	}
	if err := registerFileInfoLane[fileInfoGroupsResponse](registry, metadata("get_item_file_info_groups", apikit.Clients, "/v1/items/{id}/file-info/groups", "Read the item's bounded version groups", []string{"opening", "cursor", "limit", "around"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "groups", around: true})); err != nil {
		return err
	}
	if err := registerFileInfoLane[fileInfoMembersResponse](registry, metadata("get_item_file_info_members", apikit.Clients, "/v1/items/{id}/file-info/groups/members", "Read one version group's member files", []string{"opening", "cursor", "limit", "group"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "members", group: true})); err != nil {
		return err
	}
	if err := registerFileInfoLane[fileInfoTracksResponse](registry, metadata("get_item_file_info_tracks", apikit.Clients, "/v1/items/{id}/file-info/tracks", "Read one file's raw audio or subtitle tracks", []string{"opening", "cursor", "limit", "occurrence", "lane"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "tracks", occurrence: true, lane: true})); err != nil {
		return err
	}
	if err := registerFileInfoLane[fileInfoHeadTracksResponse](registry, metadata("get_item_file_info_head_tracks", apikit.Clients, "/v1/items/{id}/file-info/groups/head-tracks", "Read a version group's offered head tracks", []string{"opening", "cursor", "limit", "group", "lane"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "head-tracks", group: true, lane: true})); err != nil {
		return err
	}
	if err := registerFileInfoLane[fileInfoSourcesResponse](registry, metadata("get_item_file_info_sources", apikit.Clients, "/v1/items/{id}/file-info/sources", "Read the item's bounded technical sources", []string{"opening", "cursor", "limit"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "sources"})); err != nil {
		return err
	}
	if err := registerFileInfoLane[fileInfoDiscResponse](registry, metadata("get_item_file_info_disc_titles", apikit.Clients, "/v1/items/{id}/file-info/disc-titles", "Read the owner's stored disc titles", []string{"opening", "cursor", "limit", "occurrence"}),
		d.fileInfoLaneHandler(fileInfoLaneSpec{kind: "disc-titles", occurrence: true})); err != nil {
		return err
	}
	query := apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_file_info_query", Method: "GET", Path: "/v1/file-info-queries/{queryId}",
		Summary: "Read a FileInfo preparation or its ready window", Access: apikit.Device, Requires: apikit.ScopeRead,
		Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseStatuses: []int{304},
		ResponseHeaders: headers, Errors: failures}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, fileInfoQueryResponse]{Metadata: query, Serve: d.fileInfoQueryHandler}); err != nil {
		return err
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[noBody, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "cancel_file_info_query", Method: "DELETE", Path: "/v1/file-info-queries/{queryId}",
		Summary: "Release a private FileInfo preparation handle", Access: apikit.Device, Requires: apikit.ScopeRead,
		Lane: apikit.Browsing, Cost: apikit.Constant, Status: 204, NoResponseBody: true, Errors: failures}, Serve: d.fileInfoQueryHandler})
}

func registerFileInfoLane[R any](registry *apikit.Registry, metadata apikit.Metadata, serve http.HandlerFunc) error {
	return apikit.RegisterRaw(registry, apikit.RawRoute[noBody, R]{Metadata: metadata, Serve: serve})
}

func fileInfoWindowHeaders() []apikit.ResponseHeader {
	return []apikit.ResponseHeader{
		{Status: 202, Name: "Location", Description: "Same-server FileInfo preparation poll path", Required: true},
		{Status: 202, Name: "Retry-After", Description: "Minimum delay before polling", Required: true},
		{Status: 200, Name: "ETag", Description: "Exact ready lane and window validator", Required: true},
		{Status: 304, Name: "ETag", Description: "Exact ready lane and window validator", Required: true},
	}
}

// Wire schemas. The ready pages are explicit 200 variants; the shared carrier is
// the 202 variant. A 304 has no body.

type fileInfoFilesResponse struct{}

func (fileInfoFilesResponse) page() map[string]any {
	return fileInfoPageSchema[catalog.FileInfoFilesPage]("file-info-files")
}
func (r fileInfoFilesResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoFilesResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

type fileInfoGroupsResponse struct{}

func (fileInfoGroupsResponse) page() map[string]any {
	return fileInfoPageSchema[catalog.FileInfoGroupsPage]("file-info-groups")
}
func (r fileInfoGroupsResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoGroupsResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

type fileInfoMembersResponse struct{}

func (fileInfoMembersResponse) page() map[string]any {
	return fileInfoPageSchema[catalog.FileInfoMembersPage]("file-info-members")
}
func (r fileInfoMembersResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoMembersResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

type fileInfoTracksResponse struct{}

func (fileInfoTracksResponse) page() map[string]any {
	return fileInfoTrackPageSchema[catalog.FileInfoTracksPage]("file-info-tracks")
}
func (r fileInfoTracksResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoTracksResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

type fileInfoHeadTracksResponse struct{}

func (fileInfoHeadTracksResponse) page() map[string]any {
	return fileInfoTrackPageSchema[catalog.FileInfoHeadTracksPage]("file-info-head-tracks")
}
func (r fileInfoHeadTracksResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoHeadTracksResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

type fileInfoSourcesResponse struct{}

func (fileInfoSourcesResponse) page() map[string]any {
	return fileInfoPageSchema[catalog.FileInfoSourcesPage]("file-info-sources")
}
func (r fileInfoSourcesResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoSourcesResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

type fileInfoDiscResponse struct{}

func (fileInfoDiscResponse) page() map[string]any {
	return fileInfoPageSchema[catalog.FileInfoDiscPage]("file-info-disc-titles")
}
func (r fileInfoDiscResponse) JSONSchema() map[string]any { return fileInfoUnion(r.page(), true) }
func (r fileInfoDiscResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(r.page(), status, true)
}

// fileInfoQueryResponse is a poll of the same private handle: a ready Get/Detail
// body, one of the original lane's ready kinds, or the shared preparation
// carrier. The carrier's detail is optional only for an opening preparation
// before the first accepted Detail.
type fileInfoQueryResponse struct{}

func fileInfoReadySchemas() []any {
	return []any{
		facetSchema[catalog.Detail](),
		facetSchema[catalog.Item](),
		fileInfoFilesResponse{}.page(),
		fileInfoGroupsResponse{}.page(),
		fileInfoMembersResponse{}.page(),
		fileInfoTracksResponse{}.page(),
		fileInfoHeadTracksResponse{}.page(),
		fileInfoSourcesResponse{}.page(),
		fileInfoDiscResponse{}.page(),
	}
}
func (fileInfoQueryResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": append(fileInfoReadySchemas(), fileInfoPreparingSchema(false))}
}
func (fileInfoQueryResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return map[string]any{"anyOf": fileInfoReadySchemas()}
	}
	if status == 202 {
		return fileInfoPreparingSchema(false)
	}
	return nil
}

func fileInfoUnion(page map[string]any, detailRequired bool) map[string]any {
	return map[string]any{"anyOf": []any{page, fileInfoPreparingSchema(detailRequired)}}
}
func fileInfoStatusSchema(page map[string]any, status int, detailRequired bool) map[string]any {
	if status == 200 {
		return page
	}
	if status == 202 {
		return fileInfoPreparingSchema(detailRequired)
	}
	return nil
}

func fileInfoPageSchema[P any](kind string) map[string]any {
	schema := facetSchema[P]()
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		panic("file information page schema has no properties")
	}
	properties["kind"] = map[string]any{"type": "string", "const": kind}
	properties["basis"] = fileInfoBasisSchema()
	properties["window"] = fileInfoWindowSchema()
	fileInfoSchemaText(schema)
	return schema
}

func fileInfoTrackPageSchema[P any](kind string) map[string]any {
	schema := fileInfoPageSchema[P](kind)
	properties, _ := schema["properties"].(map[string]any)
	properties["lane"] = map[string]any{"type": "string", "enum": []any{"audio", "subtitles"}}
	return schema
}

func fileInfoPreparingSchema(detailRequired bool) map[string]any {
	schema := facetSchema[catalog.FileInfoPreparing]()
	properties, _ := schema["properties"].(map[string]any)
	properties["kind"] = map[string]any{"type": "string", "const": "file-info-preparing"}
	properties["state"] = map[string]any{"type": "string", "const": "preparing"}
	if detailRequired {
		// A lane window always names the exact 338-character private window id.
		properties["queryId"] = fileInfoQueryIDSchema()
	} else {
		// The generic poll also carries an opening preparation handle from the
		// same store's opening namespace.
		properties["queryId"] = fileInfoOpaqueHandleSchema()
	}
	properties["retryAfterMs"] = map[string]any{"type": "integer", "minimum": 1}
	// An opening preparation before the first accepted Detail has no detail. A
	// lane always echoes the original Detail unchanged, so its carrier requires
	// it.
	if detailRequired {
		required, _ := schema["required"].([]string)
		present := false
		for _, name := range required {
			if name == "detail" {
				present = true
			}
		}
		if !present {
			schema["required"] = append(required, "detail")
		}
	}
	return schema
}

func fileInfoQueryIDSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": fileInfoQueryIDText, "maxLength": fileInfoQueryIDText,
		"pattern": "^[A-Za-z0-9_-]{183}\\.[A-Za-z0-9_-]{154}$"}
}
func fileInfoOpaqueHandleSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 512,
		"pattern": "^[A-Za-z0-9_.-]+$"}
}
func fileInfoCursorSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": fileInfoCursorText, "maxLength": fileInfoCursorText,
		"pattern": "^[A-Za-z0-9_-]{154}$"}
}
func fileInfoBasisSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 43, "maxLength": 43, "pattern": "^[A-Za-z0-9_-]{43}$"}
}
func fileInfoWindowSchema() map[string]any {
	schema := facetSchema[catalog.FileInfoWindow]()
	properties, _ := schema["properties"].(map[string]any)
	properties["queryId"] = fileInfoQueryIDSchema()
	properties["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": catalog.FileInfoMaximumLimit}
	properties["returned"] = map[string]any{"type": "integer", "minimum": 0, "maximum": catalog.FileInfoMaximumLimit}
	for _, key := range []string{"nextCursor", "previousCursor", "reloadCursor"} {
		properties[key] = fileInfoCursorSchema()
	}
	return schema
}

// fileInfoLegalText names the human text fields. They stay lossless at any
// length: no arbitrary maxLength is published, and a NUL is refused because no
// legal path, edition, title or codec label contains one.
var fileInfoLegalText = map[string]bool{
	"path": true, "edition": true, "title": true, "language": true, "channelLayout": true,
	"profile": true, "codec": true, "container": true, "videoCodec": true, "audioCodec": true,
	"dynamicRange": true, "objectAudio": true,
}

// fileInfoSchemaText bounds every row array at one page and marks legal text as
// NUL-free without inventing a length cap.
func fileInfoSchemaText(node any) {
	switch value := node.(type) {
	case map[string]any:
		if value["type"] == "array" {
			value["maxItems"] = catalog.FileInfoMaximumLimit
		}
		if properties, ok := value["properties"].(map[string]any); ok {
			for name, raw := range properties {
				child, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if child["type"] == "string" && fileInfoLegalText[name] {
					child["pattern"] = "^[^\x00]*$"
				}
				fileInfoSchemaText(child)
			}
		}
		if items, ok := value["items"]; ok {
			fileInfoSchemaText(items)
		}
		if variants, ok := value["anyOf"].([]any); ok {
			for _, variant := range variants {
				fileInfoSchemaText(variant)
			}
		}
	case []any:
		for _, item := range value {
			fileInfoSchemaText(item)
		}
	}
}

// The registry authorizer invokes this before generic query validation. The
// item must be concealed even when a request has unknown or malformed fields.
func fileInfoLaneRoute(m apikit.Metadata) bool {
	if m.Method != http.MethodGet {
		return false
	}
	switch m.ID {
	case "get_items_id_file_info", "get_item_file_info_groups", "get_item_file_info_members", "get_item_file_info_tracks", "get_item_file_info_head_tracks", "get_item_file_info_sources", "get_item_file_info_disc_titles":
		return true
	}
	return false
}
