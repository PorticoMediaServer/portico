package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

func (d Dependencies) analysisAccess(r *http.Request) (mediaanalysis.Access, error) {
	p, library, fence, e := d.detailAccess(r)
	if e != nil {
		return mediaanalysis.Access{}, e
	}
	return mediaanalysis.Access{ServerID: d.Identity.ID(), LibraryID: library, ItemID: r.PathValue("id"), ViewerFence: fence, Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, SessionHash: p.Hash, Owner: p.Authority == "local" && p.Role == "owner", Authorize: func(tx *sql.Tx) error {
		if p.Authority == "local" && p.Role == "owner" {
			if e := d.manualMetadataAuthorize(r.Context(), tx, p); e != nil {
				return e
			}
		}
		return d.resourceAuthorize(tx, p, library)
	}}, nil
}
func analysisFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	status, code := 0, ""
	switch {
	case errors.Is(e, mediaanalysis.ErrClock):
		status, code = 422, "analysis_clock_unavailable"
	case errors.Is(e, mediaanalysis.ErrConflict):
		status, code = 409, "analysis_conflict"
	case errors.Is(e, mediaanalysis.ErrInput), errors.Is(e, lyrics.ErrInput):
		// The analysis routes read their bodies with the lyrics routes' strict decoder.
		status, code = 400, "invalid_analysis"
	case errors.Is(e, mediaanalysis.ErrBudget):
		status, code = 422, "analysis_budget_exceeded"
	case errors.Is(e, mediaanalysis.ErrUnsupported):
		status, code = 503, "analysis_unavailable"
	}
	if status == 0 {
		failure(w, e)
		return
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": publicErrorMessage(code), "retryable": status == 503}})
}
func analysisQuery(r *http.Request) (mediaanalysis.Target, string, error) {
	q := r.URL.Query()
	for k, v := range q {
		if len(v) != 1 || len(v[0]) > 256 {
			return mediaanalysis.Target{}, "", mediaanalysis.ErrInput
		}
		switch k {
		case "sourceId", "sourceRevision", "mappingRevision", "sessionId", "generation", "positionUS":
		default:
			return mediaanalysis.Target{}, "", mediaanalysis.ErrInput
		}
	}
	t := mediaanalysis.Target{SourceID: q.Get("sourceId"), SourceRevision: q.Get("sourceRevision"), MappingRevision: q.Get("mappingRevision"), SessionID: q.Get("sessionId")}
	if q.Has("generation") {
		n, e := strconv.ParseInt(q.Get("generation"), 10, 64)
		if e != nil || n < 0 {
			return t, "", mediaanalysis.ErrInput
		}
		t.Generation = n
	}
	return t, q.Get("positionUS"), nil
}

