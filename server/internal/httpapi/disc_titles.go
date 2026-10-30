package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func registerDiscTitles(r *apikit.Registry, d Dependencies) {
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, ID: "get_disc_titles", Method: "GET", Path: "/v1/items/{id}/disc-titles", Summary: "Read current disc title choices", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Query: []string{"versionId"}, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request"}}, func(ctx context.Context, q *http.Request, _ noBody) (catalog.DiscTitles, error) {
		p, err := d.principal(q)
		if err != nil {
			return catalog.DiscTitles{}, err
		}
		tx, done, err := dbwork.BeginRead(ctx, d.DB)
		if err != nil {
			return catalog.DiscTitles{}, err
		}
		defer done()
		if _, err = d.playbackAuthorityTx(ctx, tx, p, q.PathValue("id")); err != nil {
			return catalog.DiscTitles{}, err
		}
		out, _, err := catalog.DiscTitlesTx(ctx, tx, q.PathValue("id"), q.URL.Query().Get("versionId"))
		return out, err
	})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, ID: "put_disc_main_title", Method: "PUT", Path: "/v1/items/{id}/main-title", Summary: "Choose and analyze the main disc title", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 202, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_mismatch", "source_unavailable"}}, func(ctx context.Context, q *http.Request, in catalog.DiscMainTitleRequest) (catalog.DiscTitles, error) {
		p, err := d.ownerContext(ctx, q)
		if err != nil {
			return catalog.DiscTitles{}, err
		}
		if d.Ingestion == nil {
			return catalog.DiscTitles{}, sql.ErrNoRows
		}
		out, err := d.Ingestion.ChooseDiscMainTitle(ctx, q.PathValue("id"), in, func(tx *sql.Tx) error { return d.ownerAuthorityTx(ctx, tx, p) })
		if errors.Is(err, catalog.ErrDiscSelectionChanged) {
			return catalog.DiscTitles{}, &playbackv1.RevisionError{Current: out.Revision}
		}
		return out, err
	})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, ID: "prepare_disc_title", Method: "POST", Path: "/v1/items/{id}/disc-titles/prepare", Summary: "Analyze an alternate disc title for playback", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 202, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_mismatch", "source_unavailable"}}, func(ctx context.Context, q *http.Request, in catalog.DiscMainTitleRequest) (catalog.DiscTitles, error) {
		p, err := d.principal(q)
		if err != nil {
			return catalog.DiscTitles{}, err
		}
		if d.Ingestion == nil {
			return catalog.DiscTitles{}, sql.ErrNoRows
		}
		out, err := d.Ingestion.PrepareDiscTitle(ctx, q.PathValue("id"), in, func(tx *sql.Tx) error { _, err := d.playbackAuthorityTx(ctx, tx, p, q.PathValue("id")); return err })
		if errors.Is(err, catalog.ErrDiscSelectionChanged) {
			return catalog.DiscTitles{}, &playbackv1.RevisionError{Current: out.Revision}
		}
		return out, err
	})

}
