package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// AuthorizeSubtitles shares the established transactional identity/library gate.
// Exported only for startup composition; it does not introduce new authority.
func (d Dependencies) AuthorizeSubtitles(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
	return d.playbackAuthorityTx(ctx, tx, p, item)
}
func subtitleHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// sessionNotVisible: the subtitle store answers "unauthorized" for a playback
// session the caller can't reach (not its own, ended, or unknown). The caller's
// credential was already accepted, so that is a hidden 404, never a 401 that
// would send a signed-in client to the sign-in screen (CD-51, NEW-28).
func sessionNotVisible(e error) error {
	if errors.Is(e, identity.ErrUnauthorized) {
		return identity.ErrNotVisible
	}
	return e
}

func subtitleFailure(w http.ResponseWriter, e error) {
	code := "subtitle_unavailable"
	seconds := 0
	var refused *subtitles.ProviderError
	switch {
	case errors.As(e, &refused):
		code = providerFailureCode(refused.Reason)
		// The quota's reset or the end of a rate limit: the client says when.
		if !refused.RetryAt.IsZero() {
			seconds = max(1, int(time.Until(refused.RetryAt).Seconds()+0.5))
		}
	case errors.Is(e, identity.ErrNotVisible):
		code = "not_found"
	case errors.Is(e, identity.ErrForbidden):
		code = "not_permitted"
	case errors.Is(e, identity.ErrUnauthorized):
		code = "unauthorized"
	case errors.Is(e, sql.ErrNoRows):
		code = "subtitle_not_found"
	case errors.Is(e, decoder.ErrConfinementUnavailable):
		code = "subtitle_renderer_platform_unsupported"
	case errors.Is(e, subtitles.ErrRendererConfiguration), errors.Is(e, decoder.ErrInvalidConfiguration):
		code = "subtitle_renderer_configuration"
	case errors.Is(e, subtitles.ErrConflict):
		code = "subtitle_conflict"
	case errors.Is(e, subtitles.ErrOperation):
		code = "subtitle_operation_conflict"
	case errors.Is(e, subtitles.ErrCapacity):
		code = "subtitle_capacity"
	case errors.Is(e, subtitles.ErrUnsupported), errors.Is(e, subtitles.ErrText), errors.Is(e, subtitles.ErrTiming):
		code = "subtitle_unsupported"
	case errors.Is(e, subtitles.ErrInput):
		code = "subtitle_invalid_input"
	}
	if code == "subtitle_unavailable" {
		seconds = 2
	}
	policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: code, RetryAfterSeconds: seconds})
}
func providerFailureCode(reason string) string {
	switch reason {
	case subtitles.ReasonNotConfigured:
		return "subtitle_provider_not_configured"
	case subtitles.ReasonKeyRejected:
		return "subtitle_provider_key_rejected"
	case subtitles.ReasonSignInFailed:
		return "subtitle_provider_sign_in_failed"
	case subtitles.ReasonSignInRequired:
		return "subtitle_provider_sign_in_required"
	case subtitles.ReasonQuotaExhausted:
		return "subtitle_provider_quota"
	case subtitles.ReasonRateLimited:
		return "subtitle_provider_busy"
	}
	return "subtitle_unavailable"
}
func subtitleDecode(w http.ResponseWriter, r *http.Request, out any) error {
	typ, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || typ != "application/json" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		return subtitles.ErrInput
	}
	r.Body = http.MaxBytesReader(w, r.Body, subtitles.MaxAssetBytes+16384)
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		return subtitles.ErrCapacity
	}
	return subtitleStrictJSON(raw, out)
}

// subtitleStrictJSON decodes one strict UTF-8 JSON object with no unknown or
// trailing fields.
func subtitleStrictJSON(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return subtitles.ErrInput
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	if e := strictJSONValue(tokens, 0); e != nil {
		return subtitles.ErrInput
	}
	if _, e := tokens.Token(); e != io.EOF {
		return subtitles.ErrInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return subtitles.ErrInput
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return subtitles.ErrInput
	}
	return nil
}

