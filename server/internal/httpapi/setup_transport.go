package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
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
	// No source check here: the setup secret leaves this server only through the
	// start routes (setupStart), which admit local sources only (S1).
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
		writeSetupError(w, e)
		return
	}
	// Setup wrote Remote access and Remote security (O-169): the gate and the
	// remote manager take them now, as after any settings save.
	d.reloadConnectionPolicy(r.Context())
	if d.SettingsApplied != nil {
		d.SettingsApplied(r.Context())
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
	// The same address check as a browser start: an app reached the server at a local or
	// configured address, never through a name that rebinds to it.
	return d.setupRequestAllowed(r) && r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Mode") == "" && d.browserSetupAuthorityAllowed(r)
}

// setupSourceAllowed is PLEX-SETUP-NOTES S1: first-run setup is open to this machine and
// the local network over plain HTTP — loopback, RFC 1918, CGNAT, link-local, IPv6 ULA —
// plus the owner's LAN networks, never a public-internet source.
//
// A reverse proxy the owner hasn't trusted (no one can configure trust before setup) makes
// every visitor look like the proxy's own LAN address. So a request carrying forwarding
// headers from an untrusted peer is not local: set up at the server's own address instead.
func (d Dependencies) setupSourceAllowed(r *http.Request) bool {
	proxies := d.trustedProxyPrefixes(r.Context())
	if peer, ok := networkpolicy.Peer(r); !ok || !networkpolicy.Contains(proxies, peer) {
		for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip", "Via"} {
			if r.Header.Get(header) != "" {
				return false
			}
		}
	}
	client, ok := networkpolicy.Client(r, proxies)
	return ok && (d.localSetupPeer(client.Unmap()) || !d.requestIsRemote(r))
}

// localSetupPeer is the one notion of a local peer, shared with the remote gate: the
// gate's home (loopback, every private and link-local range, the detected and configured
// home networks, and the tailnet under remote access On or Private only), or, where no
// gate is running, the home of an unconfigured server's default policy (remote access
// On: the fixed local set and the tailnet).
func (d Dependencies) localSetupPeer(client netip.Addr) bool {
	if d.RemoteGate != nil {
		if policy := d.RemoteGate.Policy(); policy != nil {
			return policy.Home(client)
		}
	}
	return defaultHome.Home(client)
}

var defaultHome = remotegate.NewPolicy(remotegate.Settings{Access: remotegate.AccessOn})

var errSetupSourceNotLocal = &apierror.Error{Code: "setup_source_not_local"}

// setupStart answers a first-run setup secret once the caller has passed its route's check.
func (d Dependencies) setupStart(ctx context.Context, r *http.Request, bucket string) (BrowserSetupResponse, error) {
	if !d.setupSourceAllowed(r) {
		return BrowserSetupResponse{}, errSetupSourceNotLocal
	}
	if err := d.Identity.SetupLimit(ctx, d.setupPeerSubject(r), bucket, 10); err != nil {
		return BrowserSetupResponse{}, setupTransportError(err)
	}
	token, err := d.Identity.BrowserSetupToken(ctx)
	if err != nil {
		return BrowserSetupResponse{}, setupTransportError(err)
	}
	return BrowserSetupResponse{d.Identity.ID(), token}, nil
}

// setupStartOver ends whatever setup is unfinished and starts a new one at this address.
// The earlier claim is withdrawn first (queued for Portico Services when it was approved),
// then the earlier setup is retired and a fresh setup secret issued.
func (d Dependencies) setupStartOver(ctx context.Context, r *http.Request) (BrowserSetupResponse, error) {
	if !d.setupSourceAllowed(r) {
		return BrowserSetupResponse{}, errSetupSourceNotLocal
	}
	if err := d.Identity.SetupLimit(ctx, d.setupPeerSubject(r), "setup-start-over", 10); err != nil {
		return BrowserSetupResponse{}, setupTransportError(err)
	}
	if d.Networking != nil {
		if _, err := d.Networking.RetireSetupClaim(ctx); err != nil {
			return BrowserSetupResponse{}, &apierror.Error{Code: "portico_services_unavailable", Cause: err}
		}
	}
	token, err := d.Identity.StartOverSetup(ctx)
	if errors.Is(err, identity.ErrSetupClaimLive) {
		return BrowserSetupResponse{}, &apierror.Error{Code: "portico_services_unavailable", Cause: err}
	}
	if err != nil {
		return BrowserSetupResponse{}, setupTransportError(err)
	}
	return BrowserSetupResponse{d.Identity.ID(), token}, nil
}

