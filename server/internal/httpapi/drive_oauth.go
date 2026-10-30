package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/driveoauth"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// DriveOAuthStartInput is the owner's own Google OAuth client. The secret is
// used only by the server's exchange and never returned.
type DriveOAuthStartInput struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type DriveOAuthStartResponse struct {
	Scope            catalog.AdminScope `json:"scope"`
	AuthorizationURL string             `json:"authorizationUrl"`
	State            string             `json:"state"`
}

type DriveOAuthStateInput struct {
	State string `json:"state"`
}

type DriveOAuthPasteInput struct {
	State       string `json:"state"`
	RedirectURL string `json:"redirectUrl"`
}

type DriveOAuthStatusResponse struct {
	Scope catalog.AdminScope `json:"scope"`
	Phase driveoauth.Phase   `json:"phase"`
	Token string             `json:"token,omitempty"`
}

// JSONSchema keeps the phase a closed enum in the published contract.
func (DriveOAuthStatusResponse) JSONSchema() map[string]any {
	type wire struct {
		Scope catalog.AdminScope `json:"scope"`
		Phase driveoauth.Phase   `json:"phase"`
		Token string             `json:"token,omitempty"`
	}
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["properties"].(map[string]any)["phase"].(map[string]any)["enum"] = []string{"pending", "ready", "expired", "denied"}
	return shape
}

// DriveOAuthPasteResponse acknowledges one recorded callback without carrying a
// cancellation meaning.
type DriveOAuthPasteResponse struct {
	Scope catalog.AdminScope `json:"scope"`
	Phase driveoauth.Phase   `json:"phase"`
}

func (DriveOAuthPasteResponse) JSONSchema() map[string]any {
	type wire struct {
		Scope catalog.AdminScope `json:"scope"`
		Phase driveoauth.Phase   `json:"phase"`
	}
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["properties"].(map[string]any)["phase"].(map[string]any)["enum"] = []string{"pending", "ready", "expired", "denied"}
	return shape
}

type DriveOAuthCancelResponse struct {
	Scope     catalog.AdminScope `json:"scope"`
	Cancelled bool               `json:"cancelled"`
}

// driveOAuthActor binds a sign-in to the verified owner session, not only to the
// account: the viewer fence carries the identity epoch, so another same-owner
// session cannot take over or read this one.
// sameOwnerSession proves the rechecked principal is the exact verified
// authorization that started this request: the verified authorization hash
// (never a token), its expiry and the viewer fence must all match. A replacement
// token intentionally ends the operation.
func sameOwnerSession(first identity.Principal, firstScope catalog.AdminScope, again identity.Principal, againScope catalog.AdminScope) bool {
	return first.Hash != "" && first.Hash == again.Hash && first.ExpiresAt.Equal(again.ExpiresAt) &&
		first.Authority == again.Authority && first.AccountID == again.AccountID && first.ProfileID == again.ProfileID &&
		first.Epoch == again.Epoch && firstScope.ViewerFence == againScope.ViewerFence
}

// driveOAuthActor is the stored key that binds a sign-in to the exact verified
// authorization: the verified authorization hash (never a raw token) as well as
// the account and viewer fence. An empty hash has no verified authorization and
// is refused. Start, status, paste and cancel all compare this same key.
func driveOAuthActor(p identity.Principal, viewerFence string) (string, error) {
	if p.Hash == "" {
		return "", errors.New("no verified authorization")
	}
	actor, err := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID, viewerFence, p.Hash})
	if err != nil {
		return "", err
	}
	return string(actor), nil
}

