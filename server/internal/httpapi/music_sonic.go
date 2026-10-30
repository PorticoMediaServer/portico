package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// These typed producers are registered only with their generated contracts.
// They do not add an unregistered route or widen the existing similar payload.
type SonicSettingsChange struct {
	Enabled          bool  `json:"enabled"`
	ExpectedRevision int64 `json:"expectedRevision"`
}

type SonicSimilarView struct {
	catalog.ListeningSimilar
	BySound bool `json:"bySound"`
}

func (d Dependencies) sonicStatusRequest(r *http.Request) (mediaanalysis.SonicStatusView, error) {
	var out mediaanalysis.SonicStatusView
	p, err := d.owner(r)
	if err != nil {
		return out, err
	}
	if d.Analysis == nil {
		return out, mediaanalysis.ErrUnsupported
	}
	library := r.PathValue("id")
	status, err := d.Analysis.SonicStatus(r.Context(), library, time.Now(), func(tx *sql.Tx) error { return d.ownerAuthorityTx(r.Context(), tx, p) })
	if err != nil {
		return out, err
	}
	return status.OwnerView(library), nil
}

func (d Dependencies) sonicSettingsRequest(r *http.Request, change SonicSettingsChange) (mediaanalysis.SonicStatusView, error) {
	var out mediaanalysis.SonicStatusView
	p, err := d.owner(r)
	if err != nil {
		return out, err
	}
	if d.Analysis == nil || d.Catalog == nil {
		return out, mediaanalysis.ErrUnsupported
	}
	if change.ExpectedRevision < 1 {
		return out, errStrictJSON
	}
	library := r.PathValue("id")
	_, err = d.Catalog.UpdateSonicSettings(r.Context(), library, change.Enabled, change.ExpectedRevision, func(tx *sql.Tx) error { return d.ownerAuthorityTx(r.Context(), tx, p) })
	if err != nil {
		return out, err
	}
	if d.Ingestion != nil {
		if err = d.Ingestion.PolicyChanged(r.Context(), library); err != nil {
			return out, err
		}
	}
	return d.sonicStatusRequest(r)
}

func (d Dependencies) sonicRetryRequest(r *http.Request) (mediaanalysis.SonicStatusView, error) {
	var out mediaanalysis.SonicStatusView
	p, err := d.owner(r)
	if err != nil {
		return out, err
	}
	if d.Analysis == nil {
		return out, mediaanalysis.ErrUnsupported
	}
	err = d.Analysis.RetrySonic(r.Context(), r.PathValue("id"), func(tx *sql.Tx) error { return d.ownerAuthorityTx(r.Context(), tx, p) })
	if err != nil && !errors.Is(err, sonic.ErrUnavailable) {
		return out, err
	}
	// A completed retry can truthfully remain unavailable. The response is the
	// coded owner status, never the loader's filesystem diagnostic.
	return d.sonicStatusRequest(r)
}

func (d Dependencies) sonicSimilarRequest(r *http.Request) (projectedViewerEnvelope[SonicSimilarView], error) {
	var out projectedViewerEnvelope[SonicSimilarView]
	p, libraries, fence, err := d.homeScope(r)
	if err != nil {
		return out, err
	}
	viewer, err := d.catalogViewer(r, p, libraries, fence)
	if err != nil {
		return out, err
	}
	q := r.URL.Query()
	if q.Get("by") != "" && q.Get("by") != "sound" {
		return out, errStrictJSON
	}
	limit, err := strconv.Atoi(q.Get("limit"))
	if q.Get("limit") != "" && (err != nil || limit < 1 || limit > 100) {
		return out, errStrictJSON
	}
	data, err := d.Catalog.WithContext(r.Context()).ListeningSimilarPage(viewer, r.PathValue("id"), q.Get("by") == "sound", q.Get("cursor"), limit)
	if err != nil {
		return out, err
	}
	out.Data = SonicSimilarView{ListeningSimilar: data.Items, BySound: data.BySound}
	after, err := d.homeFenceAfter(r, p)
	if err != nil {
		return out, err
	}
	if after != fence {
		return out, catalog.ErrStaleContinuation
	}
	out.Scope = projectedListeningScope{ServerID: d.Identity.ID(), Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}
	return out, nil
}
