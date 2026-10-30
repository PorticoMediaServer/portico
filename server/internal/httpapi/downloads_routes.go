package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Offline download routes. Everything a client does to take media offline is
// here: reading the menu of qualities and their sizes, claiming a title,
// taking the address its bytes are fetched from, reading what travels with it
// (titles, artwork, subtitles), holding the signed receipt that permits
// playback with no server in reach, and handing back the plays it made while
// it was away. Every route is on the typed registry.

func downloadErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "download_conflict", Status: 409, Message: publicErrorMessage("download_conflict"), Retry: apierror.AfterRefresh},
		{Code: "storage_full", Status: 409, Message: publicErrorMessage("storage_full"), Retry: apierror.Never},
		{Code: "downloads_not_allowed", Status: 403, Message: publicErrorMessage("downloads_not_allowed"), Retry: apierror.Never},
		{Code: "download_not_ready", Status: 409, Message: publicErrorMessage("download_not_ready"), Retry: apierror.SameRequest},
		{Code: "download_unavailable", Status: 410, Message: "This download can no longer be fetched. Ask for it again.", Retry: apierror.AfterRefresh},
		{Code: "invalid_download_grant", Status: 401, Message: publicErrorMessage("invalid_download_grant"), Retry: apierror.AfterRefresh},
		{Code: "download_source_offline", Status: 503, Message: "The file for this download can't be reached right now.", Retry: apierror.SameRequest},
		{Code: "downloads_unavailable", Status: 503, Message: "Downloads are not set up on this server.", Retry: apierror.SameRequest},
	}
}

// downloadError maps this area's errors onto registered codes. A fence failure
// carries the current row, so a client re-renders without a second read.
func downloadError(e error) error {
	if mutation := userMutationError(e); mutation != nil {
		return mutation
	}

	var public *apierror.Error
	if e == nil || errors.As(e, &public) {
		return e
	}
	code := ""
	switch {
	case errors.Is(e, downloads.ErrRequestConflict):
		code = "idempotency_key_reused"
	case errors.Is(e, downloads.ErrInput), errors.Is(e, downloads.ErrReceipt):
		code = "invalid_request"
	case errors.Is(e, downloads.ErrNotFound):
		code = "not_found"
	case errors.Is(e, downloads.ErrConflict):
		code = "download_conflict"
	case errors.Is(e, downloads.ErrStorageFull):
		code = "storage_full"
	case errors.Is(e, downloads.ErrPolicy):
		code = "downloads_not_allowed"
	case errors.Is(e, downloads.ErrNotReady):
		code = "download_not_ready"
	case errors.Is(e, downloads.ErrUnavailable):
		code = "download_unavailable"
	case errors.Is(e, downloads.ErrSourceOffline):
		return &apierror.Error{Code: "download_source_offline", RetryAfterSeconds: 30, Cause: e}
	case errors.Is(e, downloads.ErrGrant):
		code = "invalid_download_grant"
	case errors.Is(e, downloads.ErrCapacity):
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 30, Cause: e}
	case errors.Is(e, downloads.ErrNoPlayWriter):
		code = "downloads_unavailable"
	}
	if code == "" {
		return e
	}
	return &apierror.Error{Code: code, Cause: e}
}

var errDownloadsUnavailable = &apierror.Error{Code: "downloads_unavailable", RetryAfterSeconds: 60}

// downloadViewer authenticates and answers whether this viewer may order the
// conversions an optimized rung needs. Conversions stay owner-authorized in
// prepared media; downloads never widens that.
func (d Dependencies) downloadViewer(r *http.Request) (identity.Principal, bool, error) {
	if d.Downloads == nil {
		return identity.Principal{}, false, errDownloadsUnavailable
	}
	p, e := d.principal(r)
	if e != nil {
		return p, false, e
	}
	return p, p.Role == "owner" && p.Authority == "local", nil
}

type downloadGrantBody struct {
	OperationID string `json:"operationId"`
	// Renew is the token of the address this device already holds for the
	// download, to keep that same address alive.
	Renew string `json:"renew,omitempty"`
}
type downloadReceiptsBody struct {
	OperationID    string   `json:"operationId"`
	PreparationIDs []string `json:"preparationIds"`
}
type downloadRevalidateBody struct {
	OperationID string   `json:"operationId"`
	ReceiptIDs  []string `json:"receiptIds"`
}
type downloadRevokeBody struct {
	OperationID string   `json:"operationId"`
	ReceiptIDs  []string `json:"receiptIds"`
	Reason      string   `json:"reason,omitempty"`
	AllProfiles bool     `json:"allProfiles,omitempty"`
}
type downloadProgressBody struct {
	OperationID string                    `json:"operationId"`
	Entries     []downloads.ProgressEntry `json:"entries"`
}
type downloadReceiptResults struct {
	Results []downloads.ReceiptOutcome `json:"results"`
}
type downloadReceiptKeys struct {
	Keys []downloads.ReceiptKey `json:"keys"`
}

