package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// administrationFailure maps this area's errors onto stable codes. A validation
// failure names the exact fields so a client can mark them.
func administrationFailure(w http.ResponseWriter, err error) {
	if writerRefused(w, err) {
		return
	}
	if revisionFailure(w, err) {
		return
	}
	var validation *administration.ValidationError
	if errors.As(err, &validation) {
		write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_administration_input", "message": publicErrorMessage("invalid_administration_input"), "fields": validation.Fields, "retryable": false}})
		return
	}
	// An error this area does not name is a server fault, not the caller's: it is
	// logged with its cause and answered with a code, never with the raw text of
	// a storage failure.
	status, code, message, retryable := 500, "administration_failed", "The administration request could not be completed.", true
	switch {
	// CD-51: a refused but valid session is 403 (or a hidden 404), never 401.
	case errors.Is(err, identity.ErrNotVisible):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(err, identity.ErrProfilePINReentry), errors.Is(err, identity.ErrProfilePINManage):
		status, code, _ = identity.Refusal(err)
		message = err.Error()
	case errors.Is(err, identity.ErrForbidden):
		status, code, message = 403, "forbidden", identity.ErrForbidden.Error()
	case errors.Is(err, errFeatureRestricted):
		status, code, message, retryable = 403, "feature_restricted", publicErrorMessage("feature_restricted"), false
	case errors.Is(err, identity.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Authentication is required."
	case errors.Is(err, administration.ErrDenied):
		status, code = 403, "administration_denied"
	case errors.Is(err, administration.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, administration.ErrIdempotencyKeyReused):
		status, code, message = 409, "idempotency_key_reused", "This request conflicts with an earlier request."
	case errors.Is(err, administration.ErrConflict):
		status, code = 409, "administration_conflict"
	case errors.Is(err, administration.ErrConfirmation):
		status, code = 409, "confirmation_mismatch"
	case errors.Is(err, administration.ErrCursor):
		status, code = 409, "stale_continuation"
	case errors.Is(err, administration.ErrImage):
		status, code, message = 415, "unsupported_image", "Upload a JPEG, PNG or WebP image no larger than 10 MiB."
	case errors.Is(err, administration.ErrBusy):
		status, code, retryable = 409, "administration_busy", true
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.Is(err, administration.ErrUnavailable):
		status, code, retryable = 503, "administration_unavailable", true
	case errors.Is(err, administration.ErrInput):
		status, code, message, retryable = 400, "invalid_administration_input", publicErrorMessage("invalid_administration_input"), false
	case errors.Is(err, sql.ErrNoRows):
		status, code, message, retryable = 404, "not_found", administration.ErrNotFound.Error(), false
	}
	if status < 500 && code != "invalid_administration_input" && code != "not_found" && code != "idempotency_key_reused" {
		message = publicErrorMessage(code)
	}
	if status >= 500 {
		log.Printf("administration request failed (%d %s): %v", status, code, err)
		w.Header().Set("Retry-After", "2")
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable}})
}

// administrationAuthority re-checks the same owner inside the write's own
// transaction, so a revoked session cannot complete a mutation that started
// while it was still valid.
func (d Dependencies) administrationAuthority(expected identity.Principal) administration.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		current, err := d.Identity.ReauthorizeTx(ctx, tx, expected)
		if err != nil {
			return identity.ErrUnauthorized
		}
		return d.ownerAuthorityTx(ctx, tx, current)
	}
}

