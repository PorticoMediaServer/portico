package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/access"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Deleting a member. The owner (or an administrator, for a plain member) first
// reads what will be removed, then confirms with the revision that preview
// showed. Turning a member off stays on PATCH /v1/direct/members/{id}; this is
// the one that removes the person and their data.
func registerMemberRoutes(registry *apikit.Registry, d Dependencies) error {
	if err := apikit.Register(registry, apikit.Route[struct{}, MemberDeletionDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "get_direct_member_deletion_preview", Method: "GET", Path: "/v1/direct/members/{id}/deletion-preview", Summary: "Read what deleting a member will remove", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (MemberDeletionDocument, error) {
		out, err := d.Identity.PreviewDirectMemberDeletion(ctx, directBearer(r), r.PathValue("id"))
		if err != nil {
			return MemberDeletionDocument{}, foundationAuthErrorOrNil(err)
		}
		p, e := d.administrator(r)
		if e != nil {
			return MemberDeletionDocument{}, foundationAuthError(e)
		}
		m, e := d.Access.Access.Member(ctx, d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"))
		if e != nil {
			return MemberDeletionDocument{}, foundationAuthError(e)
		}
		if m.Revision != out.Revision {
			return MemberDeletionDocument{}, foundationAuthError(identity.ErrProfileChanged)
		}
		return MemberDeletionDocument{MemberDeletionPreview: out, Notes: m.Notes}, nil
	}}); err != nil {
		return err
	}
	// Every member's devices, for the owner and administrators: the same device
	// records sessions are bound to, with their approval. An administrator
	// who is not the owner sees and decides about plain members' devices.
	if err := apikit.Register(registry, apikit.Route[struct{}, access.PeopleDevicePage]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_admin_access_devices", Method: "GET", Path: "/v1/admin/access/devices", Summary: "List members' devices", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "members", Lane: apikit.Security, Cost: apikit.PageSized, Query: []string{"cursor", "limit", "account", "sort"}, QueryIntegers: emptyLimitQuery(25, 100), Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.PeopleDevicePage, error) {
		p, err := d.foundationCaller(r.WithContext(ctx))
		if err != nil {
			return access.PeopleDevicePage{}, foundationAuthError(err)
		}
		query := r.URL.Query()
		networks, e := d.peopleNetworks(ctx)
		if e != nil {
			return access.PeopleDevicePage{}, foundationAuthError(e)
		}
		out, err := d.Access.Access.PeopleDevices(ctx, d.accessAuthority(p, identity.TierAdmin), p, access.PeopleDeviceQuery{Cursor: query.Get("cursor"), Limit: query.Get("limit"), Account: query.Get("account"), Sort: query.Get("sort")}, networks)
		return out, peopleDeviceError(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, access.PeopleDevice]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_admin_access_device", Method: "GET", Path: "/v1/admin/access/devices/{id}", Summary: "Read one member's device as the devices list shows it", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "members", Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (access.PeopleDevice, error) {
		p, err := d.foundationCaller(r.WithContext(ctx))
		if err != nil {
			return access.PeopleDevice{}, foundationAuthError(err)
		}
		networks, e := d.peopleNetworks(ctx)
		if e != nil {
			return access.PeopleDevice{}, foundationAuthError(e)
		}
		out, err := d.Access.Access.PeopleDevice(ctx, d.accessAuthority(p, identity.TierAdmin), p, r.PathValue("id"), networks)
		return out, peopleDeviceError(err)
	}}); err != nil {
		return err
	}
	for _, route := range []struct{ id, path string }{{"post_direct_member_device_sign_out", "/v1/direct/members/{id}/devices/{deviceId}/sign-out"}, {"post_direct_member_devices_sign_out", "/v1/direct/members/{id}/devices/sign-out"}} {
		if err := apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence, ID: route.id, Method: "POST", Path: route.path, Summary: "Sign out one or all devices belonging to a person", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "members", Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 204, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
			p, e := d.foundationCaller(r.WithContext(ctx))
			if e != nil {
				return struct{}{}, foundationAuthError(e)
			}
			return struct{}{}, memberDeviceError(d.Identity.SignOutMemberDevices(ctx, p, r.PathValue("id"), r.PathValue("deviceId")))
		}}); err != nil {
			return err
		}
	}
	return apikit.Register(registry, apikit.Route[identity.MemberDeletion, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence, ID: "delete_direct_member", Method: "DELETE", Path: "/v1/direct/members/{id}", Summary: "Delete a member, their profiles, devices and saved data", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 204, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "not_found", "revision_mismatch", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body identity.MemberDeletion) (struct{}, error) {
		err := d.Identity.DeleteDirectMember(ctx, directBearer(r), r.PathValue("id"), body)
		return struct{}{}, foundationAuthErrorOrNil(err)
	}})
}

