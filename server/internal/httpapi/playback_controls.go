package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func registerPlaybackControls(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_required", "revision_mismatch", "session_ended", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Integrations, Admission: laneMedia, ID: "get_admin_session_controls", Method: "GET", Path: "/v1/admin/sessions/{id}/controls", Summary: "Read the transport controls and current revision of any administrable session", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "playback", Lane: apikit.Media, Cost: apikit.Constant, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (playbackv1.SessionControls, error) {
		return d.playbackV1().AdminControls(ctx, r.PathValue("id"))
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Integrations, Admission: laneMedia, ID: "control_admin_session", Method: "POST", Path: "/v1/admin/sessions/{id}/controls", Summary: "Pause, resume or seek any administrable session using its current revision", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "playback", Lane: apikit.Media, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: failures}, func(ctx context.Context, r *http.Request, in playbackv1.ControlCommand) (playbackv1.SessionControls, error) {
		return d.playbackV1().AdminControl(ctx, r.PathValue("id"), in)
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Integrations, Admission: laneMedia, ID: "get_remote_playback_controls", Method: "GET", Path: "/v1/playback/controls", Summary: "Read this account’s playback on each device and the controls it may use", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 0, Default: 50, Maximum: 100, EmptyIsDefault: true, Description: "Zero selects the default page size."}}, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (playbackv1.RemoteControlsDocument, error) {
		p, err := d.foundationCaller(r)
		if err != nil {
			return playbackv1.RemoteControlsDocument{}, err
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil {
				return playbackv1.RemoteControlsDocument{}, &apierror.Error{Code: "invalid_request"}
			}
		}
		device := callerFrom(r.Context()).DeviceID
		if device == "" {
			device = p.DeviceID
		}
		c := playbackv1.Caller{Principal: p, DeviceID: device}
		original, err := d.captureOwnControlsRecipient(ctx, c)
		if err != nil {
			return playbackv1.RemoteControlsDocument{}, err
		}
		out, err := d.playbackV1().RemoteControls(ctx, c, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			return out, err
		}
		// A running session is durable authority, but its title may have become
		// hidden since it started. Hide that title and remove its remote controls.
		for i, v := range out.Items {
			if v.Item != nil {
				if e := d.itemAccess(ctx, p, v.Item.ID); e != nil {
					out.Items[i].Item = nil
					out.Items[i].Actions = []string{"stop"}
					out.Items[i].Next = nil
					out.Items[i].DurationMs = 0
					out.Items[i].PositionMs = 0
				}
			}
		}
		if err = d.checkOwnControlsRecipient(r.WithContext(ctx), c, original); err != nil {
			return playbackv1.RemoteControlsDocument{}, err
		}
		return out, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Integrations, Admission: lanePlaybackStop, ID: "stop_account_playback", Method: "POST", Path: "/v1/playback/controls/{id}:stop", Summary: "Stop playback belonging to this account without disclosing a hidden title", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, Status: 204, BodyLimit: 4096, OptionalBody: true, Errors: failures}, func(ctx context.Context, r *http.Request, in playbackv1.AccountStopRequest) (noBody, error) {
		p, err := d.foundationCaller(r)
		if err != nil {
			return noBody{}, err
		}
		err = d.playbackV1().StopAccountSession(ctx, playbackv1.Caller{Principal: p, DeviceID: p.DeviceID}, r.PathValue("id"), in.Generation)
		return noBody{}, err
	})
}