// A selection delete is confined to one library, under the same snapshot and
// owner fence used to build its preview and apply the confirmed operation.
func (d Dependencies) deletionAuthority(expected identity.Principal, ids []string) administration.Authorize {
	owner := d.administrationAuthority(expected)
	raw, _ := json.Marshal(ids)
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := owner(ctx, tx); err != nil {
			return err
		}
		for _, id := range ids {
			if _, valid := entityid.Decode(id); !valid {
				return administration.ErrNotFound
			}
		}
		var libraries int
		if err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT library_id) FROM catalog_entities WHERE public_id IN(SELECT pid_blob(value) FROM json_each(?))`, string(raw)).Scan(&libraries); err != nil {
			return err
		}
		if libraries > 1 {
			return administration.ErrInput
		}
		return nil
	}
}

// administrationQuery accepts exactly the named parameters and nothing else, so
// a typo is a refusal rather than a silently ignored filter.
func administrationQuery(r *http.Request, allowed ...string) (map[string]string, error) {
	out := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) != 1 || !contains(allowed, key) {
			return nil, administration.ErrInput
		}
		out[key] = values[0]
	}
	return out, nil
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func administrationLimit(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		// An explicit limit must be 1-200 (the published Limit parameter); only
		// an absent one means the default page.
		return 0, &administration.ValidationError{Fields: []string{"limit"}}
	}
	return limit, nil
}

// administrationHandler is the shared shape: owner in, work, owner again, write.
// The second check closes the window between authorising and answering.
func (d Dependencies) administrationHandler(timeout time.Duration, work func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc {
	return d.administrationTierHandler(false, timeout, work)
}

func (d Dependencies) libraryAdministrationHandler(timeout time.Duration, work func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc {
	return d.administrationTierHandler(true, timeout, work)
}

func (d Dependencies) administrationTierHandler(library bool, timeout time.Duration, work func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc {
	authorize := d.owner
	if library {
		authorize = d.administrator
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		if d.Administration == nil {
			administrationFailure(w, administration.ErrUnavailable)
			return
		}
		p, err := authorize(r.WithContext(ctx))
		if err != nil {
			administrationFailure(w, err)
			return
		}
		result, err := work(ctx, w, r, p)
		if err != nil {
			administrationFailure(w, err)
			return
		}
		if result == nil {
			// The handler already wrote its own response body (a download, an
			// image); nothing further to send.
			return
		}
		if _, err = authorize(r.WithContext(ctx)); err != nil {
			administrationFailure(w, err)
			return
		}
		write(w, 200, map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "result": result})
	}
}

func administrationActor(p identity.Principal) string {
	return p.Authority + ":" + p.AccountID + ":" + p.ProfileID
}

func (d Dependencies) administrationRoutes(mux *http.ServeMux) {
	d.deletionRoutes(mux)
	d.libraryAdministrationRoutes(mux)
	d.liveAdministrationRoutes(mux)
	d.dvrAdministrationRoutes(mux)
	d.channelAdministrationRoutes(mux)
	d.maintenanceRoutes(mux)
}

// ---------------------------------------------------------------- deletion

func (d Dependencies) deletionRoutes(mux *http.ServeMux) {
	preview := func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal, ids []string) (any, error) {
		return d.Administration.PreviewDelete(ctx, d.deletionAuthority(p, ids), ids)
	}
	d.httpHandle(mux, "POST /v1/items/{id}/delete/preview", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		return preview(ctx, w, r, p, []string{r.PathValue("id")})
	}))
	d.httpHandle(mux, "POST /v1/admin/media/delete/preview", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body struct {
			ItemIDs []string `json:"itemIds"`
		}
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return preview(ctx, w, r, p, body.ItemIDs)
	}))
	d.httpHandle(mux, "POST /v1/items/{id}/delete", d.administrationHandler(120*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.DeleteRequest
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		if len(body.ItemIDs) > 0 {
			// The item is named in the path; a body list would make it ambiguous.
			return nil, administration.ErrInput
		}
		body.ItemIDs = []string{r.PathValue("id")}
		return d.Administration.Delete(ctx, d.deletionAuthority(p, body.ItemIDs), administrationActor(p), body)
	}))
	d.httpHandle(mux, "POST /v1/admin/media/delete", d.administrationHandler(300*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.DeleteRequest
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.Delete(ctx, d.deletionAuthority(p, body.ItemIDs), administrationActor(p), body)
	}))
}

// ---------------------------------------------------------------- libraries

func (d Dependencies) libraryAdministrationRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/filesystem", d.libraryAdministrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "path", "cursor", "limit", "includeFiles", "showHidden", "prefix", "checkWritable")
		if err != nil {
			return nil, err
		}
		limit, err := administrationLimit(query["limit"])
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"includeFiles", "showHidden", "checkWritable"} {
			if v := query[key]; v != "" && v != "true" && v != "false" && v != "1" && v != "0" {
				return nil, administration.ErrInput
			}
		}
		flag := func(key string) bool { return query[key] == "true" || query[key] == "1" }
		page, err := d.Administration.Browse(ctx, query["path"], query["cursor"], limit, flag("includeFiles"), administration.BrowseOptions{ShowHidden: flag("showHidden"), Prefix: query["prefix"], CheckWritable: flag("checkWritable")})
		if page.Reason != "" && (errors.Is(err, administration.ErrDenied) || errors.Is(err, administration.ErrNotFound)) {
			return page, nil
		}
		return page, err
	}))
	d.httpHandle(mux, "GET /v1/admin/library-settings", d.libraryAdministrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "cursor", "limit")
		if err != nil {
			return nil, err
		}
		limit, err := administrationLimit(query["limit"])
		if err != nil {
			return nil, err
		}
		return d.Administration.LibraryIndex(ctx, d.libraryAdministrationAuthority(p), query["cursor"], limit, d.libraryAdministrationScope(p))
	}))
	d.httpHandle(mux, "GET /v1/admin/analysis-operations", d.libraryAdministrationHandler(15*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		return administration.Matrix(), nil
	}))
	d.httpHandle(mux, "GET /v1/admin/metadata-providers", d.libraryAdministrationHandler(15*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		return map[string]any{"providers": administration.MetadataProviders()}, nil
	}))

}

// ---------------------------------------------------------------- live sources

func (d Dependencies) liveAdministrationRoutes(mux *http.ServeMux) {

	d.httpHandle(mux, "GET /v1/admin/live-sources/{id}/channel-map", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "cursor", "limit", "channelId")
		if err != nil {
			return nil, err
		}
		limit, err := administrationLimit(query["limit"])
		if err != nil {
			return nil, err
		}
		channelID := query["channelId"]
		if _, given := query["channelId"]; given && channelID == "" {
			return nil, &administration.ValidationError{Fields: []string{"channelId"}}
		}
		return d.Administration.ChannelMap(ctx, d.administrationAuthority(p), r.PathValue("id"), query["cursor"], channelID, limit)
	}))
	d.httpHandle(mux, "PUT /v1/admin/live-sources/{id}/channel-map", d.administrationHandler(60*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.ChannelMapChange
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.SaveChannelMap(ctx, d.administrationAuthority(p), r.PathValue("id"), body)
	}))
	d.httpHandle(mux, "POST /v1/admin/live-sources/{id}/logos", d.administrationHandler(120*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body struct {
			OperationID string `json:"operationId"`
		}
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.ImportLogos(ctx, d.administrationAuthority(p), r.PathValue("id"), body.OperationID)
	}))
	d.httpHandle(mux, "POST /v1/admin/live-sources/discover", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body struct {
			WindowMs int `json:"windowMs"`
		}
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		if body.WindowMs < 0 || body.WindowMs > 10000 {
			return nil, administration.ErrInput
		}
		return d.Administration.DiscoverTuners(ctx, d.administrationAuthority(p), time.Duration(body.WindowMs)*time.Millisecond)
	}))
}

// ---------------------------------------------------------------- DVR

func (d Dependencies) dvrAdministrationRoutes(mux *http.ServeMux) {
	d.recordingPermissionRoutes(mux)
	d.recordingOwnersRoute(mux)
	d.httpHandle(mux, "GET /v1/admin/dvr/settings", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		return d.Administration.DVRSettings(ctx, d.administrationAuthority(p))
	}))
	d.httpHandle(mux, "PUT /v1/admin/dvr/settings", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.Change[administration.DVRDefaults]
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.SaveDVRSettings(ctx, d.administrationAuthority(p), body)
	}))

	d.httpHandle(mux, "GET /v1/admin/dvr/recording-groups", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "cursor", "limit")
		if err != nil {
			return nil, err
		}
		limit, err := administrationLimit(query["limit"])
		if err != nil {
			return nil, err
		}
		return d.Administration.RecordingGroups(ctx, d.administrationAuthority(p), query["cursor"], limit)
	}))
	d.httpHandle(mux, "POST /v1/admin/dvr/recording-groups", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.RecordingGroupChange
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.SaveRecordingGroup(ctx, d.administrationAuthority(p), "", body)
	}))
	d.httpHandle(mux, "PUT /v1/admin/dvr/recording-groups/{id}", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.RecordingGroupChange
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.SaveRecordingGroup(ctx, d.administrationAuthority(p), r.PathValue("id"), body)
	}))
	d.httpHandle(mux, "DELETE /v1/admin/dvr/recording-groups/{id}", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "expectedRevision", "operationId")
		if err != nil {
			return nil, err
		}
		if query["expectedRevision"] == "" {
			return nil, errRevisionRequired
		}
		revision, err := strconv.ParseInt(query["expectedRevision"], 10, 64)
		if err != nil {
			return nil, administration.ErrInput
		}
		if err = d.Administration.DeleteRecordingGroup(ctx, d.administrationAuthority(p), r.PathValue("id"), revision, query["operationId"]); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "id": r.PathValue("id")}, nil
	}))
}

// ---------------------------------------------------------------- channels

func (d Dependencies) channelAdministrationRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/library-channels/criteria", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "libraryId", "field", "cursor", "limit")
		if err != nil {
			return nil, err
		}
		limit, err := administrationLimit(query["limit"])
		if err != nil {
			return nil, err
		}
		return d.Administration.Criteria(ctx, d.administrationAuthority(p), query["libraryId"], query["field"], query["cursor"], limit)
	}))
	d.httpHandle(mux, "GET /v1/admin/library-channels/block-presets", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "cursor", "limit")
		if err != nil {
			return nil, err
		}
		limit, err := administrationLimit(query["limit"])
		if err != nil {
			return nil, err
		}
		return d.Administration.BlockPresets(ctx, d.administrationAuthority(p), query["cursor"], limit)
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/block-presets", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.BlockPresetChange
		if err := decode(w, r, &body); err != nil {
			return nil, administration.ErrInput
		}
		return d.Administration.SaveBlockPreset(ctx, d.administrationAuthority(p), "", body)
	}))
	d.httpHandle(mux, "PUT /v1/admin/library-channels/block-presets/{id}", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.BlockPresetChange
		if err := decode(w, r, &body); err != nil {
			return nil, administration.ErrInput
		}
		return d.Administration.SaveBlockPreset(ctx, d.administrationAuthority(p), r.PathValue("id"), body)
	}))
	d.httpHandle(mux, "DELETE /v1/admin/library-channels/block-presets/{id}", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := administrationQuery(r, "expectedRevision", "operationId")
		if err != nil {
			return nil, err
		}
		revision, err := strconv.ParseInt(query["expectedRevision"], 10, 64)
		if err != nil {
			return nil, administration.ErrInput
		}
		if err = d.Administration.DeleteBlockPreset(ctx, d.administrationAuthority(p), r.PathValue("id"), revision, query["operationId"]); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "id": r.PathValue("id")}, nil
	}))
	d.httpHandle(mux, "GET /v1/admin/library-channels/{id}/health", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		return d.Administration.ChannelHealthFor(ctx, d.administrationAuthority(p), r.PathValue("id"))
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/{id}/logo", d.administrationHandler(60*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		// The upload carries its fields in the multipart body, so a query string
		// here is a mistake rather than a filter.
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		raw, revision, operationID, err := readLogoUpload(w, r)
		if err != nil {
			return nil, err
		}
		return d.Administration.UploadChannelLogo(ctx, d.administrationAuthority(p), r.PathValue("id"), raw, revision, operationID)
	}))
	// A channel logo is part of what a viewer sees, so this read is the one
	// endpoint on these pages that is not owner-only.
	d.httpHandle(mux, "GET /v1/library-channels/{id}/logo", func(w http.ResponseWriter, r *http.Request) {
		if d.Administration == nil {
			administrationFailure(w, administration.ErrUnavailable)
			return
		}
		if _, err := d.principal(r); err != nil {
			administrationFailure(w, err)
			return
		}
		raw, mime, digest, err := d.Administration.ChannelLogoBytes(r.Context(), r.PathValue("id"))
		if err != nil {
			administrationFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("ETag", `"`+digest+`"`)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if strings.Contains(r.Header.Get("If-None-Match"), digest) {
			w.WriteHeader(304)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})
}

// logoUploadEnvelope bounds the multipart wrapper around one image.
const logoUploadEnvelope = 1 << 20

func readLogoUpload(w http.ResponseWriter, r *http.Request) ([]byte, *int64, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, administration.LogoUploadBytes+logoUploadEnvelope)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, nil, "", administration.ErrImage
	}
	var raw []byte
	var revision *int64
	var operationID string
	seen := make(map[string]bool)
	controlBytes := int64(0)
	for {
		// NextRawPart preserves the original transfer-encoding header, including
		// quoted-printable and duplicate values. Never decode before admission.
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, "", administration.ErrImage
		}
		name := part.FormName()
		if (name != "file" && name != "expectedRevision" && name != "operationId") || seen[name] || len(part.Header.Values("Content-Disposition")) != 1 {
			return nil, nil, "", administration.ErrInput
		}
		seen[name] = true
		encodings := part.Header.Values("Content-Transfer-Encoding")
		if len(encodings) > 1 {
			return nil, nil, "", administration.ErrImage
		}
		encoding := strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")))
		if encoding != "" && encoding != "binary" && encoding != "8bit" && (encoding != "base64" || name != "file") {
			return nil, nil, "", administration.ErrImage
		}
		limit := int64(logoUploadEnvelope) - controlBytes
		if name == "file" {
			if part.FileName() == "" {
				return nil, nil, "", administration.ErrImage
			}
			limit = administration.LogoUploadBytes
		} else if part.FileName() != "" {
			return nil, nil, "", administration.ErrInput
		}
		value, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil || int64(len(value)) > limit {
			return nil, nil, "", administration.ErrImage
		}
		if err := part.Close(); err != nil {
			return nil, nil, "", administration.ErrImage
		}
		if name == "file" {
			raw = value
			if encoding == "base64" {
				if strings.ContainsAny(string(raw), "\r\n") {
					return nil, nil, "", administration.ErrImage
				}
				raw, err = base64.StdEncoding.Strict().DecodeString(string(raw))
				if err != nil || len(raw) == 0 || len(raw) > administration.LogoUploadBytes {
					return nil, nil, "", administration.ErrImage
				}
			}
		} else {
			controlBytes += int64(len(value))
			if name == "operationId" {
				operationID = string(value)
			} else {
				number, err := strconv.ParseInt(string(value), 10, 64)
				if err != nil || number < 0 {
					return nil, nil, "", administration.ErrInput
				}
				revision = &number
			}
		}
	}
	if !seen["file"] {
		return nil, nil, "", administration.ErrImage
	}
	return raw, revision, operationID, nil
}

// ---------------------------------------------------------------- maintenance

func (d Dependencies) maintenanceRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/storage-usage", d.administrationHandler(120*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		return d.Administration.Storage(ctx, d.administrationAuthority(p))
	}))
	d.httpHandle(mux, "POST /v1/admin/storage-usage/{category}/cleanup", d.administrationHandler(300*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body administration.CleanupRequest
		if err := decodeLegacyRevision(w, r, &body); err != nil {
			return nil, errors.Join(administration.ErrInput, err)
		}
		return d.Administration.Cleanup(ctx, d.administrationAuthority(p), r.PathValue("category"), body)
	}))
}

// updatePolicy reads the release channel and feed URL from the owner settings
// registry, which is where every server-wide setting lives.
func (d Dependencies) updatePolicy(ctx context.Context, p identity.Principal) (string, string) {
	channel, feed := "stable", ""
	if d.Console == nil {
		return channel, feed
	}
	document, err := d.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		return channel, feed
	}
	if document.Effective.UpdateChannel != "" {
		channel = document.Effective.UpdateChannel
	}
	return channel, document.Effective.UpdateFeedURL
}