func registerDownloads(r *apikit.Registry, d Dependencies) {
	read := []string{"invalid_request", "unauthorized", "not_permitted", "feature_restricted", "not_found", "downloads_not_allowed", "downloads_unavailable", "internal"}
	write := append([]string{"download_conflict", "storage_full", "download_not_ready", "download_unavailable", "rate_limited"}, read...)
	// What a key or token must be allowed to do to use each route. Reading what
	// a member has claimed is reading; claiming, fetching, taking offline
	// permission and reporting plays are that member's own playback; the
	// owner's download settings are administration.
	scopes := map[string]apikit.Scope{
		"get_download_options": apikit.ScopeRead, "list_download_preparations": apikit.ScopeRead, "get_download_preparation": apikit.ScopeRead,
		"get_download_bundle": apikit.ScopeRead, "list_download_revocations": apikit.ScopeRead, "list_download_receipt_keys": apikit.ScopeRead,
		"get_download_usage": apikit.ScopeRead, "get_download_settings": apikit.ScopeRead, "list_download_requests": apikit.ScopeRead,
		"create_download_preparations": apikit.ScopePlayback, "act_on_download_preparation": apikit.ScopePlayback, "create_download_grant": apikit.ScopePlayback,
		"issue_download_receipts": apikit.ScopePlayback, "revalidate_download_receipts": apikit.ScopePlayback, "revoke_download_receipts": apikit.ScopePlayback,
		"sync_download_progress": apikit.ScopePlayback,
		// Fetched with no credential at all: the address is the permission, and
		// taking the address is what needs the playback scope.
		"get_download_artifact": apikit.ScopeOpen, "head_download_artifact": apikit.ScopeOpen, "get_download_artwork": apikit.ScopeOpen, "get_download_subtitle": apikit.ScopeOpen, "get_download_subtitle_original": apikit.ScopeOpen,
		"get_download_trickplay_thumbnails": apikit.ScopeOpen, "get_download_trickplay_tile": apikit.ScopeOpen, "get_download_chapter_image": apikit.ScopeOpen,
		"put_download_settings": apikit.ScopeAdmin,
	}
	scope := func(id string) apikit.Scope {
		requires, ok := scopes[id]
		if !ok {
			panic(id + ": a download route needs a key scope")
		}
		return requires
	}
	admission := func(id string) string {
		switch id {
		case "act_on_download_preparation":
			return laneDefault
		case "create_download_grant":
			return laneDefault
		case "create_download_preparations":
			return laneDefault
		case "create_download_request":
			return laneDefault
		case "get_download_artifact":
			return laneBulkTransfer
		case "get_download_artwork":
			return laneMedia
		case "get_download_bundle":
			return laneDefault
		case "get_download_options":
			return laneBrowsing
		case "get_download_preparation":
			return laneDefault
		case "get_download_request":
			return laneBrowsing
		case "get_download_settings":
			return laneDefault
		case "get_download_subtitle":
			return laneMedia
		case "get_download_subtitle_original", "get_download_trickplay_thumbnails", "get_download_trickplay_tile", "get_download_chapter_image":
			return laneMedia
		case "get_download_usage":
			return laneDefault
		case "head_download_artifact":
			return laneBulkTransfer
		case "issue_download_receipts":
			return laneDefault
		case "list_download_preparations":
			return laneDefault
		case "list_download_receipt_keys":
			return laneDefault
		case "list_download_requests":
			return laneBrowsing
		case "list_download_revocations":
			return laneSecurityFence
		case "put_download_settings":
			return laneDefault
		case "revalidate_download_receipts":
			return laneDefault
		case "revoke_download_receipts":
			return laneSecurityFence
		case "revoke_profile_downloads":
			return laneSecurityFence
		case "sync_download_progress":
			return laneDefault
		}
		return laneDefault
	}
	route := func(id, method, path, summary string, access apikit.Access, lane apikit.Lane, cost apikit.Cost, status int, bodyLimit int64, query []string) apikit.Metadata {
		failures := read
		if method != "GET" {
			failures = write
		}
		var integers []apikit.QueryInteger
		switch id {
		case "list_download_preparations":
			integers = []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 50, Maximum: 200, DefaultBelowMinimum: true, DefaultAboveMaximum: true, DefaultOnInvalid: true, EmptyIsDefault: true, Description: "Invalid integer syntax and out-of-range values use the default; integer overflow saturates before normalization."}}
		case "list_download_revocations":
			integers = []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 200, Maximum: 500, DefaultBelowMinimum: true, DefaultAboveMaximum: true, DefaultOnInvalid: true, EmptyIsDefault: true, Description: "Invalid integer syntax and out-of-range values use the default; integer overflow saturates before normalization."}}
		case "get_download_usage":
			integers = emptyLimitQuery(256, 256)
		case "list_download_requests":
			integers = emptyLimitQuery(20, downloads.MaxRequestPage)
		}
		return apikit.Metadata{Audience: apikit.Clients, Admission: admission(id), ID: id, Method: method, Path: path, Summary: summary, Access: access, Requires: scope(id), Lane: lane, Cost: cost, Status: status, BodyLimit: bodyLimit, Query: query, QueryIntegers: integers, Errors: failures}
	}
	v1Route(r, route("get_download_options", "GET", "/v1/items/{itemId}/download-options", "List offline qualities this device can play and this server can produce", apikit.ViewerItem, apikit.Browsing, apikit.PageSized, 200, 0, []string{"versionId"}), func(ctx context.Context, q *http.Request, _ noBody) (downloads.DownloadOptionsView, error) {
		p, owner, e := d.downloadViewer(q)
		if e == nil {
			e = d.itemAccess(ctx, p, q.PathValue("itemId"))
		}
		if e != nil {
			return downloads.DownloadOptionsView{}, e
		}
		out, e := d.Downloads.OptionsVersion(ctx, p, q.PathValue("itemId"), q.URL.Query().Get("versionId"), owner)
		return out, downloadError(e)
	})
	v1Route(r, route("create_download_preparations", "POST", "/v1/downloads/preparations", "Claim one title, several titles or the next episode for offline use", apikit.Device, apikit.Default, apikit.PageSized, 201, 32768, nil), func(ctx context.Context, q *http.Request, body downloads.Request) (downloads.Batch, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Batch{}, e
		}
		// The guard runs inside the admitting transaction, so it reads through
		// that transaction rather than opening a second one.
		out, e := d.Downloads.Submit(ctx, p, body, func(tx *sql.Tx, library string) error {
			return d.allowedLibraryTx(ctx, p, library, tx)
		})
		if e == nil && out.Duplicate {
			apikit.SetStatus(ctx, 200)
		}
		return out, downloadError(e)
	})
	v1Route(r, route("list_download_preparations", "GET", "/v1/downloads/preparations", "List this viewer's download claims, newest first", apikit.Device, apikit.Default, apikit.PageSized, 200, 0, []string{"state", "profileId", "cursor", "limit", "ids", "operationId", "deviceId"}), func(ctx context.Context, q *http.Request, _ noBody) (downloads.Page, error) {
		p, owner, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Page{}, e
		}
		limit, _ := strconv.Atoi(q.URL.Query().Get("limit"))
		if q.URL.Query().Has("operationId") || q.URL.Query().Has("deviceId") {
			if q.URL.Query().Get("operationId") == "" || q.URL.Query().Get("deviceId") == "" || q.URL.Query().Has("ids") || q.URL.Query().Has("state") || q.URL.Query().Has("profileId") {
				return downloads.Page{}, downloadError(downloads.ErrInput)
			}
			if limit == 0 {
				limit = 20
			}
			if limit < 1 || limit > 100 {
				return downloads.Page{}, downloadError(downloads.ErrInput)
			}
			out, e := d.Downloads.ListSelectionAction(ctx, p, q.URL.Query().Get("operationId"), q.URL.Query().Get("deviceId"), q.URL.Query().Get("cursor"), limit)
			return out, downloadError(e)
		}
		var ids []string
		if value := q.URL.Query().Get("ids"); value != "" {
			ids = strings.Split(value, ",")
		}
		out, e := d.Downloads.List(ctx, p, downloads.ListQuery{IDs: ids, State: q.URL.Query().Get("state"), ProfileID: q.URL.Query().Get("profileId"), Cursor: q.URL.Query().Get("cursor"), Limit: limit}, owner)
		return out, downloadError(e)
	})
	v1Route(r, route("get_download_preparation", "GET", "/v1/downloads/preparations/{id}", "Read one download claim", apikit.Device, apikit.Default, apikit.Constant, 200, 0, nil), func(ctx context.Context, q *http.Request, _ noBody) (downloads.Preparation, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Preparation{}, e
		}
		out, e := d.Downloads.Get(ctx, p, q.PathValue("id"))
		return out, downloadError(e)
	})
	v1Route(r, route("act_on_download_preparation", "POST", "/v1/downloads/preparations/{id}/actions", "Pause, resume, cancel, retry or remove a download claim", apikit.Device, apikit.Default, apikit.Constant, 200, 4096, nil), func(ctx context.Context, q *http.Request, body downloads.Command) (downloads.Preparation, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Preparation{}, e
		}
		out, e := d.Downloads.Act(ctx, p, q.PathValue("id"), body)
		if errors.Is(e, downloads.ErrConflict) {
			return out, &apierror.Error{Code: "download_conflict", Current: out, Cause: e}
		}
		return out, downloadError(e)
	})
	v1Route(r, route("create_download_grant", "POST", "/v1/downloads/preparations/{id}/grant", "Take, or keep alive, the address a ready download is fetched from", apikit.Device, apikit.Default, apikit.Constant, 201, 4096, nil), func(ctx context.Context, q *http.Request, body downloadGrantBody) (downloads.Grant, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Grant{}, e
		}
		out, e := d.Downloads.IssueGrant(ctx, p, q.PathValue("id"), downloads.GrantRequest{OperationID: body.OperationID, Renew: body.Renew})
		if e == nil && out.Renewed {
			apikit.SetStatus(ctx, 200)
		}
		return out, downloadError(e)
	})
	v1Route(r, route("get_download_bundle", "GET", "/v1/downloads/preparations/{id}/bundle", "Read everything that travels with a ready download: titles, artwork, chapters, markers, subtitles and audio tracks", apikit.Device, apikit.Default, apikit.PageSized, 200, 0, nil), func(ctx context.Context, q *http.Request, _ noBody) (downloads.Bundle, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Bundle{}, e
		}
		out, e := d.Downloads.Bundle(ctx, p, q.PathValue("id"))
		if e != nil {
			return out, downloadError(e)
		}
		if d.Subtitles != nil && out.Media.VideoCodec != "" {
			// A styled track's fonts are known once the server has read the track
			// out of its file. That is given a moment here; if it is still going
			// on, the device is told to ask again shortly rather than handed a
			// list that is missing the fonts.
			listing, cancel := context.WithTimeout(ctx, 20*time.Second)
			tracks, err := d.Subtitles.DownloadTracks(listing, p, out.ItemID, out.SourceID)
			cancel()
			if errors.Is(err, subtitles.ErrUnavailable) {
				return out, &apierror.Error{Code: "download_not_ready", RetryAfterSeconds: 30, Cause: err}
			}
			if err != nil {
				return out, err
			}
			for _, track := range tracks {
				bundled := downloads.BundleSubtitle{ID: track.ID, Revision: track.Revision, Language: track.Language, Title: track.Title, Default: track.Default, Forced: track.Forced, Files: subtitleFiles(track)}
				if track.Lines != nil {
					bundled.Lines = &downloads.BundleLines{Forced: track.Lines.Forced, Total: track.Lines.Total}
				}
				out.Subtitles = append(out.Subtitles, bundled)
			}
		}
		if d.Analysis != nil && out.Media.VideoCodec != "" && out.SourceID != "" {
			out.Trickplay = d.downloadTrickplay(ctx, p, out.ItemID, out.SourceID)
		}
		return out, nil
	})

	// The transfer itself. The address is the whole authority here, exactly as
	// a media grant is for playback: it names the viewer, and that viewer's
	// permission is rechecked before a byte of every request is served.
	transfer := func(w http.ResponseWriter, q *http.Request) {
		if d.Downloads == nil {
			r.Errors().Write(w, "", errDownloadsUnavailable)
			return
		}
		out, e := d.Downloads.OpenGrant(q.Context(), q.PathValue("grant"))
		if e != nil {
			r.Errors().Write(w, "", v1Error(downloadError(e)))
			return
		}
		reader := newDownloadReader(q.Context(), out.Reader, func() error {
			scope, err := d.Downloads.ResolveGrant(q.Context(), q.PathValue("grant"))
			if err != nil {
				return err
			}
			if scope.Artifact != out.Artifact {
				return downloads.ErrUnavailable
			}
			return d.allowedLibrary(q.Context(), identity.Principal{Viewer: scope.Viewer}, scope.LibraryID)
		})
		defer reader.Close()
		if e = d.allowedLibrary(q.Context(), identity.Principal{Viewer: out.Viewer}, out.LibraryID); e != nil {
			r.Errors().Write(w, "", &apierror.Error{Code: "invalid_download_grant", Cause: e})
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", out.Artifact.ContentType)
		w.Header().Set("Content-Disposition", contentDisposition(out.SaveAs, out.Artifact.FileName))
		// The digest is the file's identity. A device continues a partial file
		// with Range and If-Range against it, under any address for the claim.
		w.Header().Set("ETag", "\"sha256-"+out.Artifact.SHA256+"\"")
		w.Header().Set("X-Portico-Artifact-Sha256", out.Artifact.SHA256)
		body := withRollingDeadline(w)
		defer body.release()
		http.ServeContent(body, q, out.Artifact.FileName, out.Modified, reader)
	}
	rawRoute := func(id, method, path, summary string, lane apikit.Lane, query []string, serve http.HandlerFunc) {
		m := apikit.Metadata{Audience: apikit.Clients, ResponseContentType: "application/octet-stream", Admission: admission(id), ID: id, Method: method, Path: path, Summary: summary, Access: apikit.Public, Requires: scope(id), Lane: lane, Cost: apikit.Constant, Status: 200, Query: query, Errors: []string{"invalid_request", "invalid_download_grant", "downloads_not_allowed", "download_unavailable", "download_not_ready", "download_source_offline", "downloads_unavailable", "not_found", "internal"}}
		if err := apikit.RegisterRaw(r, apikit.RawRoute[noBody, SubtitleBytesContract]{Metadata: m, Serve: serve}); err != nil {
			panic(m.ID + ": " + err.Error())
		}
	}
	rawRoute("get_download_artifact", "GET", "/v1/downloads/artifacts/{grant}", "Fetch a download's media file, whole or by byte range", apikit.Media, nil, transfer)
	rawRoute("head_download_artifact", "HEAD", "/v1/downloads/artifacts/{grant}", "Read a download's size, type and digest", apikit.Media, nil, transfer)
	rawRoute("get_download_artwork", "GET", "/v1/downloads/artifacts/{grant}/artwork/{role}", "Fetch one picture that travels with a download", apikit.Media, nil, func(w http.ResponseWriter, q *http.Request) {
		scope, ok := d.downloadExtra(w, q, r)
		if !ok {
			return
		}
		width := downloads.ArtworkWidth(q.PathValue("role"))
		if width == 0 || d.Metadata == nil {
			r.Errors().Write(w, "", &apierror.Error{Code: "not_found"})
			return
		}
		f, mime, e := d.Metadata.EntityArtworkVariant(q.Context(), metadata.RepairTarget{Kind: "item", ID: scope.ItemID}, q.PathValue("role"), "", metadata.ArtworkSizeForWidth(width), "")
		if errors.Is(e, metadata.ErrArtworkPending) {
			// The picture exists and its smaller copy is being made.
			r.Errors().Write(w, "", &apierror.Error{Code: "download_not_ready", RetryAfterSeconds: 5})
			return
		}
		if e != nil {
			// No such picture for this title is an ordinary answer: most
			// episodes have no backdrop of their own.
			r.Errors().Write(w, "", &apierror.Error{Code: "not_found", Cause: e})
			return
		}
		defer f.Close()
		w.Header().Set("Cache-Control", "private, no-store")
		serveArtwork(w, q, f, mime)
	})
	rawRoute("get_download_subtitle", "GET", "/v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}.vtt", "Fetch one subtitle track that travels with a download, as WebVTT", apikit.Media, nil, func(w http.ResponseWriter, q *http.Request) {
		scope, ok := d.downloadExtra(w, q, r)
		if !ok {
			return
		}
		revision, e := strconv.ParseInt(q.PathValue("revision"), 10, 64)
		if e != nil || revision < 1 || d.Subtitles == nil {
			r.Errors().Write(w, "", &apierror.Error{Code: "not_found"})
			return
		}
		document, e := d.Subtitles.DownloadTrackVTT(q.Context(), scope.Principal, scope.ItemID, q.PathValue("resource"), revision)
		if e != nil {
			r.Errors().Write(w, "", v1Error(subtitleDownloadError(e)))
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(document)))
		_, _ = w.Write(document)
	})

	// A track as it was authored, for a device that draws styled or picture
	// subtitles itself: the script or picture stream ("original"), the second
	// file of a pair ("companion"), and a styled script's fonts ("font-0" …).
	// The offline player needs these on the device; none of them can be asked
	// for once the server is out of reach.
	rawRoute("get_download_subtitle_original", "GET", "/v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}/{file}", "Fetch one file of a styled or picture subtitle track that travels with a download", apikit.Media, nil, func(w http.ResponseWriter, q *http.Request) {
		scope, ok := d.downloadExtra(w, q, r)
		if !ok {
			return
		}
		revision, e := strconv.ParseInt(q.PathValue("revision"), 10, 64)
		part, font := "", false
		switch file := q.PathValue("file"); {
		case file == "original":
			part = "data"
		case file == "companion":
			part = "companion"
		case strings.HasPrefix(file, "font-"):
			if n, err := strconv.Atoi(file[len("font-"):]); err == nil && n >= 0 && n < 16 && strconv.Itoa(n) == file[len("font-"):] {
				part, font = "font:"+strconv.Itoa(n), true
			}
		}
		if e != nil || revision < 1 || part == "" || d.Subtitles == nil {
			r.Errors().Write(w, "", &apierror.Error{Code: "not_found"})
			return
		}
		reader, size, format, encoding, e := d.Subtitles.OpenDownloadOriginal(q.Context(), scope.Principal, scope.ItemID, q.PathValue("resource"), revision, part)
		if e != nil {
			r.Errors().Write(w, "", v1Error(subtitleDownloadError(e)))
			return
		}
		defer reader.Close()
		contentType := "application/octet-stream"
		switch {
		case font:
			contentType = "font/ttf"
		case part == "data" && (format == "ass" || format == "ssa"):
			contentType = "text/x-ssa"
			if encoding != "" {
				contentType += "; charset=" + encoding
			}
		case part == "data" && format == "vobsub":
			contentType = "text/plain"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body := withRollingDeadline(w)
		defer body.release()
		http.ServeContent(body, q, "", time.Time{}, io.NewSectionReader(reader, 0, size))
	})

	// The scrubbing previews and chapter thumbnails of a download's file, so the
	// offline player shows them with no server in reach. They are the same
	// stored images the item routes serve, read under the transfer address.
	preview := func(w http.ResponseWriter, q *http.Request, read func(context.Context, mediaanalysis.Access) (mediaanalysis.Image, error)) {
		scope, ok := d.downloadExtra(w, q, r)
		if !ok {
			return
		}
		if d.Analysis == nil {
			r.Errors().Write(w, "", &apierror.Error{Code: "not_found"})
			return
		}
		ctx, cancel := context.WithTimeout(q.Context(), 20*time.Second)
		defer cancel()
		out, e := read(ctx, d.downloadAnalysis(scope.Principal, scope.LibraryID, scope.ItemID))
		if e != nil {
			r.Errors().Write(w, "", downloadPreviewError(e))
			return
		}
		w.Header().Set("Content-Type", out.MIME)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(out.Data)))
		_, _ = w.Write(out.Data)
	}
	rawRoute("get_download_trickplay_thumbnails", "GET", "/v1/downloads/artifacts/{grant}/trickplay/{set}/thumbnails.vtt", "Fetch the scrubbing preview track that travels with a download", apikit.Media, nil, func(w http.ResponseWriter, q *http.Request) {
		preview(w, q, func(ctx context.Context, a mediaanalysis.Access) (mediaanalysis.Image, error) {
			track, e := d.Analysis.Thumbnails(ctx, a, q.PathValue("set"))
			return mediaanalysis.Image{MIME: "text/vtt; charset=utf-8", Data: []byte(track)}, e
		})
	})
	rawRoute("get_download_trickplay_tile", "GET", "/v1/downloads/artifacts/{grant}/trickplay/{set}/tiles/{tile}", "Fetch one scrubbing preview sheet that travels with a download", apikit.Media, nil, func(w http.ResponseWriter, q *http.Request) {
		raw, found := strings.CutSuffix(q.PathValue("tile"), ".jpg")
		tile, e := strconv.Atoi(raw)
		if !found || e != nil || raw != strconv.Itoa(tile) || tile < 0 || tile >= downloads.MaxBundleTrickplayTiles {
			r.Errors().Write(w, "", &apierror.Error{Code: "not_found"})
			return
		}
		preview(w, q, func(ctx context.Context, a mediaanalysis.Access) (mediaanalysis.Image, error) {
			return d.Analysis.Tile(ctx, a, q.PathValue("set"), tile)
		})
	})
	rawRoute("get_download_chapter_image", "GET", "/v1/downloads/artifacts/{grant}/chapters/{source}/{index}/image", "Fetch one chapter thumbnail that travels with a download", apikit.Media, nil, func(w http.ResponseWriter, q *http.Request) {
		preview(w, q, func(ctx context.Context, a mediaanalysis.Access) (mediaanalysis.Image, error) {
			// The chapter's identity is the playing projection's own `{sourceId}:{index}`.
			return d.Analysis.ChapterImage(ctx, a, q.PathValue("source")+":"+q.PathValue("index"))
		})
	})

	v1Route(r, route("issue_download_receipts", "POST", "/v1/downloads/receipts", "Take signed offline permission for ready downloads", apikit.Device, apikit.Default, apikit.PageSized, 201, 32768, nil), func(ctx context.Context, q *http.Request, body downloadReceiptsBody) (downloadReceiptResults, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloadReceiptResults{}, e
		}
		out, e := d.Downloads.IssueReceipts(ctx, p, d.Identity.ID(), body.OperationID, body.PreparationIDs)
		return downloadReceiptResults{Results: out}, downloadError(e)
	})
	v1Route(r, route("revalidate_download_receipts", "POST", "/v1/downloads/receipts/revalidate", "Renew offline permission for copies already on a device", apikit.Device, apikit.Default, apikit.PageSized, 200, 32768, nil), func(ctx context.Context, q *http.Request, body downloadRevalidateBody) (downloadReceiptResults, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloadReceiptResults{}, e
		}
		out, e := d.Downloads.Revalidate(ctx, p, d.Identity.ID(), body.OperationID, body.ReceiptIDs, func(tx *sql.Tx, library string) error {
			return d.allowedLibraryTx(ctx, p, library, tx)
		})
		return downloadReceiptResults{Results: out}, downloadError(e)
	})
	v1Route(r, route("revoke_download_receipts", "POST", "/v1/downloads/receipts/revoke", "Withdraw offline permission from copies on devices", apikit.Device, apikit.Security, apikit.PageSized, 200, 32768, nil), func(ctx context.Context, q *http.Request, body downloadRevokeBody) (downloadReceiptResults, error) {
		p, owner, e := d.downloadViewer(q)
		if e != nil {
			return downloadReceiptResults{}, e
		}
		out, e := d.Downloads.Revoke(ctx, p, body.OperationID, body.Reason, body.ReceiptIDs, owner && body.AllProfiles)
		return downloadReceiptResults{Results: out}, downloadError(e)
	})
	v1Route(r, route("list_download_revocations", "GET", "/v1/downloads/receipts/revocations", "Page the receipts that have been withdrawn", apikit.Device, apikit.Security, apikit.PageSized, 200, 0, []string{"cursor", "limit", "scope"}), func(ctx context.Context, q *http.Request, _ noBody) (downloads.RevocationPage, error) {
		p, owner, e := d.downloadViewer(q)
		if e != nil {
			return downloads.RevocationPage{}, e
		}
		limit, _ := strconv.Atoi(q.URL.Query().Get("limit"))
		out, e := d.Downloads.Revocations(ctx, p, q.URL.Query().Get("cursor"), limit, owner && q.URL.Query().Get("scope") == "server")
		return out, downloadError(e)
	})
	v1Route(r, route("list_download_receipt_keys", "GET", "/v1/downloads/receipt-keys", "Read the keys this server signs receipts with", apikit.Device, apikit.Default, apikit.Constant, 200, 0, nil), func(ctx context.Context, q *http.Request, _ noBody) (downloadReceiptKeys, error) {
		if _, _, e := d.downloadViewer(q); e != nil {
			return downloadReceiptKeys{}, e
		}
		out, e := d.Downloads.ReceiptKeys(ctx)
		return downloadReceiptKeys{Keys: out}, downloadError(e)
	})
	v1Route(r, route("sync_download_progress", "POST", "/v1/downloads/progress", "Report plays of downloaded copies made with no server in reach", apikit.Device, apikit.Default, apikit.PageSized, 200, 65536, nil), func(ctx context.Context, q *http.Request, body downloadProgressBody) (downloads.ProgressReceipt, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.ProgressReceipt{}, e
		}
		out, e := d.Downloads.SyncProgress(ctx, p, body.OperationID, body.Entries)
		return out, downloadError(e)
	})
	v1Route(r, route("get_download_usage", "GET", "/v1/downloads/usage", "Read how much this viewer, or the whole server, has claimed", apikit.Device, apikit.Default, apikit.PageSized, 200, 0, []string{"scope", "cursor", "limit"}), func(ctx context.Context, q *http.Request, _ noBody) (downloads.Usage, error) {
		p, owner, e := d.downloadViewer(q)
		if e != nil {
			return downloads.Usage{}, e
		}
		limit := 256
		if raw := q.URL.Query().Get("limit"); raw != "" {
			limit, e = strconv.Atoi(raw)
			if e != nil {
				return downloads.Usage{}, &apierror.Error{Code: "invalid_request"}
			}
		}
		out, e := d.Downloads.UsagePage(ctx, p, owner && q.URL.Query().Get("scope") == "server", q.URL.Query().Get("cursor"), limit)
		return out, downloadError(e)
	})
	v1Route(r, route("get_download_settings", "GET", "/v1/downloads/settings", "Read the owner's download settings", apikit.Device, apikit.Default, apikit.Constant, 200, 0, nil), func(ctx context.Context, q *http.Request, _ noBody) (downloads.Settings, error) {
		if _, _, e := d.downloadViewer(q); e != nil {
			return downloads.Settings{}, e
		}
		out, e := d.Downloads.Settings(ctx)
		return out, downloadError(e)
	})
	v1Route(r, route("put_download_settings", "PUT", "/v1/downloads/settings", "Change the owner's download settings", apikit.Owner, apikit.Default, apikit.Constant, 200, 4096, nil), func(ctx context.Context, q *http.Request, body downloads.SettingsChange) (downloads.Settings, error) {
		if d.Downloads == nil {
			return downloads.Settings{}, errDownloadsUnavailable
		}
		p, e := d.owner(q)
		if e != nil {
			return downloads.Settings{}, e
		}
		out, e := d.Downloads.ApplySettings(ctx, p, body, func(tx *sql.Tx) error { return d.ownerAuthorityTx(ctx, tx, p) })
		if errors.Is(e, downloads.ErrConflict) {
			return out, &apierror.Error{Code: "download_conflict", Current: out, Cause: e}
		}
		return out, downloadError(e)
	})
	v1Route(r, route("list_download_requests", "GET", "/v1/downloads/requests", "List the whole-show, season, album, book and playlist downloads a device started", apikit.Device, apikit.Browsing, apikit.PageSized, 200, 0, []string{"deviceId", "cursor", "limit"}), func(ctx context.Context, q *http.Request, _ noBody) (downloads.RequestPage, error) {
		p, _, e := d.downloadViewer(q)
		if e != nil {
			return downloads.RequestPage{}, e
		}
		limit := 20
		if v := q.URL.Query().Get("limit"); v != "" {
			if limit, e = strconv.Atoi(v); e != nil {
				return downloads.RequestPage{}, downloadRequestError(downloads.ErrInput)
			}
		}
		out, e := d.Downloads.Requests(ctx, p, q.URL.Query().Get("deviceId"), q.URL.Query().Get("cursor"), limit)
		return out, downloadRequestError(e)
	})
}

