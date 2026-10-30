package httpapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"strings"
)

// Native clients may omit Origin. Browser writes require the request's exact
// origin, including for callers otherwise permitted by cross-origin API CORS.
func (d Dependencies) setupRequestAllowed(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || (d.sameRequestOrigin(r, origin) && d.browserSetupAuthorityAllowed(r))
}

// The bootstrap secret must not be readable through an attacker-controlled DNS
// name that rebinds to this listener. Use independent local/configured names;
// never resolve the submitted name and then trust the answer.
func (d Dependencies) browserSetupAuthorityAllowed(r *http.Request) bool {
	origin, err := url.Parse(networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context())))
	if err != nil || origin.Host == "" {
		return false
	}
	if addr, err := netip.ParseAddr(origin.Hostname()); err == nil {
		return !addr.IsUnspecified() && !addr.IsMulticast()
	}
	host := strings.TrimSuffix(strings.ToLower(origin.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	if machine, err := os.Hostname(); err == nil && machine != "" {
		if setupMachineHostMatches(host, machine) {
			return true
		}
	}
	for _, allowed := range d.SetupHosts {
		if setupHostsMatch(origin.Host, strings.TrimSpace(allowed)) {
			return true
		}
	}
	setting, err := d.connectionSetting(r.Context())
	if err != nil {
		return false
	}
	if setting != nil {
		browser, err := url.Parse(r.Header.Get("Origin"))
		if err != nil {
			return false
		}
		for _, raw := range setting.accessURLs {
			if configured, err := url.Parse(raw); err == nil && configured.Scheme == browser.Scheme && setupHostsMatch(configured.Host, origin.Host) {
				return true
			}
		}
	}
	return false
}
func (d Dependencies) serveSetup(w http.ResponseWriter, r *http.Request) {
	if !d.setupRequestAllowed(r) {
		failure(w, identity.ErrUnauthorized)
		return
	}
	var body identity.SetupOptions
	if e := decode(w, r, &body); e != nil {
		failure(w, e)
		return
	}
	if e := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "setup", 10); e != nil {
		setupFailure(w, e)
		return
	}
	result, e := d.Identity.SetupWithOptions(r.Context(), body, d.privateSetupPeer(r))
	if e != nil {
		failure(w, e)
		return
	}
	writeAuth(w, r, 201, result)
}

type BrowserSetupResponse struct {
	ServerID   string `json:"serverId"`
	SetupToken string `json:"setupToken"`
}

// appSetupRequest is a request from an app (phone, tablet, television), never from a browser
// page: browsers send Origin on every POST, and a page on another site cannot send this JSON
// without a preflight, which the router refuses for the setup routes. Whoever can reach the
// server may set it up from a browser at its address; an app at its address may do the same.
func (d Dependencies) appSetupRequest(r *http.Request) bool {
	return d.setupRequestAllowed(r) && r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Mode") == ""
}

// setupStart answers a first-run setup secret once the caller has passed its route's check.
func (d Dependencies) setupStart(ctx context.Context, r *http.Request, bucket string) (BrowserSetupResponse, error) {
	if err := d.Identity.SetupLimit(ctx, d.setupPeerSubject(r), bucket, 10); err != nil {
		return BrowserSetupResponse{}, setupTransportError(err)
	}
	token, err := d.Identity.BrowserSetupToken(ctx)
	if err != nil {
		return BrowserSetupResponse{}, setupTransportError(err)
	}
	return BrowserSetupResponse{d.Identity.ID(), token}, nil
}

func registerSetupTransport(registry *apikit.Registry, d Dependencies) error {
	err := apikit.RegisterRaw(registry, apikit.RawRoute[identity.SetupOptions, identity.SetupResult]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "setup_server", Method: "POST", Path: "/v1/setup", Summary: "Create the first owner", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 201, BodyLimit: 65536, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "internal"}}, Serve: d.serveSetup})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, BrowserSetupResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "start_browser_setup", Method: "POST", Path: "/v1/setup/browser", Summary: "Start first-run setup from this origin", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (BrowserSetupResponse, error) {
		if !d.setupRequestAllowed(r) || !d.sameRequestOrigin(r, r.Header.Get("Origin")) {
			return BrowserSetupResponse{}, foundationAuthError(identity.ErrUnauthorized)
		}
		return d.setupStart(ctx, r, "browser-setup")
	}})
	if err != nil {
		return err
	}
	// First run from the Portico app on a phone or tablet that found a server nobody has set up.
	return apikit.Register(registry, apikit.Route[struct{}, BrowserSetupResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "start_app_setup", Method: "POST", Path: "/v1/setup/app", Summary: "Start first-run setup from an app", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (BrowserSetupResponse, error) {
		if !d.appSetupRequest(r) {
			return BrowserSetupResponse{}, foundationAuthError(identity.ErrUnauthorized)
		}
		return d.setupStart(ctx, r, "app-setup")
	}})
}

func setupTransportError(err error) error {
	if errors.Is(err, identity.ErrConflict) {
		return &apierror.Error{Code: "setup_already_complete"}
	}
	var protocol *identity.SetupProtocolError
	if errors.As(err, &protocol) && protocol.Code == "rate_limited" {
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: protocol.Interval}
	}
	return foundationAuthError(err)
}
func connectionErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{{Code: "setup_already_complete", Status: 409, Message: "This server is already set up.", Retry: apierror.Never}}
}