// analysisV1 maps a v1 session id and presentation generation in an analysis
// query to the media session presenting it (NEW-35; playback_v1_presentation.go),
// read-only. The generation the client holds is the v1 one: it must be the
// presentation's current generation (else a conflict, as for a media session),
// and the media session's own generation is what the store then fences on.
func (d Dependencies) analysisV1(ctx context.Context, a *mediaanalysis.Access, t *mediaanalysis.Target) (v1Presentation, bool, error) {
	if t.SessionID == "" {
		return v1Presentation{}, false, nil
	}
	p := identity.Principal{Viewer: identity.Viewer{Authority: a.Authority, AccountID: a.AccountID, ProfileID: a.ProfileID}, Hash: a.SessionHash}
	presentation, v1, e := d.resolveV1Presentation(ctx, p, t.SessionID)
	if e != nil || !v1 {
		return presentation, false, e
	}
	if t.Generation != int64(presentation.generation) {
		return presentation, false, mediaanalysis.ErrConflict
	}
	media, e := d.mediaGeneration(ctx, presentation.media)
	if e != nil {
		return presentation, false, e
	}
	t.SessionID, t.Generation = presentation.media, int64(media)
	a.SessionHash = presentation.as(p).Hash
	return presentation, true, nil
}
func (d Dependencies) analysisRoutes(mux *http.ServeMux) {
	rate := &personalLimiter{}
	for _, route := range []struct{ pattern, action string }{
		{"GET /v1/items/{id}/analysis", "view"},
		{"GET /v1/items/{id}/analysis/artifacts/{artifact}", "read"},
		{"POST /v1/items/{id}/analysis/jobs", "queue"},
		{"POST /v1/items/{id}/analysis/jobs/{job}/control", "control"},
		{"POST /v1/items/{id}/analysis/markers", "markers"},
		{"PUT /v1/items/{id}/analysis/policy", "policy"},
	} {
		d.httpHandle(mux, route.pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if d.Analysis == nil {
				analysisFailure(w, mediaanalysis.ErrUnsupported)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			a, e := d.analysisAccess(r)
			if e != nil {
				analysisFailure(w, e)
				return
			}
			if r.Method != "GET" {
				if !a.Owner {
					failure(w, identity.ErrForbidden)
					return
				}
				if r.URL.RawQuery != "" {
					analysisFailure(w, mediaanalysis.ErrInput)
					return
				}
				if !rate.allow(a.Authority + ":" + a.AccountID + ":" + a.ProfileID) {
					w.Header().Set("Retry-After", "60")
					write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many analysis changes. Retry shortly.", "retryable": true}})
					return
				}
			}
			var out any
			status := 200
			switch route.action {
			case "view", "read":
				var t mediaanalysis.Target
				var position string
				t, position, e = analysisQuery(r)
				var presentation v1Presentation
				var v1 bool
				if e == nil {
					presentation, v1, e = d.analysisV1(ctx, &a, &t)
				}
				if e == nil {
					if route.action == "view" {
						var view mediaanalysis.View
						view, e = d.Analysis.View(ctx, a, t, position)
						if v1 {
							view.Scope.SessionID, view.Scope.SessionGeneration = presentation.session, int64(presentation.generation)
						}
						out = view
					} else {
						var artifact mediaanalysis.Artifact
						artifact, e = d.Analysis.Read(ctx, a, t, r.PathValue("artifact"))
						if v1 {
							artifact.Scope.SessionID, artifact.Scope.SessionGeneration = presentation.session, int64(presentation.generation)
						}
						out = artifact
					}
				}
			case "markers":
				var m mediaanalysis.Mutation
				if e = lyricDecode(w, r, &m); e == nil {
					out, e = d.Analysis.Mutate(ctx, a, m)
				}
			case "policy":
				var m struct {
					mediaanalysis.Target
					Tier             string                    `json:"tier"`
					Operations       []string                  `json:"operations"`
					Trickplay        catalog.TrickplaySettings `json:"trickplay"`
					ExpectedRevision int64                     `json:"expectedRevision"`
				}
				if e = lyricDecode(w, r, &m); e == nil {
					var release func()
					a, release, e = d.Analysis.Capture(ctx, a, m.Target)
					if e != nil {
						break
					}
					defer release()
					_, e = d.Analysis.ResolveOwner(ctx, a, m.Target)
					if e == nil {
						out, e = d.Catalog.UpdateScanPolicy(ctx, a.LibraryID, catalog.ScanPolicy{Tier: m.Tier, Operations: m.Operations, Trickplay: m.Trickplay}, m.ExpectedRevision, func(tx *sql.Tx) error { return d.Analysis.AuthorizeTarget(ctx, tx, a, m.Target) })
					}
					if e == nil && d.Ingestion != nil {
						e = d.Ingestion.PolicyChanged(ctx, a.LibraryID)
					}
				}
			case "queue", "control":
				var m struct {
					mediaanalysis.Target
					Action string `json:"action"`
				}
				if e = lyricDecode(w, r, &m); e == nil {
					var release func()
					a, release, e = d.Analysis.Capture(ctx, a, m.Target)
					if e != nil {
						break
					}
					defer release()
					var v mediaanalysis.Source
					v, e = d.Analysis.ResolveOwner(ctx, a, m.Target)
					if e == nil && d.Ingestion == nil {
						e = mediaanalysis.ErrUnsupported
					}
					authorize := func(tx *sql.Tx) error {
						if e := d.Analysis.AuthorizeTarget(ctx, tx, a, m.Target); e != nil {
							return e
						}
						if route.action == "control" {
							var valid bool
							if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id=? AND r.source_id=? AND j.library_id=?)`, r.PathValue("job"), v.RootID, a.LibraryID).Scan(&valid); e != nil {
								return e
							}
							if !valid {
								return identity.ErrNotVisible
							}
						}
						if route.action == "queue" || m.Action == "retry" {
							return d.Analysis.RequestProbeRefresh(ctx, tx, a, m.Target)
						}
						return nil
					}
					if e == nil {
						if route.action == "queue" {
							out, e = d.Ingestion.QueueItemAnalysis(ctx, a.LibraryID, v.ID, v.Revision, authorize)
							status = 202
						} else {
							switch m.Action {
							case "pause", "resume", "cancel":
								e = d.Ingestion.Control(ctx, r.PathValue("job"), m.Action, authorize)
								out = map[string]string{"status": m.Action}
							case "retry":
								out, e = d.Ingestion.QueueItemAnalysis(ctx, a.LibraryID, v.ID, v.Revision, authorize)
								status = 202
							default:
								e = mediaanalysis.ErrInput
							}
						}
					}
				}
			}
			if e != nil {
				analysisFailure(w, e)
				return
			}
			after, e := d.analysisAccess(r)
			if e != nil {
				analysisFailure(w, e)
				return
			}
			if a.ViewerFence != after.ViewerFence || a.LibraryID != after.LibraryID {
				failure(w, identity.ErrNotVisible)
				return
			}
			write(w, status, out)
		})
	}
}