// downloadExtra checks the transfer address an extra is fetched under, the
// same way and as often as the media file's own requests are checked.
func (d Dependencies) downloadExtra(w http.ResponseWriter, q *http.Request, r *apikit.Registry) (downloads.GrantScope, bool) {
	if d.Downloads == nil {
		r.Errors().Write(w, "", errDownloadsUnavailable)
		return downloads.GrantScope{}, false
	}
	scope, e := d.Downloads.ResolveGrant(q.Context(), q.PathValue("grant"))
	if e == nil {
		e = d.allowedLibrary(q.Context(), identity.Principal{Viewer: scope.Viewer}, scope.LibraryID)
		if e != nil {
			e = &apierror.Error{Code: "invalid_download_grant", Cause: e}
		}
	}
	if e != nil {
		r.Errors().Write(w, "", v1Error(downloadError(e)))
		return scope, false
	}
	return scope, true
}

// downloadAnalysis is the media-analysis access for a download's previews:
// the viewer the download (or its transfer address) stands for, on the
// title's library, rechecked inside every read.
func (d Dependencies) downloadAnalysis(p identity.Principal, library, item string) mediaanalysis.Access {
	return mediaanalysis.Access{ServerID: d.Identity.ID(), LibraryID: library, ItemID: item, Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, SessionHash: p.Hash, Authorize: func(tx *sql.Tx) error {
		return d.resourceAuthorize(tx, p, library)
	}}
}

