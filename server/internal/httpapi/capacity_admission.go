package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type capacityAdmissionKey struct{}
type capacityAdmission struct{ original identity.ControlsRecipient }

func controlsAdmissionRequest(r *http.Request) *http.Request {
	request := r.Clone(dbwork.WithClass(r.Context(), dbwork.ClassSecurityFence))
	address := *r.URL
	address.Path = "/v1/playback/controls"
	address.RawPath = ""
	address.RawQuery = ""
	request.URL = &address
	request.Method = http.MethodGet
	return request
}
func (d Dependencies) controlsRouteAdmission(r *http.Request) error {
	request := controlsAdmissionRequest(r)
	if err := registryScopeGate(request, apikit.ScopePlayback); err != nil {
		return err
	}
	_, err := d.v1Authorize(request.Context(), request, apikit.Metadata{Audience: apikit.Integrations, ID: "get_remote_playback_controls", Method: http.MethodGet, Path: "/v1/playback/controls", Access: apikit.Device, Requires: apikit.ScopePlayback})
	return err
}

// Attach only from actual existing controls admission. An ineligible reader
// keeps its ordinary events; no account-wide capacity audience is added.
func (d Dependencies) withCapacityAdmission(r *http.Request, p identity.Principal) (*http.Request, error) {
	if d.Identity == nil || d.DB == nil || !identity.CanUsePersonalState(p) {
		return r, nil
	}
	if err := d.controlsRouteAdmission(r); err != nil {
		return r, nil
	}
	var original identity.ControlsRecipient
	err := dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		var e error
		original, e = d.Identity.CaptureControlsRecipientTx(ctx, dbwork.Snapshot(ctx), p)
		return e
	})
	if err != nil {
		if _, _, refused := identity.Refusal(err); refused {
			return r, nil
		}
		return nil, err
	}
	ctx := context.WithValue(r.Context(), capacityAdmissionKey{}, capacityAdmission{original})
	ctx = eventfeed.WithCapacityRecipient(ctx, func(ctx context.Context, tx *sql.Tx, live identity.Principal) error {
		return d.Identity.CheckControlsRecipientTx(ctx, tx, original, live)
	})
	return r.WithContext(ctx), nil
}

// Recheck route eligibility and the original private family before a queued
// page or individual capacity event is delivered. Reconnect captures anew.
func (d Dependencies) checkCapacityReader(r *http.Request, p identity.Principal) error {
	binding, ok := r.Context().Value(capacityAdmissionKey{}).(capacityAdmission)
	if !ok {
		return nil
	}
	if err := d.controlsRouteAdmission(r); err != nil {
		return err
	}
	return dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		return d.Identity.CheckControlsRecipientTx(ctx, dbwork.Snapshot(ctx), binding.original, p)
	})
}