// RememberedAccountsRequest names the installation whose remembered accounts
// are wanted or changed. The installation id travels in the body: it is what a
// device renews its sign-in with, and an address ends up in proxy and access
// logs.
type RememberedAccountsRequest struct {
	InstallationID string `json:"installationId"`
}

type RememberedAccountsDocument struct {
	Items []identity.BrowserAccount `json:"items"`
}

// registerRememberedAccountRoutes: the account switcher a browser shows before
// anyone has signed in, and forgetting one entry of it.
func registerRememberedAccountRoutes(registry *apikit.Registry, d Dependencies) error {
	if err := apikit.Register(registry, apikit.Route[RememberedAccountsRequest, RememberedAccountsDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, // Unauthenticated by design: a browser that has not signed in is exactly
		// the caller that needs the account switcher, and no entry is a credential.
		ID: "post_auth_remembered_accounts", Method: "POST", Path: "/v1/auth/remembered-accounts", Summary: "List the accounts remembered on this installation", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "internal"},
	}, Handler: func(ctx context.Context, _ *http.Request, body RememberedAccountsRequest) (RememberedAccountsDocument, error) {
		items, err := d.Identity.BrowserAccounts(ctx, body.InstallationID)
		if err != nil {
			return RememberedAccountsDocument{}, rememberedAccountError(err)
		}
		return RememberedAccountsDocument{Items: items}, nil
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[RememberedAccountsRequest, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "delete_direct_remembered_account", Method: "DELETE", Path: "/v1/direct/remembered-accounts/{accountId}", Summary: "Forget one account remembered on this installation", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 204, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body RememberedAccountsRequest) (struct{}, error) {
		err := d.Identity.ForgetBrowserAccount(ctx, directBearer(r), body.InstallationID, r.PathValue("accountId"))
		return struct{}{}, rememberedAccountError(err)
	}})
}

func rememberedAccountError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, identity.ErrBrowserAccountInput) {
		return &apierror.Error{Code: "invalid_request"}
	}
	return foundationAuthError(err)
}

func memberDeviceError(err error) error {
	if err == nil {
		return nil
	}
	var changed *identity.DeviceChangedError
	if errors.As(err, &changed) {
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	}
	return foundationAuthError(err)
}

type MemberDeletionDocument struct {
	identity.MemberDeletionPreview
	Notes *string `json:"notes,omitempty"`
}

func (d Dependencies) peopleNetworks(ctx context.Context) ([]netip.Prefix, error) {
	document, e := d.settingsDocument(ctx)
	if e != nil {
		return nil, e
	}
	out := []netip.Prefix{}
	for _, raw := range document.Effective.LANNetworks {
		p, e := netip.ParsePrefix(raw)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
func peopleDeviceError(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return &apierror.Error{Code: "not_found"}
	}
	if e == nil {
		return nil
	}
	if errors.Is(e, access.ErrInvalid) {
		return &apierror.Error{Code: "invalid_request"}
	}
	return foundationAuthError(e)
}