// downloadTrickplay names the scrubbing preview set of a download's file in its
// bundle: the current set made from that very file, when there is one.
// Previews are an extra: anything that stops them leaves the bundle without.
func (d Dependencies) downloadTrickplay(ctx context.Context, p identity.Principal, item, source string) *downloads.BundleTrickplay {
	library, e := d.Catalog.WithContext(ctx).LibraryForItem(item)
	if e != nil {
		return nil
	}
	view, e := d.Analysis.Trickplay(ctx, d.downloadAnalysis(p, library, item))
	if e != nil {
		return nil
	}
	for _, set := range view.Sets {
		if set.SourceID != source || set.Stale || set.TileCount < 1 || set.TileCount > downloads.MaxBundleTrickplayTiles {
			continue
		}
		out := &downloads.BundleTrickplay{ID: set.ID, TileWidth: set.TileWidth, TileHeight: set.TileHeight, Columns: set.Columns, Rows: set.Rows, IntervalSeconds: set.IntervalSeconds, FrameCount: set.FrameCount, DurationSeconds: set.DurationSeconds, SourceOffsetSeconds: set.SourceOffsetSeconds,
			Thumbnails: downloads.BundleFile{Path: downloads.TrickplayThumbnailsPath(set.ID), ContentType: "text/vtt"}, Tiles: make([]downloads.BundleFile, 0, set.TileCount)}
		for tile := 0; tile < set.TileCount; tile++ {
			out.Tiles = append(out.Tiles, downloads.BundleFile{Path: downloads.TrickplayTilePath(set.ID, tile), ContentType: "image/jpeg"})
		}
		return out
	}
	return nil
}

