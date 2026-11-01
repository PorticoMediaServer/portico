package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/connectivity"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Connectivity routes use the same typed registry as the first-party API.
type ConnectivityChange struct {
	SecureConnections *string   `json:"secureConnections,omitempty"`
	RemoteSecurity    *string   `json:"remoteSecurity,omitempty"`
	RemoteAccess      *string   `json:"remoteAccess,omitempty"`
	ExpectedRevision  int64     `json:"expectedRevision"`
	OperationID       string    `json:"operationId"`
	RemoteSignIn      *string   `json:"remoteSignInPolicy,omitempty"`
	RemoteBitrate     *int      `json:"remoteBitrateLimitKbps,omitempty"`
	LANNetworks       *[]string `json:"lanNetworks,omitempty"`
	AccessURLs        *[]string `json:"accessUrls,omitempty"`
	LANDiscovery      *bool     `json:"lanDiscoveryEnabled,omitempty"`
	DLNA              *bool     `json:"dlnaEnabled,omitempty"`
	TrustedProxies    *[]string `json:"trustedProxies,omitempty"`
	UploadCapacity    *int      `json:"uploadCapacityKbps,omitempty"`
	PausedTimeout     *int      `json:"pausedSessionTimeoutMinutes,omitempty"`
	Interface         *string   `json:"advertisedInterface,omitempty"`
	OwnAddressEnabled *bool     `json:"ownAddressEnabled,omitempty"`
	CertificatePath   *string   `json:"customCertificatePath,omitempty"`
	CertificateKey    *string   `json:"customCertificateKeyPath,omitempty"`
	CertificateHost   *string   `json:"customCertificateDomain,omitempty"`
	ConnectionLimit   *int      `json:"connectionLimit,omitempty"`
	AdvertiseURL      *string   `json:"advertiseUrl,omitempty"`
}

func (d Dependencies) changeConnectivity(r *http.Request, body ConnectivityChange) (connectivity.Policy, error) {
	p, err := d.owner(r)
	if err != nil {
		return connectivity.Policy{}, err
	}
	document, err := d.applySettings(r, p, body.ExpectedRevision, body.OperationID, func(v *operations.Settings) {
		if body.SecureConnections != nil {
			v.SecureConnections = *body.SecureConnections
		}
		if body.RemoteSignIn != nil {
			v.RemoteSignInPolicy = *body.RemoteSignIn
		}
		if body.RemoteSecurity != nil {
			v.RemoteSecurity = *body.RemoteSecurity
		}
		if body.RemoteAccess != nil {
			v.RemoteAccess = *body.RemoteAccess
		}
		if body.RemoteBitrate != nil {
			v.RemoteBitrateLimitKbps = *body.RemoteBitrate
		}
		if body.LANNetworks != nil {
			v.LANNetworks = *body.LANNetworks
		}
		if body.AccessURLs != nil {
			v.AccessURLs = *body.AccessURLs
		}
		if body.LANDiscovery != nil {
			v.LANDiscoveryEnabled = *body.LANDiscovery
		}
		if body.DLNA != nil {
			v.DLNAEnabled = *body.DLNA
		}
		if body.TrustedProxies != nil {
			v.TrustedProxies = *body.TrustedProxies
		}
		if body.UploadCapacity != nil {
			v.UploadCapacityKbps = *body.UploadCapacity
		}
		if body.PausedTimeout != nil {
			v.PausedSessionTimeoutMinutes = *body.PausedTimeout
		}
		if body.Interface != nil {
			v.AdvertisedInterface = *body.Interface
		}
		if body.OwnAddressEnabled != nil {
			v.OwnAddressEnabled = *body.OwnAddressEnabled
		}
		if body.CertificatePath != nil {
			v.CustomCertificatePath = *body.CertificatePath
		}
		if body.CertificateKey != nil {
			v.CustomCertificateKeyPath = *body.CertificateKey
		}
		if body.CertificateHost != nil {
			v.CustomCertificateDomain = *body.CertificateHost
		}
		if body.ConnectionLimit != nil {
			v.ConnectionLimit = *body.ConnectionLimit
		}
		if body.AdvertiseURL != nil {
			v.AdvertiseURL = *body.AdvertiseURL
		}
	})
	if err != nil {
		return connectivity.Policy{}, err
	}
	return connectivity.ProjectPolicy(document), nil
}

