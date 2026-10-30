package httpapi

import (
	"context"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

type LiveSourcesDocument struct {
	ProtocolVersion string                      `json:"protocolVersion"`
	ServerID        string                      `json:"serverId"`
	Sources         []livechannels.Source       `json:"sources"`
	Refresh         []livechannels.RemoteStatus `json:"refresh"`
}
type LivePreviewDocument struct {
	ProtocolVersion string               `json:"protocolVersion"`
	ServerID        string               `json:"serverId"`
	Preview         livechannels.Preview `json:"preview"`
}
type LiveRemotePreviewDocument struct {
	ProtocolVersion string                     `json:"protocolVersion"`
	ServerID        string                     `json:"serverId"`
	RemotePreview   livechannels.RemotePreview `json:"remotePreview"`
}
type LiveSourceDocument struct {
	ProtocolVersion string              `json:"protocolVersion"`
	ServerID        string              `json:"serverId"`
	Source          livechannels.Source `json:"source"`
}
type LiveRemoteSave struct {
	PreviewID string `json:"previewId"`
	RequestID string `json:"requestId"`
}
type LiveOwnerResult[T any] struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerID        string `json:"serverId"`
	Result          T      `json:"result"`
}

func liveContractError(err error) error {
	if err == nil {
		return nil
	}
	var lan *livechannels.LANConfirmationRequired
	if errors.As(err, &lan) {
		return &apierror.Error{Code: "source_lan_confirmation_required", Current: struct {
			ConfirmRoots []string `json:"confirmRoots"`
		}{lan.Roots}, Cause: err}
	}
	code := "channel_unavailable"
	switch {
	case errors.Is(err, livechannels.ErrInvalid), errors.Is(err, administration.ErrInput):
		code = "invalid_channel_input"
	case errors.Is(err, livechannels.ErrConflict), errors.Is(err, administration.ErrConflict):
		code = "channel_revision_conflict"
	case errors.Is(err, livechannels.ErrDenied), errors.Is(err, administration.ErrDenied), errors.Is(err, identity.ErrForbidden):
		code = "not_permitted"
	case errors.Is(err, identity.ErrUnauthorized):
		code = "unauthorized"
	case errors.Is(err, livechannels.ErrNetworkPolicy):
		code = "source_network_policy"
	case errors.Is(err, livechannels.ErrSourceAuthentication):
		code = "source_credentials_required"
	case errors.Is(err, livechannels.ErrSourceUnavailable):
		code = "source_unavailable"
	case errors.Is(err, administration.ErrNotFound):
		code = "not_found"
	}
	return &apierror.Error{Code: code, Cause: err}
}
func liveContract[Req, Resp any](r *apikit.Registry, d Dependencies, method, path, id string, limit int64, h func(context.Context, *http.Request, identity.Principal, Req) (Resp, error)) {
	admission := laneAdmin
	if method == "POST" && (path == "/v1/admin/live-sources" || path == "/v1/admin/live-sources/preview") {
		admission = laneUpload
	}

	m := apikit.Metadata{Audience: apikit.Clients, Admission: admission, ID: id, Method: method, Path: path, Summary: "Manage live source acquisition and guide settings", Access: apikit.Admin, AdminScope: "playback", Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: limit, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "invalid_channel_input", "channel_revision_conflict", "channel_unavailable", "source_lan_confirmation_required", "source_network_policy", "source_credentials_required", "source_unavailable", "internal"}}
	if e := apikit.Register(r, apikit.Route[Req, Resp]{Metadata: m, Handler: func(ctx context.Context, q *http.Request, in Req) (Resp, error) {
		var out Resp
		p, e := d.ownerContext(ctx, q)
		if e != nil {
			return out, liveContractError(e)
		}
		out, e = h(ctx, q, p, in)
		if e != nil {
			return out, liveContractError(e)
		}
		_, e = d.ownerContext(ctx, q)
		return out, liveContractError(e)
	}}); e != nil {
		panic(e)
	}
}
func registerLiveContracts(r *apikit.Registry, d Dependencies) {
	liveContract(r, d, "GET", "/v1/admin/live-sources", "list_live_sources", 0, func(ctx context.Context, q *http.Request, p identity.Principal, _ struct{}) (LiveSourcesDocument, error) {
		out := LiveSourcesDocument{ProtocolVersion: "1.0", ServerID: d.Identity.ID()}
		if d.LiveChannels == nil {
			return out, livechannels.ErrUnavailable
		}
		var e error
		out.Sources, e = d.LiveChannels.Sources(ctx, d.liveAuthority(p))
		if e == nil {
			out.Refresh, e = d.LiveChannels.RemoteStatuses(ctx, d.liveAuthority(p))
		}
		return out, e
	})
	liveContract(r, d, "POST", "/v1/admin/live-sources/preview", "preview_live_source", 48<<20, func(ctx context.Context, q *http.Request, p identity.Principal, in livechannels.SourceInput) (LivePreviewDocument, error) {
		if d.LiveChannels == nil {
			return LivePreviewDocument{}, livechannels.ErrUnavailable
		}
		value, e := d.LiveChannels.Preview(ctx, d.liveAuthority(p), in)
		return LivePreviewDocument{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "POST", "/v1/admin/live-sources", "save_live_source", 48<<20, func(ctx context.Context, q *http.Request, p identity.Principal, in livechannels.SourceInput) (LiveSourceDocument, error) {
		if d.LiveChannels == nil {
			return LiveSourceDocument{}, livechannels.ErrUnavailable
		}
		value, e := d.LiveChannels.Save(ctx, d.liveAuthority(p), in)
		return LiveSourceDocument{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "POST", "/v1/admin/live-sources/remote/preview", "preview_remote_live_source", 8<<20, func(ctx context.Context, q *http.Request, p identity.Principal, in livechannels.RemoteDraft) (LiveRemotePreviewDocument, error) {
		if d.LiveChannels == nil {
			return LiveRemotePreviewDocument{}, livechannels.ErrUnavailable
		}
		value, e := d.LiveChannels.PreviewRemote(ctx, d.liveAuthority(p), livechannels.SourceFetcher{}, in)
		return LiveRemotePreviewDocument{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "POST", "/v1/admin/live-sources/remote/save", "publish_remote_live_source", 4096, func(ctx context.Context, q *http.Request, p identity.Principal, in LiveRemoteSave) (LiveSourceDocument, error) {
		if d.LiveChannels == nil {
			return LiveSourceDocument{}, livechannels.ErrUnavailable
		}
		value, e := d.LiveChannels.SaveRemote(ctx, d.liveAuthority(p), in.PreviewID, in.RequestID)
		return LiveSourceDocument{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "GET", "/v1/admin/live/settings", "get_live_settings", 0, func(ctx context.Context, q *http.Request, p identity.Principal, _ struct{}) (LiveOwnerResult[administration.Document[administration.LiveDefaults]], error) {
		var out LiveOwnerResult[administration.Document[administration.LiveDefaults]]
		if d.Administration == nil {
			return out, administration.ErrUnavailable
		}
		value, e := d.Administration.LiveDefaultsDocument(ctx, d.administrationAuthority(p))
		return LiveOwnerResult[administration.Document[administration.LiveDefaults]]{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "PUT", "/v1/admin/live/settings", "set_live_settings", 65536, func(ctx context.Context, q *http.Request, p identity.Principal, in administration.Change[administration.LiveDefaults]) (LiveOwnerResult[administration.Document[administration.LiveDefaults]], error) {
		var out LiveOwnerResult[administration.Document[administration.LiveDefaults]]
		if d.Administration == nil {
			return out, administration.ErrUnavailable
		}
		value, e := d.Administration.SaveLiveDefaults(ctx, d.administrationAuthority(p), in)
		return LiveOwnerResult[administration.Document[administration.LiveDefaults]]{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "GET", "/v1/admin/live-sources/{id}/configuration", "get_live_source_configuration", 0, func(ctx context.Context, q *http.Request, p identity.Principal, _ struct{}) (LiveOwnerResult[administration.LiveSourceDocument], error) {
		if d.Administration == nil {
			return LiveOwnerResult[administration.LiveSourceDocument]{}, administration.ErrUnavailable
		}
		value, e := d.Administration.LiveSourceConfiguration(ctx, d.administrationAuthority(p), q.PathValue("id"))
		return LiveOwnerResult[administration.LiveSourceDocument]{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "PUT", "/v1/admin/live-sources/{id}/configuration", "set_live_source_configuration", 8<<20, func(ctx context.Context, q *http.Request, p identity.Principal, in administration.Change[administration.LiveSourceSettings]) (LiveOwnerResult[administration.LiveSourceDocument], error) {
		if d.Administration == nil {
			return LiveOwnerResult[administration.LiveSourceDocument]{}, administration.ErrUnavailable
		}
		value, e := d.Administration.SaveLiveSourceConfiguration(ctx, d.administrationAuthority(p), q.PathValue("id"), in, d.LiveChannels.ConfigurationChangedTx)
		return LiveOwnerResult[administration.LiveSourceDocument]{"1.0", d.Identity.ID(), value}, e
	})
	liveContract(r, d, "GET", "/v1/admin/dvr/tuners", "get_live_tuners", 0, func(ctx context.Context, q *http.Request, p identity.Principal, _ struct{}) (LiveOwnerResult[administration.TunerView], error) {
		if d.Administration == nil {
			return LiveOwnerResult[administration.TunerView]{}, administration.ErrUnavailable
		}
		value, e := d.Administration.Tuners(ctx, d.administrationAuthority(p))
		return LiveOwnerResult[administration.TunerView]{"1.0", d.Identity.ID(), value}, e
	})
}