// downloadPreviewError says what a failed preview read means to the device:
// no such image is an ordinary answer, a sign-in that ended renews the address.
func downloadPreviewError(e error) error {
	switch {
	case errors.Is(e, identity.ErrUnauthorized), errors.Is(e, identity.ErrForbidden):
		return &apierror.Error{Code: "invalid_download_grant", Cause: e}
	case errors.Is(e, context.DeadlineExceeded), errors.Is(e, context.Canceled):
		return &apierror.Error{Code: "download_not_ready", RetryAfterSeconds: 30, Cause: e}
	}
	return &apierror.Error{Code: "not_found", Cause: e}
}

// subtitleDownloadError says what a failed read of a download's subtitle file
// means to the device fetching it.
func subtitleDownloadError(e error) error {
	switch {
	case errors.Is(e, subtitles.ErrInput), errors.Is(e, identity.ErrNotVisible), errors.Is(e, sql.ErrNoRows), errors.Is(e, subtitles.ErrUnsupported):
		// This title has no such track or file: an ordinary answer.
		return &apierror.Error{Code: "not_found", Cause: e}
	case errors.Is(e, identity.ErrUnauthorized), errors.Is(e, identity.ErrForbidden):
		// The sign-in the address stands for has ended. That is not "no such
		// track": the device renews the address under its current sign-in and
		// asks again.
		return &apierror.Error{Code: "invalid_download_grant", Cause: e}
	case errors.Is(e, subtitles.ErrUnavailable), errors.Is(e, subtitles.ErrCapacity), errors.Is(e, context.DeadlineExceeded), errors.Is(e, context.Canceled):
		// The track is still being read out of its file. Ask again shortly.
		return &apierror.Error{Code: "download_not_ready", RetryAfterSeconds: 30, Cause: e}
	}
	return e
}

