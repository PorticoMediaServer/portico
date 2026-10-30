package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type DevicesDocument struct {
	Items []identity.Device `json:"items"`
}

type DeviceBindingRequest struct {
	InstallationID  string `json:"installationId"`
	SessionFamilyID string `json:"sessionFamilyId"`
}

func registerDeviceRoutes(registry *apikit.Registry, d Dependencies) error {
	if err := apikit.Register(registry, apikit.Route[identity.DeviceRegistration, identity.Device]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "post_device", Method: "POST", Path: "/v1/devices", Summary: "Register or refresh this account's device", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 201, Errors: []string{"unauthorized", "invalid_request", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body identity.DeviceRegistration) (identity.Device, error) {
		out, err := d.Identity.RegisterDevice(ctx, directBearer(r), body, d.signInPeer(r))
		return out, foundationAuthErrorOrNil(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, DevicesDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "get_devices", Method: "GET", Path: "/v1/devices", Summary: "List this account's devices", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.PageSized, Query: []string{"installationId"}, Status: 200, Errors: []string{"unauthorized", "invalid_request", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (DevicesDocument, error) {
		current := r.URL.Query()["installationId"]
		if len(current) > 1 || len(current) == 1 && len(current[0]) > 128 {
			return DevicesDocument{}, &apierror.Error{Code: "invalid_request"}
		}
		installation := ""
		if len(current) == 1 {
			installation = current[0]
		}
		items, err := d.Identity.Devices(ctx, directBearer(r), installation)
		if err != nil {
			return DevicesDocument{}, foundationAuthError(err)
		}
		return DevicesDocument{Items: items}, nil
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[identity.DeviceEdit, identity.Device]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "patch_device", Method: "PATCH", Path: "/v1/devices/{id}", Summary: "Edit this account's device record", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body identity.DeviceEdit) (identity.Device, error) {
		out, err := d.Identity.EditDevice(ctx, directBearer(r), r.PathValue("id"), body)
		return out, foundationAuthErrorOrNil(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[DeviceBindingRequest, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "post_device_session_binding", Method: "POST", Path: "/v1/devices/{id}/sessions/bind", Summary: "Verify a session's existing device binding", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 204, Errors: []string{"unauthorized", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body DeviceBindingRequest) (struct{}, error) {
		err := d.Identity.BindDeviceFamilyForDevice(ctx, directBearer(r), r.PathValue("id"), body.InstallationID, body.SessionFamilyID)
		return struct{}{}, foundationAuthErrorOrNil(err)
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "delete_device_sessions", Method: "DELETE", Path: "/v1/devices/{id}/sessions", Summary: "Sign out every session on this device", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, Status: 204, Errors: []string{"unauthorized", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
		err := d.Identity.SignOutDevice(ctx, directBearer(r), r.PathValue("id"))
		return struct{}{}, foundationAuthErrorOrNil(err)
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "delete_device", Method: "DELETE", Path: "/v1/devices/{id}", Summary: "Forget a device and sign out its sessions", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, Status: 204, Errors: []string{"unauthorized", "invalid_request", "not_found", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
		err := d.Identity.ForgetDevice(ctx, directBearer(r), r.PathValue("id"))
		return struct{}{}, foundationAuthErrorOrNil(err)
	}})
}

func foundationAuthErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return foundationAuthError(err)
}