// subtitleMetadataBytes bounds a multipart upload's JSON metadata part.
const subtitleMetadataBytes = 16384

// subtitleMutationDecode reads a subtitle save as JSON (content or base64
// data), or as multipart/form-data (IM04): a bounded JSON "metadata" part
// first, then the raw "file" and, for a VobSub pair, its "companion". Parts
// are read one at a time, in order and unencoded, so a binary file crosses
// the wire once rather than as base64 inside JSON.
func subtitleMutationDecode(w http.ResponseWriter, r *http.Request, m *subtitles.Mutation) error {
	typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || typ != "multipart/form-data" {
		return subtitleDecode(w, r, m)
	}
	limitBytes := subtitles.BinaryLimitBytes()
	r.Body = http.MaxBytesReader(w, r.Body, int64(limitBytes)+subtitleMetadataBytes+16384)
	reader, e := r.MultipartReader()
	if e != nil {
		return subtitles.ErrInput
	}
	seen := map[string]bool{}
	files := 0
	for {
		// NextRawPart keeps any transfer encoding visible: only unencoded parts
		// are admitted.
		part, e := reader.NextRawPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return subtitleUploadFailure(e)
		}
		name := part.FormName()
		if name != "metadata" && name != "file" && name != "companion" || seen[name] || name != "metadata" && !seen["metadata"] || name == "companion" && !seen["file"] || len(part.Header.Values("Content-Disposition")) != 1 {
			return subtitles.ErrInput
		}
		seen[name] = true
		if encodings := part.Header.Values("Content-Transfer-Encoding"); len(encodings) > 1 || len(encodings) == 1 && !strings.EqualFold(strings.TrimSpace(encodings[0]), "binary") && !strings.EqualFold(strings.TrimSpace(encodings[0]), "8bit") {
			return subtitles.ErrInput
		}
		limit := int64(subtitleMetadataBytes)
		if name != "metadata" {
			limit = int64(limitBytes - files)
		} else if part.FileName() != "" {
			return subtitles.ErrInput
		} else if typ := part.Header.Get("Content-Type"); typ != "" {
			if media, params, e := mime.ParseMediaType(typ); e != nil || media != "application/json" && media != "text/plain" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
				return subtitles.ErrInput
			}
		}
		value, e := io.ReadAll(io.LimitReader(part, limit+1))
		if e != nil {
			return subtitleUploadFailure(e)
		}
		if int64(len(value)) > limit {
			return subtitles.ErrCapacity
		}
		if e = part.Close(); e != nil {
			return subtitleUploadFailure(e)
		}
		switch name {
		case "metadata":
			if e = subtitleStrictJSON(value, m); e != nil {
				return e
			}
			// The bytes travel as their own parts, never inside the metadata.
			if m.Content != nil || m.Data != nil || m.Companion != nil {
				return subtitles.ErrInput
			}
		case "file":
			if len(value) == 0 {
				return subtitles.ErrInput
			}
			m.Data = value
			files += len(value)
		case "companion":
			if len(value) == 0 {
				return subtitles.ErrInput
			}
			m.Companion = value
			files += len(value)
		}
	}
	if !seen["file"] {
		return subtitles.ErrInput
	}
	return nil
}

