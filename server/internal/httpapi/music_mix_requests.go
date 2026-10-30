package httpapi

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// The shared DTO projects these exact current-profile cards. Both Home and
// Music Discover consume that one row; neither supplies a profile identifier.
func (d Dependencies) musicMixCardsRequest(r *http.Request) ([]catalog.MusicMixCard, projectedListeningScope, error) {
	var scope projectedListeningScope
	p, libraries, fence, err := d.homeScope(r)
	if err != nil {
		return nil, scope, err
	}
	viewer, err := d.catalogViewer(r, p, libraries, fence)
	if err != nil {
		return nil, scope, err
	}
	cards, err := d.Catalog.WithContext(r.Context()).MusicMixRow(viewer, time.Now())
	if err != nil {
		return nil, scope, err
	}
	after, err := d.homeFenceAfter(r, p)
	if err != nil {
		return nil, scope, err
	}
	if after != fence {
		return nil, scope, catalog.ErrStaleContinuation
	}
	scope = projectedListeningScope{ServerID: d.Identity.ID(), Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}
	return cards, scope, nil
}

type musicMixSaveChange struct {
	OperationID string `json:"operationId"`
	// This is the canonical formatter's localized display name, not a raw
	// model tag or an English sentence assembled independently by the server.
	Name string `json:"name"`
}

func (d Dependencies) saveMusicMixRequest(w http.ResponseWriter, r *http.Request) (catalog.PlaylistReceipt, error) {
	var out catalog.PlaylistReceipt
	p, _, _, err := d.homeScope(r)
	if err != nil {
		return out, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var change musicMixSaveChange
	if err = decode(w, r, &change); err != nil {
		return out, err
	}
	out, err = d.Catalog.WithContext(r.Context()).SaveMusicMix(r.Context(), resourceActor(p), r.PathValue("id"), change.OperationID, change.Name, time.Now(), func(tx *sql.Tx) (catalog.Viewer, error) {
		return d.queueCatalogViewerTx(r.Context(), tx, p)
	}, func(tx *sql.Tx) error {
		return d.resourceAuthorize(tx, p, "")
	})
	if err != nil {
		return out, err
	}
	_, _, fence, err := d.homeScope(r)
	if err != nil {
		return out, err
	}
	out.ServerID, out.ViewerFence = d.Identity.ID(), fence
	return out, nil
}
