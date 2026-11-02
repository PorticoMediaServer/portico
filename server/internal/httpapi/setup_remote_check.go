package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
)

// SetupRemoteCheckRequest is setup's lockout check: the setup token and the Remote access
// and Remote security the person chose.
type SetupRemoteCheckRequest struct {
	SetupToken     string `json:"setupToken"`
	RemoteAccess   string `json:"remoteAccess"`
	RemoteSecurity string `json:"remoteSecurity"`
}

// SetupRemoteCheck says whether the address setup is reached at would be refused once setup
// is complete, and which fixes would work: "address" (Keep allowing this address, adding
// Address to Custom addresses) and "standard" (Switch to Standard). Only fixes that would
// actually let this request in are listed; there may be none (remote access Off). Reason, when
// refused, is "access" (Remote access leaves this network out), "http" (plain HTTP from outside)
// or "name" (an address that isn't in Custom addresses).
type SetupRemoteCheck struct {
	Refused bool     `json:"refused"`
	Reason  string   `json:"reason,omitempty"`
	Fixes   []string `json:"fixes"`
	Address string   `json:"address,omitempty"`
}

func registerSetupRemoteCheck(registry *apikit.Registry, d Dependencies) error {
	return apikit.Register(registry, apikit.Route[SetupRemoteCheckRequest, SetupRemoteCheck]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "post_setup_remote_check", Method: "POST", Path: "/v1/setup/remote-check", Summary: "Say whether this address will be refused once setup is complete", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "setup_source_not_local", "internal"}}, Handler: func(ctx context.Context, r *http.Request, q SetupRemoteCheckRequest) (SetupRemoteCheck, error) {
		origin := r.Header.Get("Origin")
		if !d.setupRequestAllowed(r) || (origin != "" && !d.sameRequestOrigin(r, origin)) {
			return SetupRemoteCheck{}, foundationAuthError(identity.ErrUnauthorized)
		}
		if !d.setupSourceAllowed(r) {
			return SetupRemoteCheck{}, errSetupSourceNotLocal
		}
		if err := d.Identity.SetupLimit(ctx, d.setupPeerSubject(r), "setup-remote-check", 30); err != nil {
			return SetupRemoteCheck{}, setupTransportError(err)
		}
		if !d.Identity.SetupRequired() {
			return SetupRemoteCheck{}, setupTransportError(identity.ErrConflict)
		}
		if _, _, err := d.Identity.SetupState(ctx, q.SetupToken, time.Now(), nil); err != nil || q.SetupToken == "" {
			return SetupRemoteCheck{}, foundationAuthError(identity.ErrUnauthorized)
		}
		if !oneOfStrings(q.RemoteAccess, operations.RemoteAccessModes) || !oneOfStrings(q.RemoteSecurity, operations.RemoteSecurityModes) {
			return SetupRemoteCheck{}, &apierror.Error{Code: "invalid_request"}
		}
		return d.setupRemoteCheck(ctx, r, q.RemoteAccess, q.RemoteSecurity)
	}})
}

// setupRemoteCheck judges this request with the policy setup will leave behind: the stored
// settings with the chosen Remote access and Remote security, and no setup exception.
func (d Dependencies) setupRemoteCheck(ctx context.Context, r *http.Request, access, security string) (SetupRemoteCheck, error) {
	out := SetupRemoteCheck{Fixes: []string{}}
	document, err := d.settingsDocument(ctx)
	if err != nil {
		return out, connectionPolicyError(err)
	}
	values := document.Effective
	values.RemoteAccess, values.RemoteSecurity = access, security
	var own func() []netip.Addr
	if d.connectionPolicy != nil {
		own = d.connectionPolicy.own
	}
	policy := remoteGatePolicy(values, d.TrustedProxies, own, nil)
	peer, ok := networkpolicy.Peer(r)
	if !ok {
		return out, errors.New("no peer address")
	}
	switch policy.Socket(peer, r.TLS != nil) {
	case remotegate.Refuse:
		// Remote access Off or Private only: neither fix lets an outside address in.
		out.Refused, out.Reason = true, "access"
		return out, nil
	case remotegate.RefuseAfterHost:
		out.Refused, out.Reason, out.Fixes = true, "http", []string{"standard"}
		return out, nil
	}
	j, ok := d.judgeRequest(r, policy)
	if !ok || j.admit {
		return out, nil
	}
	out.Refused = true
	switch {
	case j.reason == remotegate.ReasonName && !(j.proxied && !remotegate.Plausible(remotegate.HostName(j.host))):
		out.Reason = "name"
		if address, ok := operations.AccessOrigin(networkpolicy.Origin(r, d.trustedProxyPrefixes(ctx))); ok {
			out.Fixes, out.Address = []string{"address", "standard"}, address
		} else {
			out.Fixes = []string{"standard"}
		}
	case j.reason == remotegate.ReasonName:
		out.Reason, out.Fixes = "name", []string{"standard"}
	case j.reason == remotegate.ReasonHTTP:
		out.Reason, out.Fixes = "http", []string{"standard"}
	default:
		out.Reason = "access"
	}
	return out, nil
}

func oneOfStrings(value string, options []string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