func registerDriveOAuth(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_source_drive_oauth_start", Method: "POST", Path: "/v1/storage/sources/rclone/drive/oauth/start", Summary: "Start the owner's own Google OAuth sign-in for a Drive source", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "administration_unavailable", "internal"}}, func(_ context.Context, r *http.Request, in DriveOAuthStartInput) (DriveOAuthStartResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return DriveOAuthStartResponse{}, rcloneConnectionFailure(err)
		}
		if d.DriveOAuth == nil {
			return DriveOAuthStartResponse{}, &apierror.Error{Code: "administration_unavailable"}
		}
		if r.URL.RawQuery != "" {
			return DriveOAuthStartResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		actor, err := driveOAuthActor(p, scope.ViewerFence)
		if err != nil {
			return DriveOAuthStartResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		authorizationURL, state, err := d.DriveOAuth.Start(actor, in.ClientID, in.ClientSecret, p.ExpiresAt)
		if err != nil {
			return DriveOAuthStartResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		again, againScope, err := d.storageOwner(r)
		if err != nil || !sameOwnerSession(p, scope, again, againScope) {
			d.DriveOAuth.Cancel(actor, state)
			return DriveOAuthStartResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		return DriveOAuthStartResponse{Scope: scope, AuthorizationURL: authorizationURL, State: state}, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_source_drive_oauth_paste", Method: "POST", Path: "/v1/storage/sources/rclone/drive/oauth/paste", Summary: "Take the code from the address Google ended on", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "source_credentials_required", "administration_unavailable", "internal"}}, func(_ context.Context, r *http.Request, in DriveOAuthPasteInput) (DriveOAuthPasteResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return DriveOAuthPasteResponse{}, rcloneConnectionFailure(err)
		}
		if d.DriveOAuth == nil {
			return DriveOAuthPasteResponse{}, &apierror.Error{Code: "administration_unavailable"}
		}
		if r.URL.RawQuery != "" {
			return DriveOAuthPasteResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		actor, actorErr := driveOAuthActor(p, scope.ViewerFence)
		if actorErr != nil {
			return DriveOAuthPasteResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		if err = d.DriveOAuth.Paste(actor, in.State, in.RedirectURL); err != nil {
			if errors.Is(err, driveoauth.ErrDenied) {
				return DriveOAuthPasteResponse{Scope: scope, Phase: driveoauth.Denied}, nil
			}
			return DriveOAuthPasteResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		again, againScope, err := d.storageOwner(r)
		if err != nil || !sameOwnerSession(p, scope, again, againScope) {
			return DriveOAuthPasteResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		return DriveOAuthPasteResponse{Scope: scope, Phase: driveoauth.Pending}, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_source_drive_oauth_status", Method: "POST", Path: "/v1/storage/sources/rclone/drive/oauth/status", Summary: "Read the owner's Google sign-in and exchange its one code", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "source_credentials_required", "administration_unavailable", "internal"}}, func(ctx context.Context, r *http.Request, in DriveOAuthStateInput) (DriveOAuthStatusResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return DriveOAuthStatusResponse{}, rcloneConnectionFailure(err)
		}
		if d.DriveOAuth == nil {
			return DriveOAuthStatusResponse{}, &apierror.Error{Code: "administration_unavailable"}
		}
		if r.URL.RawQuery != "" {
			return DriveOAuthStatusResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		actor, actorErr := driveOAuthActor(p, scope.ViewerFence)
		if actorErr != nil {
			return DriveOAuthStatusResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		result, err := d.DriveOAuth.Status(ctx, actor, in.State)
		if err != nil {
			if errors.Is(err, driveoauth.ErrExchange) {
				return DriveOAuthStatusResponse{}, &apierror.Error{Code: "source_credentials_required"}
			}
			return DriveOAuthStatusResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		again, againScope, err := d.storageOwner(r)
		if err != nil || !sameOwnerSession(p, scope, again, againScope) {
			d.DriveOAuth.Cancel(actor, in.State)
			return DriveOAuthStatusResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		return DriveOAuthStatusResponse{Scope: scope, Phase: result.Phase, Token: result.Token}, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_source_drive_oauth_cancel", Method: "POST", Path: "/v1/storage/sources/rclone/drive/oauth/cancel", Summary: "End the owner's Google sign-in session", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "administration_unavailable", "internal"}}, func(_ context.Context, r *http.Request, in DriveOAuthStateInput) (DriveOAuthCancelResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return DriveOAuthCancelResponse{}, rcloneConnectionFailure(err)
		}
		if d.DriveOAuth == nil {
			return DriveOAuthCancelResponse{}, &apierror.Error{Code: "administration_unavailable"}
		}
		if r.URL.RawQuery != "" {
			return DriveOAuthCancelResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		actor, actorErr := driveOAuthActor(p, scope.ViewerFence)
		if actorErr != nil {
			return DriveOAuthCancelResponse{}, &apierror.Error{Code: "not_permitted"}
		}
		d.DriveOAuth.Cancel(actor, in.State)
		return DriveOAuthCancelResponse{Scope: scope, Cancelled: true}, nil
	})
}
