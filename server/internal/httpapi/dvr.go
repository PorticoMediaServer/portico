package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func dvrFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	if revisionFailure(w, e) {
		return
	}
	if errors.Is(e, errFeatureRestricted) {
		failure(w, e)
		return
	}

	status, code, message := 503, "dvr_unavailable", "Recordings are temporarily unavailable. Try again."
	switch {
	// CD-51: a refused but valid session is 403 (or a hidden 404), never 401.
	case errors.Is(e, identity.ErrNotVisible):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, identity.ErrForbidden):
		status, code, message = 403, "forbidden", identity.ErrForbidden.Error()
	case errors.Is(e, identity.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Authentication is required."
	case errors.Is(e, dvr.ErrDenied), errors.Is(e, livechannels.ErrDenied):
		status, code, message = 403, "dvr_denied", "This recording is not available to this profile."
	case errors.Is(e, dvr.ErrConflict), errors.Is(e, livechannels.ErrConflict), errors.Is(e, livechannels.ErrCursor):
		status, code, message = 409, "dvr_conflict", "The schedule or recording changed. Refresh before applying this action."
	case errors.Is(e, dvr.ErrInvalid), errors.Is(e, livechannels.ErrInvalid):
		status, code, message = 400, "dvr_invalid", "Check the recording options and try again."
	case errors.Is(e, dvr.ErrUnsupportedPredicate):
		status, code, message = 422, "dvr_predicate_unavailable", dvr.ErrUnsupportedPredicate.Error()
	case errors.Is(e, dvr.ErrDeletionUnavailable), errors.Is(e, dvr.ErrStoragePolicyUnavailable):
		status, code, message = 422, "dvr_storage_unsupported", "Physical recording cleanup is unavailable on this server platform."
	case errors.Is(e, dvr.ErrCaptureUnavailable):
		status, code, message = 422, "dvr_capture_unavailable", "Configure a supported recording root and confined FFmpeg/ffprobe on this server."
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == 503}})
}