// subtitleUploadFailure is a multipart read failure: an oversize body is a
// capacity refusal, anything else malformed input.
func subtitleUploadFailure(e error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(e, &tooLarge) {
		return subtitles.ErrCapacity
	}
	return subtitles.ErrInput
}
func (d Dependencies) registerSubtitleRoutes(registry *apikit.Registry) error {
	var registrationError error
	type handler func(http.ResponseWriter, *http.Request, identity.Principal) (any, error)
	register := func(pattern string, fn handler) {
		serve := func(w http.ResponseWriter, r *http.Request) {
			subtitleHeaders(w)
			if d.Subtitles == nil {
				subtitleFailure(w, subtitles.ErrUnavailable)
				return
			}
			p, e := d.principal(r)
			if e != nil {
				subtitleFailure(w, e)
				return
			}
			out, e := fn(w, r, p)
			if e != nil {
				subtitleFailure(w, e)
				return
			}
			write(w, 200, out)
		}
		if registrationError != nil {
			return
		}
		switch pattern {
		case "GET /v1/items/{itemId}/subtitles":
			registrationError = registerSubtitleRaw[struct{}, subtitles.Catalog](registry, pattern, serve)
		case "POST /v1/items/{itemId}/subtitles", "PUT /v1/items/{itemId}/subtitles/{resource}":
			registrationError = registerSubtitleRaw[subtitles.Mutation, subtitles.Receipt](registry, pattern, serve)
		case "DELETE /v1/items/{itemId}/subtitles/{resource}":
			registrationError = registerSubtitleRaw[subtitles.DeleteMutation, subtitles.Receipt](registry, pattern, serve)
		case "POST /v1/items/{itemId}/subtitles/import":
			registrationError = registerSubtitleRaw[subtitles.ImportRequest, subtitles.Receipt](registry, pattern, serve)
		case "POST /v1/items/{itemId}/subtitles/refresh":
			registrationError = registerSubtitleRaw[SubtitleRefreshRequest, subtitles.Catalog](registry, pattern, serve)
		case "POST /v1/items/{itemId}/subtitles/remote":
			registrationError = registerSubtitleRaw[subtitles.RemoteRequest, subtitles.Receipt](registry, pattern, serve)
		case "POST /v1/items/{itemId}/subtitles/search":
			registrationError = registerSubtitleRaw[subtitles.SearchRequest, subtitles.SearchResult](registry, pattern, serve)
		case "POST /v1/items/{itemId}/subtitles/apply":
			registrationError = registerSubtitleRaw[subtitles.ApplyRequest, subtitles.Receipt](registry, pattern, serve)
		case "GET /v1/items/{itemId}/playback/{session}/subtitles":
			registrationError = registerSubtitleRaw[struct{}, subtitles.PlaybackPlan](registry, pattern, serve)
		}
	}
	register("GET /v1/items/{itemId}/subtitles", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		return d.Subtitles.List(r.Context(), p, r.PathValue("itemId"))
	})
	register("POST /v1/items/{itemId}/subtitles", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.Mutation
		if e := subtitleMutationDecode(w, r, &m); e != nil {
			return nil, e
		}
		if m.ResourceID != "" {
			return nil, subtitles.ErrInput
		}
		return d.Subtitles.Mutate(r.Context(), p, r.PathValue("itemId"), m)
	})
	register("PUT /v1/items/{itemId}/subtitles/{resource}", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.Mutation
		if e := subtitleMutationDecode(w, r, &m); e != nil {
			return nil, e
		}
		if m.ResourceID != "" && m.ResourceID != r.PathValue("resource") {
			return nil, subtitles.ErrInput
		}
		m.ResourceID = r.PathValue("resource")
		return d.Subtitles.Mutate(r.Context(), p, r.PathValue("itemId"), m)
	})
	register("DELETE /v1/items/{itemId}/subtitles/{resource}", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.DeleteMutation
		if e := subtitleDecode(w, r, &m); e != nil {
			return nil, e
		}
		return d.Subtitles.Delete(r.Context(), p, r.PathValue("itemId"), r.PathValue("resource"), m)
	})
	register("POST /v1/items/{itemId}/subtitles/import", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.ImportRequest
		if e := subtitleDecode(w, r, &m); e != nil {
			return nil, e
		}
		return d.Subtitles.Import(r.Context(), p, r.PathValue("itemId"), m)
	})
	register("POST /v1/items/{itemId}/subtitles/refresh", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m struct {
			SourceID string `json:"sourceId"`
		}
		if e := subtitleDecode(w, r, &m); e != nil {
			return nil, e
		}
		return d.Subtitles.Refresh(r.Context(), p, r.PathValue("itemId"), m.SourceID)
	})
	register("POST /v1/items/{itemId}/subtitles/remote", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.RemoteRequest
		if e := subtitleDecode(w, r, &m); e != nil {
			return nil, e
		}
		return d.Subtitles.ImportRemote(r.Context(), p, r.PathValue("itemId"), m)
	})
	register("POST /v1/items/{itemId}/subtitles/search", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.SearchRequest
		if e := subtitleDecode(w, r, &m); e != nil {
			return nil, e
		}
		return d.Subtitles.Search(r.Context(), p, r.PathValue("itemId"), m)
	})
	register("POST /v1/items/{itemId}/subtitles/apply", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var m subtitles.ApplyRequest
		if e := subtitleDecode(w, r, &m); e != nil {
			return nil, e
		}
		return d.Subtitles.Apply(r.Context(), p, r.PathValue("itemId"), m)
	})
	// A v1 client names its v1 session and presentation generation; the plan
	// lives on the media session presenting it (playback_v1_presentation.go).
	register("GET /v1/items/{itemId}/playback/{session}/subtitles", func(w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		presentation, v1, e := d.resolveV1Presentation(r.Context(), p, r.PathValue("session"))
		if e != nil {
			return nil, e
		}
		if !v1 {
			out, e := d.Subtitles.Plan(r.Context(), p, r.PathValue("itemId"), r.PathValue("session"))
			return out, sessionNotVisible(e)
		}
		out, e := d.Subtitles.Plan(r.Context(), presentation.as(p), r.PathValue("itemId"), presentation.media)
		if e == nil {
			out.SessionID, out.Generation = presentation.session, presentation.generation
		}
		return out, sessionNotVisible(e)
	})
	if registrationError != nil {
		return registrationError
	}
	documentHandler := func(w http.ResponseWriter, r *http.Request) {
		if d.Subtitles == nil || d.Playback == nil {
			subtitleFailure(w, subtitles.ErrUnavailable)
			return
		}
		subtitleHeaders(w)
		isVTT := strings.HasSuffix(r.URL.Path, ".vtt")
		revisionText := strings.TrimSuffix(r.PathValue("revision"), ".vtt")
		revision, e := strconv.ParseInt(revisionText, 10, 64)
		if e != nil || revision < 1 || strconv.FormatInt(revision, 10) != revisionText {
			subtitleFailure(w, subtitles.ErrInput)
			return
		}
		_, p, item, e := d.Playback.ResolveGrantContext(r.Context(), r.PathValue("grant"))
		if e != nil {
			subtitleFailure(w, e)
			return
		}
		var session string
		e = d.DB.QueryRowContext(r.Context(), `SELECT id FROM playback_sessions WHERE grant_hash=?`, identity.Digest(r.PathValue("grant"))).Scan(&session)
		if e != nil {
			subtitleFailure(w, e)
			return
		}
		id := r.PathValue("resource")
		if isVTT {
			// One sealed document (at most 4 MiB), filtered to one 60s window.
			segment := -1
			if raw := r.URL.Query().Get("segment"); raw != "" {
				segment, e = strconv.Atoi(raw)
				if e != nil || segment < 0 || segment > 1440 {
					subtitleFailure(w, subtitles.ErrInput)
					return
				}
			}
			reader, offset, err := d.Subtitles.OpenManifestDocument(r.Context(), p, item, session, id, revision)
			if err != nil {
				subtitleFailure(w, err)
				return
			}
			defer reader.Close()
			data, err := subtitles.ManifestWebVTT(reader, offset, segment)
			if err != nil {
				subtitleFailure(w, err)
				return
			}
			if _, _, _, err = d.Playback.ResolveGrantContext(r.Context(), r.PathValue("grant")); err != nil {
				subtitleFailure(w, err)
				return
			}
			if err = d.Subtitles.CheckManifestDocument(r.Context(), p, item, session, id, revision); err != nil {
				subtitleFailure(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("ETag", `"subtitle-vtt-`+id+`-`+strconv.FormatInt(revision, 10)+`-`+strconv.FormatInt(offset, 10)+`-`+strconv.Itoa(segment)+`"`)
			check := func() error {
				if _, _, _, e := d.Playback.ResolveGrantContext(r.Context(), r.PathValue("grant")); e != nil {
					return e
				}
				return d.Subtitles.CheckManifestDocument(r.Context(), p, item, session, id, revision)
			}
			http.ServeContent(w, r, id+".vtt", time.Time{}, &authorizedSubtitleBytes{Reader: bytes.NewReader(data), check: check})
			return
		}
		reader, e := d.Subtitles.OpenDocument(r.Context(), p, item, session, id, revision)
		if e != nil {
			subtitleFailure(w, e)
			return
		}
		defer reader.Close()
		check := func() error {
			_, _, _, e := d.Playback.ResolveGrantContext(r.Context(), r.PathValue("grant"))
			if e != nil {
				return e
			}
			return d.Subtitles.CheckDocument(r.Context(), p, item, session, id, revision)
		}
		serveSubtitleDocument(w, r, id, revision, reader, check)
	}
	if e := registerSubtitleRaw[struct{}, subtitles.Document](registry, "GET /v1/media/subtitles/{grant}/{resource}/{revision}", documentHandler); e != nil {
		return e
	}
	if e := registerSubtitleRaw[struct{}, SubtitleBytesContract](registry, "GET /v1/media/subtitles/{grant}/{resource}/{revision}.vtt", documentHandler); e != nil {
		return e
	}

	for _, path := range []string{"/v1/media/subtitles/{grant}/{resource}/{revision}/original.ass", "/v1/media/subtitles/{grant}/{resource}/{revision}/original.ssa", "/v1/media/subtitles/{grant}/{resource}/{revision}/original.sup", "/v1/media/subtitles/{grant}/{resource}/{revision}/original.idx", "/v1/media/subtitles/{grant}/{resource}/{revision}/original.sub", "/v1/media/subtitles/{grant}/{resource}/{revision}/original.mks", "/v1/media/subtitles/{grant}/{resource}/{revision}/fonts/{font}"} {
		if e := registerSubtitleRaw[struct{}, SubtitleBytesContract](registry, "GET "+path, d.originalSubtitleRoute); e != nil {
			return e
		}
		if e := registerSubtitleRaw[struct{}, SubtitleBytesContract](registry, "HEAD "+path, d.originalSubtitleRoute); e != nil {
			return e
		}
	}
	if e := registerSubtitleRaw[struct{}, subtitles.Lines](registry, "GET /v1/media/subtitles/{grant}/{resource}/{revision}/lines", d.originalSubtitleRoute); e != nil {
		return e
	}
	return registerSubtitleRaw[struct{}, subtitles.FontFiles](registry, "GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts", d.originalSubtitleRoute)

}

// subtitleETagMatches compares an If-None-Match header with this document's
// strong validator. A weak validator never matches: the comparison is strong.
func subtitleETagMatches(header, etag string) bool {
	if len(header) > 1024 {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}

// serveSubtitleDocument answers one authorised document read.
//
// Players reload this document every few seconds purely to extend their
// authorisation. check runs in full first, every time; only for a viewer who is
// still entitled to the document may the body be skipped. A stored revision is
// immutable, so resource and revision are a strong validator of its bytes. The
// response stays uncacheable: the validator travels in the header by hand, so
// nothing can ever be answered from a cache for a revoked viewer.
func serveSubtitleDocument(w http.ResponseWriter, r *http.Request, id string, revision int64, reader io.Reader, check func() error) {
	if e := check(); e != nil {
		subtitleFailure(w, e)
		return
	}
	etag := `"` + id + "." + strconv.FormatInt(revision, 10) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	if match := r.Header.Get("If-None-Match"); match != "" && subtitleETagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == "HEAD" {
		return
	}
	// No range cache or disk pathname escapes the grant boundary. A revocation
	// during an I/O call discards that chunk before it reaches the response.
	buffer := make([]byte, 32<<10)
	for {
		if check() != nil {
			return
		}
		n, readErr := reader.Read(buffer)
		if check() != nil {
			return
		}
		if n > 0 {
			if _, e := w.Write(buffer[:n]); e != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