// subtitleFiles names what a device fetches for one track, keyed by form. The
// form a device draws itself keeps its own name ("ass", "pgs", "vobsub",
// "dvb"); "sub" is the pictures beside a VobSub index; "font0" onward are a
// styled script's fonts.
func subtitleFiles(track subtitles.DownloadTrack) map[string]downloads.BundleFile {
	files := map[string]downloads.BundleFile{}
	base := "subtitles/" + track.ID + "/" + strconv.FormatInt(track.Revision, 10)
	if track.Text {
		files["vtt"] = downloads.BundleFile{Path: downloads.SubtitlePath(track.ID, track.Revision, "vtt"), ContentType: "text/vtt"}
	}
	switch track.Original {
	case "ass":
		files["ass"] = downloads.BundleFile{Path: base + "/original", ContentType: "text/x-ssa"}
		for i := 0; i < track.Fonts && i < 16; i++ {
			files["font"+strconv.Itoa(i)] = downloads.BundleFile{Path: base + "/font-" + strconv.Itoa(i), ContentType: "font/ttf"}
		}
	case "pgs", "dvb":
		files[track.Original] = downloads.BundleFile{Path: base + "/original", ContentType: "application/octet-stream"}
	case "vobsub":
		files["vobsub"] = downloads.BundleFile{Path: base + "/original", ContentType: "text/plain"}
		if track.Companion {
			files["sub"] = downloads.BundleFile{Path: base + "/companion", ContentType: "application/octet-stream"}
		}
	}
	return files
}

// contentDisposition names the file for a person saving it. The plain name
// keeps to what every browser reads; the readable one travels in the encoded
// form beside it (RFC 6266). A name is title text, so it may hold a quote, a
// backslash, a line break or characters outside ASCII: the quoted form carries
// a safe ASCII rendering of the plain name, never the text as it came, and
// the encoded form carries the name exactly.
func contentDisposition(readable, plain string) string {
	safe := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, plain)
	if readable == "" {
		readable = plain
	}
	if safe == "" {
		safe = "download"
	}
	out := "attachment; filename=\"" + safe + "\""
	if readable == "" || readable == safe {
		return out
	}
	encoded := make([]byte, 0, len(readable)*3)
	for i := 0; i < len(readable); i++ {
		c := readable[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			encoded = append(encoded, c)
			continue
		}
		const hex = "0123456789ABCDEF"
		encoded = append(encoded, '%', hex[c>>4], hex[c&15])
	}
	return out + "; filename*=UTF-8''" + string(encoded)
}