// ConnectionDocument lets sign-in show a quiet HTTP note and lets owners see
// the address actually used by rate limits and the home-network policy.
type ConnectionDocument struct {
	LANNetworks          []string `json:"lanNetworks"`
	SecureConnections    string   `json:"secureConnections"`
	Secure               bool     `json:"secure"`
	Remote               bool     `json:"remote"`
	PlainHTTPOutsideHome bool     `json:"plainHttpOutsideHome"`
	ClientAddress        string   `json:"clientAddress"`
	AccessURLs           []string `json:"accessUrls"`
}

func (d Dependencies) connectionDocument(r *http.Request) ConnectionDocument {
	secure, remote := d.secureTransport(r), d.requestIsRemote(r)
	out := ConnectionDocument{LANNetworks: append([]string{}, operations.DefaultSettings().LANNetworks...), SecureConnections: "preferred", Secure: secure, Remote: remote, PlainHTTPOutsideHome: !secure && remote, ClientAddress: d.setupPeerSubject(r), AccessURLs: []string{}}
	if setting, err := d.connectionSetting(r.Context()); err == nil && setting != nil {
		out.AccessURLs = append(out.AccessURLs, setting.accessURLs...)
		out.LANNetworks = append([]string{}, setting.lanNetworks...)
		out.SecureConnections = setting.secureConnections
		if setting.certificateDomain != "" && d.ListeningPort > 0 {
			host := setting.certificateDomain
			if d.ListeningPort != 443 {
				host = net.JoinHostPort(host, strconv.Itoa(d.ListeningPort))
			}
			candidate := "https://" + host
			found := false
			for _, origin := range out.AccessURLs {
				if origin == candidate {
					found = true
				}
			}
			if !found {
				out.AccessURLs = append(out.AccessURLs, candidate)
			}
		}
	}
	return out
}

type ConnectivityStatusDocument struct {
	connectivity.Status
	Connection   ConnectionDocument `json:"connection"`
	IdentityCode string             `json:"identityCode,omitempty"`
	LocalURL     string             `json:"localUrl,omitempty"`
}

// These owner-frame facts are observations, never settings or connection
// grants. The code comes only from the protected current key publication; the
// loopback URL was captured from the actual bound listener by root composition.
func (d Dependencies) connectivityOwnerFacts(ctx context.Context, r *http.Request, out *ConnectivityStatusDocument) error {
	p, err := d.owner(r)
	if err != nil {
		return nil
	}
	read, err := dbwork.BeginSnapshot(ctx, d.DB)
	if err != nil {
		return err
	}
	defer read.Rollback()
	if err = d.ownerAuthorityTx(ctx, read.Tx(), p); err != nil {
		return nil
	}
	out.LocalURL = d.LocalHTTPURL
	code, err := connectivityIdentityCodeTx(ctx, read.Tx())
	if err != nil {
		return err
	}
	out.IdentityCode = code
	return nil
}

func connectivityIdentityCodeTx(ctx context.Context, tx *sql.Tx) (string, error) {
	kind, err := identity.SignInKindTx(ctx, tx)
	if err != nil {
		return "", err
	}
	if kind != identity.SignInLocal {
		return "", nil
	}
	code, err := networking.IdentityCodeTx(ctx, tx)
	if errors.Is(err, networking.ErrStale) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return code, nil
}