func (d Dependencies) dvrRoutes(mux *http.ServeMux) {
	wrap := func(owner bool, fn func(context.Context, identity.Principal, *http.Request) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			p, e := d.channelPrincipal(ctx, r, owner)
			if e != nil {
				dvrFailure(w, e)
				return
			}
			if d.DVR == nil {
				dvrFailure(w, dvr.ErrUnavailable)
				return
			}
			_, before, e := d.viewerRestrictions(r, p)
			if e != nil {
				dvrFailure(w, e)
				return
			}
			result, e := fn(ctx, p, r)
			if e != nil {
				dvrFailure(w, e)
				return
			}
			current, e := d.channelPrincipal(ctx, r, owner)
			if e != nil || current != p {
				dvrFailure(w, identity.ErrUnauthorized)
				return
			}
			_, after, e := d.viewerRestrictions(r, p)
			if e != nil {
				dvrFailure(w, e)
				return
			}
			if before != after {
				dvrFailure(w, livechannels.ErrCursor)
				return
			}
			write(w, 200, map[string]any{"protocolVersion": dvr.Version, "serverId": d.Identity.ID(), "result": result})
		}
	}
	d.httpHandle(mux, "GET /v1/dvr/summary", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, dvr.ErrInvalid
		}
		if e := d.featureAllowed(ctx, p, "live_tv"); e != nil {
			return nil, e
		}
		return d.DVR.Summary(ctx, d.dvrAuthority(p), channelSubject(p))
	}))
	d.httpHandle(mux, "GET /v1/dvr/groups", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		if e := d.featureAllowed(ctx, p, "live_tv"); e != nil {
			return nil, e
		}
		q := r.URL.Query()
		for k, v := range q {
			if len(v) != 1 || k != "sort" && k != "cursor" && k != "limit" {
				return nil, dvr.ErrInvalid
			}
		}
		limit := 30
		if q.Has("limit") {
			n, e := strconv.Atoi(q.Get("limit"))
			if e != nil {
				return nil, dvr.ErrInvalid
			}
			limit = n
		}
		return d.DVR.Groups(ctx, d.dvrAuthority(p), channelSubject(p), q.Get("sort"), q.Get("cursor"), limit)
	}))
	d.httpHandle(mux, "GET /v1/dvr/recordings/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		p, e := d.channelPrincipal(r.Context(), r, false)
		if e != nil {
			dvrFailure(w, e)
			return
		}
		if d.DVR == nil || d.Administration == nil {
			dvrFailure(w, dvr.ErrUnavailable)
			return
		}
		small := artworkSize(r) == metadata.ArtworkSmall
		digest, mime, e := d.DVR.ImageRendition(r.Context(), d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), small)
		if e != nil {
			dvrFailure(w, e)
			return
		}
		raw, e := d.Administration.GuideImageBytes(r.Context(), digest, mime)
		if e != nil {
			dvrFailure(w, e)
			return
		}
		current, e := d.channelPrincipal(r.Context(), r, false)
		if e != nil || current != p {
			dvrFailure(w, dvr.ErrDenied)
			return
		}
		again, _, e := d.DVR.ImageRendition(r.Context(), d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), small)
		if e != nil || again != digest {
			dvrFailure(w, dvr.ErrDenied)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})

	d.httpHandle(mux, "GET /v1/dvr", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		q := r.URL.Query()
		for k, v := range q {
			if (k != "state" && k != "cursor" && k != "limit" && k != "sort" && k != "group") || len(v) != 1 {
				return nil, dvr.ErrInvalid
			}
		}
		state := q.Get("state")
		if state == "" {
			state = "upcoming"
		}
		limit := 50
		if q.Get("limit") != "" {
			var e error
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil {
				return nil, dvr.ErrInvalid
			}
		}
		return d.DVR.List(ctx, d.dvrAuthority(p), channelSubject(p), dvr.Query{State: state, Cursor: q.Get("cursor"), Limit: limit, Sort: q.Get("sort"), Group: q.Get("group")})
	}))
	d.httpHandle(mux, "GET /v1/dvr/recordings/{id}", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		for k, v := range r.URL.Query() {
			if k != "after" || len(v) != 1 {
				return nil, dvr.ErrInvalid
			}
		}
		return d.DVR.Detail(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), r.URL.Query().Get("after"))
	}))
	d.httpHandle(mux, "GET /v1/dvr/channels", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		for k, v := range r.URL.Query() {
			if (k != "sourceId" && k != "search" && k != "cursor") || len(v) != 1 {
				return nil, dvr.ErrInvalid
			}
		}
		q := r.URL.Query()
		return d.DVR.ChannelChoices(ctx, d.dvrAuthority(p), channelSubject(p), q.Get("sourceId"), q.Get("search"), q.Get("cursor"))
	}))
	d.httpHandle(mux, "POST /v1/dvr/recordings", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var body struct {
			RequestID   string         `json:"requestId"`
			Occurrence  dvr.Occurrence `json:"occurrence"`
			Options     *dvr.Options   `json:"options"`
			UseDefaults bool           `json:"useDefaults"`
		}
		if e := liveBody(r, &body); e != nil {
			return nil, e
		}
		in := dvr.ScheduleInput{RequestID: body.RequestID, Occurrence: body.Occurrence, UseDefaults: body.UseDefaults || body.Options == nil}
		if body.Options != nil {
			in.Options = *body.Options
		}
		return d.DVR.Schedule(ctx, d.dvrAuthority(p), channelSubject(p), in)
	}))
	d.httpHandle(mux, "PATCH /v1/dvr/recordings/{id}", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.UpdateInput
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		return d.DVR.Update(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), in)
	}))
	d.httpHandle(mux, "POST /v1/dvr/recordings/{id}/cancel", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.Mutation
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		return d.DVR.Cancel(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), in)
	}))
	d.httpHandle(mux, "PUT /v1/dvr/recordings/{id}/keep", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.KeepInput
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		return d.DVR.SetKeep(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), in)
	}))
	d.httpHandle(mux, "GET /v1/dvr/recordings/{id}/delete-preview", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		return d.DVR.PreviewDelete(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"))
	}))
	d.httpHandle(mux, "DELETE /v1/dvr/recordings/{id}", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.Mutation
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		return d.DVR.Delete(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), in)
	}))
	d.httpHandle(mux, "POST /v1/dvr/rules/preview", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.RuleConfig
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		return d.DVR.PreviewRule(ctx, d.dvrAuthority(p), channelSubject(p), in)
	}))
	d.httpHandle(mux, "PUT /v1/dvr/rules/{id}", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.RuleInput
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		if in.ID != r.PathValue("id") {
			return nil, dvr.ErrInvalid
		}
		return d.DVR.SaveRule(ctx, d.dvrAuthority(p), channelSubject(p), in)
	}))
	d.httpHandle(mux, "DELETE /v1/dvr/rules/{id}", wrap(false, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.DeleteRuleInput
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		e := d.DVR.DeleteRule(ctx, d.dvrAuthority(p), channelSubject(p), r.PathValue("id"), in)
		return map[string]bool{"deleted": e == nil}, e
	}))
	d.httpHandle(mux, "GET /v1/dvr/storage", wrap(true, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		return d.DVR.StorageStatus(ctx, d.dvrAuthority(p))
	}))
	d.httpHandle(mux, "PUT /v1/dvr/storage", wrap(true, func(ctx context.Context, p identity.Principal, r *http.Request) (any, error) {
		var in dvr.StoragePolicyInput
		if e := liveBody(r, &in); e != nil {
			return nil, e
		}
		return d.DVR.SetStoragePolicy(ctx, d.dvrAuthority(p), channelSubject(p), in)
	}))
}

func liveBody(r *http.Request, value any) error {
	if r.Body == nil {
		return dvr.ErrInvalid
	}
	reader := io.LimitReader(r.Body, (384<<10)+1)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()
	var raw json.RawMessage
	if e := dec.Decode(&raw); e != nil {
		return dvr.ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return dvr.ErrInvalid
	}
	if strings.HasPrefix(r.URL.Path, "/v1/dvr") {
		if err := requireRevisionJSON(raw, value); err != nil {
			return err
		}
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(value); err != nil {
		return dvr.ErrInvalid
	}
	return nil
}

func (d Dependencies) dvrAuthority(p identity.Principal) livechannels.Authority {
	return func(ctx context.Context, tx *sql.Tx, owner bool) (string, func(string, string) bool, error) {
		fence, allowed, e := d.liveAuthority(p)(ctx, tx, owner)
		if e != nil {
			return "", nil, e
		}
		policy, e := identity.RestrictionsForViewerTx(ctx, tx, p.Viewer)
		if e != nil {
			return "", nil, e
		}
		if !policy.AllowDVR {
			return "", nil, livechannels.ErrDenied
		}
		content, e := access.ContentLimitsTx(ctx, tx, p.AccountID, policy.Content())
		if e != nil {
			return "", nil, e
		}
		return fence + ":" + catalog.RestrictionFence(content), allowed, nil
	}
}
