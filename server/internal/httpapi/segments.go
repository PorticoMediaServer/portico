package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

// Intro and credits detection, for the owner: the server's progress, each
// library's shows and films, one title's review, approving a season's
// detections at once, and measuring a title again. Single markers are
// approved, adjusted, dismissed and added through the title analysis marker
// route, with the targets the review document carries.

// SegmentApproveRequest approves a title's detections of some kinds, in one
// season or (season omitted) all of them.
type SegmentApproveRequest struct {
	Kinds  []string `json:"kinds"`
	Season *int     `json:"season"`
}

// SegmentChangeResult says how many markers or files a change touched.
type SegmentChangeResult struct {
	Changed int `json:"changed"`
}

func (d Dependencies) segmentRoutes(mux *http.ServeMux) {
	handle := func(pattern string, timeout time.Duration, work func(context.Context, *http.Request, identity.Principal) (any, error)) {
		d.httpHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if d.Analysis == nil {
				analysisFailure(w, mediaanalysis.ErrUnsupported)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)
			p, err := d.owner(r)
			if err != nil {
				failure(w, err)
				return
			}
			// Marker edits are the local owner's; detection review is too.
			if p.Authority != "local" || p.Role != "owner" {
				failure(w, identity.ErrForbidden)
				return
			}
			out, err := work(ctx, r, p)
			if err != nil {
				analysisFailure(w, err)
				return
			}
			if _, err = d.owner(r); err != nil {
				failure(w, err)
				return
			}
			write(w, 200, LiveOwnerResult[any]{ProtocolVersion: "1.0", ServerID: d.Identity.ID(), Result: out})
		})
	}
	noQuery := func(r *http.Request) error {
		if r.URL.RawQuery != "" {
			return mediaanalysis.ErrInput
		}
		return nil
	}
	handle("GET /v1/admin/segments", 15*time.Second, func(ctx context.Context, r *http.Request, _ identity.Principal) (any, error) {
		if err := noQuery(r); err != nil {
			return nil, err
		}
		return d.Analysis.SegmentStatus(ctx, nil)
	})
	handle("GET /v1/admin/segments/titles", 20*time.Second, func(ctx context.Context, r *http.Request, _ identity.Principal) (any, error) {
		q := r.URL.Query()
		for k, v := range q {
			if len(v) != 1 || len(v[0]) > 4096 || k != "libraryId" && k != "cursor" && k != "limit" {
				return nil, mediaanalysis.ErrInput
			}
		}
		limit := 50
		if q.Has("limit") {
			n, err := strconv.Atoi(q.Get("limit"))
			if err != nil || n < 1 || n > 100 {
				return nil, mediaanalysis.ErrInput
			}
			limit = n
		}
		if q.Get("libraryId") == "" {
			return nil, mediaanalysis.ErrInput
		}
		return d.Analysis.SegmentTitles(ctx, nil, q.Get("libraryId"), q.Get("cursor"), limit)
	})
	handle("GET /v1/admin/segments/titles/{id}", 20*time.Second, func(ctx context.Context, r *http.Request, _ identity.Principal) (any, error) {
		if err := noQuery(r); err != nil {
			return nil, err
		}
		return d.Analysis.SegmentTitle(ctx, nil, r.PathValue("id"))
	})
	handle("POST /v1/admin/segments/titles/{id}/approve", 30*time.Second, func(ctx context.Context, r *http.Request, p identity.Principal) (any, error) {
		if err := noQuery(r); err != nil {
			return nil, err
		}
		var body SegmentApproveRequest
		if err := lyricDecode(nil, r, &body); err != nil {
			return nil, mediaanalysis.ErrInput
		}
		season := -2
		if body.Season != nil {
			if *body.Season < -1 || *body.Season > 9999 {
				return nil, mediaanalysis.ErrInput
			}
			season = *body.Season
		}
		n, err := d.Analysis.SegmentApprove(ctx, nil, mediaanalysis.Actor(p.Authority, p.AccountID, p.ProfileID), r.PathValue("id"), season, body.Kinds)
		return SegmentChangeResult{n}, err
	})
	handle("POST /v1/admin/segments/titles/{id}/redetect", 30*time.Second, func(ctx context.Context, r *http.Request, _ identity.Principal) (any, error) {
		if err := noQuery(r); err != nil {
			return nil, err
		}
		n, err := d.Analysis.SegmentRedetect(ctx, nil, r.PathValue("id"))
		return SegmentChangeResult{n}, err
	})
}