func registerConnectivity(registry *apikit.Registry, d Dependencies) error {
	err := apikit.Register(registry, apikit.Route[struct{}, ConnectionDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "get_connection", Method: "GET", Path: "/v1/networking/connection", Summary: "Read the current connection and advertised addresses", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ConnectionDocument, error) {
		return d.connectionDocument(r), nil
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, connectivity.Policy]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_connectivity_policy", Method: "GET", Path: "/v1/admin/connectivity/policy", Summary: "Read home networks and connection settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (connectivity.Policy, error) {
		document, err := d.settingsDocument(ctx)
		if err != nil {
			return connectivity.Policy{}, connectionPolicyError(err)
		}
		return connectivity.ProjectPolicy(document), nil
	}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[ConnectivityChange, connectivity.Policy]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "change_connectivity_policy", Method: "PATCH", Path: "/v1/admin/connectivity/policy", Summary: "Set home networks and connection settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, BodyLimit: 32768, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "revision_mismatch", "request_in_progress", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body ConnectivityChange) (connectivity.Policy, error) {
		out, err := d.changeConnectivity(r, body)
		if err != nil {
			return connectivity.Policy{}, connectionPolicyError(err)
		}
		return out, nil
	}})
	if err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, ConnectivityStatusDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_connectivity_status", Method: "GET", Path: "/v1/admin/connectivity/status", Summary: "Read certificates and observed connection addresses", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "connectivity", Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ConnectivityStatusDocument, error) {
		document, err := d.settingsDocument(ctx)
		if err != nil {
			return ConnectivityStatusDocument{}, connectionPolicyError(err)
		}
		reporter := connectivity.Reporter{Advertiser: d.Access.Advertiser, Certificates: d.certificateStatus(), Custom: d.customCertificateStatus()}
		out := ConnectivityStatusDocument{Status: reporter.Report(ctx, connectivity.ProjectPolicy(document)), Connection: d.connectionDocument(r)}
		if err := d.connectivityOwnerFacts(ctx, r, &out); err != nil {
			return ConnectivityStatusDocument{}, err
		}
		return out, nil
	}})
}

// certificateStatus adapts the networking claim handler's certificate manager
// into the connectivity report's seam. A server without networking configured
// reports no TLS rather than failing the whole report.
func (d Dependencies) certificateStatus() func(context.Context) (networking.CertificateStatus, error) {
	if d.Networking == nil {
		return nil
	}
	manager := d.Networking.Certificates()
	if manager == nil {
		return nil
	}
	return manager.Status
}

// customCertificateStatus adapts the owner-supplied certificate into the
// report's optional seam. Nil leaves the custom section out of the report.
func (d Dependencies) customCertificateStatus() func(context.Context) networking.CustomCertificateStatus {
	if d.CustomCertificate == nil {
		return nil
	}
	custom := d.CustomCertificate
	return func(ctx context.Context) networking.CustomCertificateStatus { return custom.Status(ctx) }
}

// Networking identity proofs are typed public contracts. A trusted proxy's
// external Host is the host the client probed, even if the backend Host differs.
type IdentityProofRequest struct {
	BaseURL string `json:"baseUrl"`
	Nonce   string `json:"nonce"`
}
type IdentityProofResponse struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func registerIdentityProof(registry *apikit.Registry, d Dependencies) error {
	return apikit.RegisterRaw(registry, apikit.RawRoute[IdentityProofRequest, IdentityProofResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth, ID: "prove_network_identity", Method: "POST", Path: "/v1/networking/identity-proof", Summary: "Prove this server's pinned identity on the requested origin", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "rate_limited", "internal"}}, Serve: func(w http.ResponseWriter, r *http.Request) {
		if d.RouteIdentity == nil {
			failure(w, identity.ErrUnauthorized)
			return
		}
		request := r.Clone(r.Context())
		if origin, err := url.Parse(networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context()))); err == nil && origin.Host != "" {
			request.Host = origin.Host
		}
		d.RouteIdentity.ServeHTTP(w, request)
	}})
}

func connectionPolicyError(err error) error {
	var validation *operations.ValidationError
	if errors.As(err, &validation) {
		fields := []apierror.Field{}
		for _, path := range validation.Fields {
			fields = append(fields, apierror.Field{Path: path, Code: "invalid_value", Message: "Check this field and try again."})
		}
		return &apierror.Error{Code: "invalid_request", Fields: fields}
	}
	if errors.Is(err, operations.ErrConflict) {
		return &apierror.Error{Code: "revision_mismatch"}
	}
	if errors.Is(err, operations.ErrInvalid) || errors.Is(err, access.ErrInvalid) {
		return &apierror.Error{Code: "invalid_request"}
	}
	return foundationAdministrationError(err)
}