func registerSetupTransport(registry *apikit.Registry, d Dependencies) error {
	err := apikit.RegisterRaw(registry, apikit.RawRoute[identity.SetupOptions, identity.SetupResult]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "setup_server", Method: "POST", Path: "/v1/setup", Summary: "Create the first owner", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 201, BodyLimit: 65536, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "setup_in_progress", "setup_portico_mode", "internal"}}, Serve: d.serveSetup})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, BrowserSetupResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "start_browser_setup", Method: "POST", Path: "/v1/setup/browser", Summary: "Start first-run setup from this origin", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "setup_in_progress", "setup_source_not_local", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (BrowserSetupResponse, error) {
		if !d.setupRequestAllowed(r) || !d.sameRequestOrigin(r, r.Header.Get("Origin")) {
			return BrowserSetupResponse{}, foundationAuthError(identity.ErrUnauthorized)
		}
		return d.setupStart(ctx, r, "browser-setup")
	}})
	if err != nil {
		return err
	}
	// Finding #2: any unfinished setup can be started over by whoever may start setup.
	err = apikit.Register(registry, apikit.Route[struct{}, BrowserSetupResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "post_setup_start_over", Method: "POST", Path: "/v1/setup/start-over", Summary: "End any unfinished first-run setup and start again from this address", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "setup_source_not_local", "portico_services_unavailable", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (BrowserSetupResponse, error) {
		origin := r.Header.Get("Origin")
		if !d.setupRequestAllowed(r) || (origin == "" && !d.appSetupRequest(r)) || (origin != "" && !d.sameRequestOrigin(r, origin)) {
			return BrowserSetupResponse{}, foundationAuthError(identity.ErrUnauthorized)
		}
		return d.setupStartOver(ctx, r)
	}})
	if err != nil {
		return err
	}
	err = apikit.RegisterRaw(registry, apikit.RawRoute[SetupAbandonRequest, SetupStateResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "post_setup_abandon", Method: "POST", Path: "/v1/setup/abandon", Summary: "Go back to the start of an unapproved Portico Account setup", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_state_changed", "setup_source_not_local", "internal"}}, Serve: d.serveSetupAbandon})
	if err != nil {
		return err
	}
	err = apikit.RegisterRaw(registry, apikit.RawRoute[networking.SetupApprovalRequest, networking.ClaimStatus]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "post_setup_approval", Method: "POST", Path: "/v1/setup/approval", Summary: "Ask a Portico Account to approve this setup", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 20 << 10, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_state_changed", "setup_source_not_local", "internal"}}, Serve: d.serveSetupApproval})
	if err != nil {
		return err
	}
	err = apikit.RegisterRaw(registry, apikit.RawRoute[SetupRetryRequest, SetupStateResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "post_setup_retry", Method: "POST", Path: "/v1/setup/retry", Summary: "Try again to apply the Portico Account approval of this setup", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_state_changed", "setup_source_not_local", "internal"}}, Serve: d.serveSetupRetry})
	if err != nil {
		return err
	}
	// First run from the Portico app on a phone or tablet that found a server nobody has set up.
	return apikit.Register(registry, apikit.Route[struct{}, BrowserSetupResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "start_app_setup", Method: "POST", Path: "/v1/setup/app", Summary: "Start first-run setup from an app", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "invalid_request", "rate_limited", "setup_already_complete", "setup_in_progress", "setup_source_not_local", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (BrowserSetupResponse, error) {
		if !d.appSetupRequest(r) {
			return BrowserSetupResponse{}, foundationAuthError(identity.ErrUnauthorized)
		}
		return d.setupStart(ctx, r, "app-setup")
	}})
}

// writeSetupError answers a setup refusal with its own code, and anything else as usual.
func writeSetupError(w http.ResponseWriter, err error) {
	var progress *identity.SetupInProgressError
	if errors.Is(err, identity.ErrConflict) || errors.Is(err, identity.ErrPorticoSetup) || errors.Is(err, setupflow.ErrStateChanged) || errors.As(err, &progress) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), setupTransportError(err))
		return
	}
	failure(w, err)
}

func setupTransportError(err error) error {
	if errors.Is(err, identity.ErrConflict) {
		return &apierror.Error{Code: "setup_already_complete"}
	}
	if errors.Is(err, identity.ErrPorticoSetup) {
		return &apierror.Error{Code: "setup_portico_mode"}
	}
	if errors.Is(err, setupflow.ErrStateChanged) {
		return &apierror.Error{Code: "setup_state_changed"}
	}
	var progress *identity.SetupInProgressError
	if errors.As(err, &progress) {
		if progress.Until.IsZero() {
			// Approved and finishing: no moment to start over at.
			return &apierror.Error{Code: "setup_in_progress"}
		}
		wait := int(time.Until(progress.Until).Seconds()) + 1
		return &apierror.Error{Code: "setup_in_progress", RetryAfterSeconds: max(wait, 1)}
	}
	var protocol *identity.SetupProtocolError
	if errors.As(err, &protocol) && protocol.Code == "rate_limited" {
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: protocol.Interval}
	}
	return foundationAuthError(err)
}
func connectionErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{{Code: "setup_already_complete", Status: 409, Message: "This server is already set up.", Retry: apierror.Never},
		{Code: "setup_in_progress", Status: 409, Message: "Setup is already in progress in another browser.", Retry: apierror.SameRequest},
		{Code: "setup_source_not_local", Status: 403, Message: "Setup has to start from a device on the same network as this server.", Retry: apierror.Never},
		{Code: "setup_portico_mode", Status: 409, Message: "This server is being set up for a Portico Account. Go back to the start of setup to choose local sign-in.", Retry: apierror.Never},
		{Code: "setup_state_changed", Status: 409, Message: "Setup moved on. Read its state again.", Retry: apierror.Never}}
}
