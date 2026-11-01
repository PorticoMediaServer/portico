package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// remoteGatePolicy builds the O-169 admission policy from a settings document.
// Home networks and trusted proxies are the same lists the locality decision
// reads; the listed names are the owner's custom addresses, the external
// address and, while it is switched on, the custom-certificate domain.
func remoteGatePolicy(v operations.Settings, envProxies []netip.Prefix, own func() []netip.Addr) *remotegate.Policy {
	settings := remotegate.Settings{Security: v.RemoteSecurity, Access: v.RemoteAccess, Own: own}
	for _, raw := range v.LANNetworks {
		if p, ok := operations.ParseNetwork(raw); ok {
			settings.Home = append(settings.Home, p)
		}
	}
	settings.Proxies = append(settings.Proxies, envProxies...)
	for _, raw := range v.TrustedProxies {
		if p, ok := operations.ParseNetwork(raw); ok {
			settings.Proxies = append(settings.Proxies, p)
		}
	}
	settings.Hosts = append(settings.Hosts, v.AccessURLs...)
	if v.AdvertiseURL != "" {
		settings.Hosts = append(settings.Hosts, v.AdvertiseURL)
	}
	if v.OwnAddressEnabled && v.CustomCertificateDomain != "" {
		settings.Hosts = append(settings.Hosts, v.CustomCertificateDomain)
	}
	return remotegate.NewPolicy(settings)
}

// remoteAdmits is the second half of the gate: one request, after its socket
// was admitted. Over this server's TLS the Host must be the name the
// certificate proved, or one the owner added; through a trusted proxy it must
// be an owner-added name forwarded over HTTPS. A refusal writes nothing.
func (d Dependencies) remoteAdmits(r *http.Request) bool {
	if d.RemoteGate == nil {
		return true
	}
	policy := d.RemoteGate.Policy()
	if policy == nil {
		return true
	}
	peer, ok := networkpolicy.Peer(r)
	if !ok {
		return false
	}
	proxied := policy.Proxy(peer)
	client := peer
	host := r.Host
	forwardedProto := ""
	if proxied {
		// The client is the rightmost hop the trusted proxies didn't add
		// (networkpolicy.Forwarded), never a value the client wrote at the
		// left. A proxied request without a usable chain is judged as from
		// outside: the proxy's own address (often at home) never stands in.
		client = netip.Addr{}
		if forwarded, ok := networkpolicy.Client(r, d.trustedProxyPrefixes(r.Context())); ok {
			client = forwarded
		}
		if value := firstForwarded(r.Header.Get("X-Forwarded-Host")); value != "" {
			host = value
		}
		forwardedProto = firstForwarded(r.Header.Get("X-Forwarded-Proto"))
	}
	serverName := ""
	if r.TLS != nil {
		serverName = r.TLS.ServerName
	}
	admit, reason := policy.Request(client, proxied, serverName, forwardedProto, host)
	if !admit && reason == remotegate.ReasonName && proxied && !remotegate.Plausible(remotegate.HostName(host)) {
		// The proxy passed its upstream address, not the name people used:
		// show the owner the proxy, since there's no name to allow.
		d.RemoteGate.Count(remotegate.ReasonProxy)
		d.RemoteGate.RecordProxy(peer)
	} else if !admit && reason != "" {
		d.RemoteGate.Count(remotegate.NameReason(host, reason))
		d.RemoteGate.Record(host, reason)
	} else if !admit {
		d.RemoteGate.Count(remotegate.ReasonAccess)
	}
	return admit
}

func firstForwarded(value string) string {
	value, _, _ = strings.Cut(value, ",")
	return strings.TrimSpace(value)
}

// RefusedAddresses is what the owner sees under Remote Access in Enhanced: the
// names outside clients asked for and were refused, filtered to names that
// point at this server.
type RefusedAddresses struct {
	RemoteSecurity string                   `json:"remoteSecurity"`
	Names          []remotegate.RefusedName `json:"names"`
}

// AllowAddress adds one refused name to the custom addresses.
type AllowAddress struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	OperationID      string `json:"operationId"`
	Name             string `json:"name"`
}

func (d Dependencies) refusedAddresses(ctx context.Context) (RefusedAddresses, error) {
	out := RefusedAddresses{RemoteSecurity: remotegate.SecurityEnhanced, Names: []remotegate.RefusedName{}}
	document, err := d.settingsDocument(ctx)
	if err != nil {
		return out, connectionPolicyError(err)
	}
	policy := remoteGatePolicy(document.Effective, d.TrustedProxies, nil)
	out.RemoteSecurity = policy.Security()
	if d.RemoteGate == nil || policy.Security() != remotegate.SecurityEnhanced {
		return out, nil
	}
	var own []netip.Addr
	if d.Networking != nil {
		own = d.Networking.PublicAddresses()
	}
	out.Names = d.RemoteGate.Refused.List(own, policy.Listed)
	return out, nil
}

func (d Dependencies) allowAddress(r *http.Request, body AllowAddress) (RefusedAddresses, error) {
	p, err := d.owner(r)
	if err != nil {
		return RefusedAddresses{}, err
	}
	name := remotegate.HostName(body.Name)
	if !remotegate.Plausible(name) {
		return RefusedAddresses{}, &apierror.Error{Code: "invalid_request", Fields: []apierror.Field{{Path: "name", Code: "invalid_value", Message: "Enter a domain name."}}}
	}
	if _, err := d.applySettings(r, p, body.ExpectedRevision, body.OperationID, func(v *operations.Settings) {
		for _, raw := range v.AccessURLs {
			if remotegate.HostName(raw) == name {
				return
			}
		}
		v.AccessURLs = append(v.AccessURLs, "https://"+name)
	}); err != nil {
		return RefusedAddresses{}, connectionPolicyError(err)
	}
	if d.RemoteGate != nil {
		d.RemoteGate.Refused.Forget(name)
	}
	return d.refusedAddresses(r.Context())
}

func registerRemoteGate(registry *apikit.Registry, d Dependencies) error {
	err := apikit.Register(registry, apikit.Route[struct{}, RefusedAddresses]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_refused_addresses", Method: "GET", Path: "/v1/admin/connectivity/refused-addresses", Summary: "Read the addresses outside clients asked for and were refused", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (RefusedAddresses, error) {
		return d.refusedAddresses(ctx)
	}})
	if err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[AllowAddress, RefusedAddresses]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "allow_refused_address", Method: "POST", Path: "/v1/admin/connectivity/refused-addresses/allow", Summary: "Add a refused address to the custom addresses", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "revision_mismatch", "request_in_progress", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body AllowAddress) (RefusedAddresses, error) {
		return d.allowAddress(r, body)
	}})
}

// RunRemoteGate looks refused names up in the background and keeps the record
// across restarts: loaded once, saved at most once a minute when it changed,
// and once more as the server stops.
func RunRemoteGate(ctx context.Context, db *sql.DB, gate *remotegate.Gate) {
	if gate == nil || gate.Refused == nil {
		return
	}
	if db != nil {
		if names, err := loadRefusedNames(ctx, db); err == nil {
			gate.Refused.Restore(names)
		}
	}
	supervise.Go("remotegate.lookups", func() { gate.Refused.Run(ctx) })
	if db == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// The server is stopping: save what changed, briefly.
			done, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = saveRefusedNames(done, db, gate.Refused)
			cancel()
			return
		case <-ticker.C:
			_ = saveRefusedNames(ctx, db, gate.Refused)
		}
	}
}

func loadRefusedNames(ctx context.Context, db *sql.DB) ([]remotegate.RefusedName, error) {
	rows, err := db.QueryContext(ctx, `SELECT name,reason,first_seen_ms,last_seen_ms,count FROM remote_refused_names`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []remotegate.RefusedName{}
	for rows.Next() {
		var n remotegate.RefusedName
		var first, last int64
		if err = rows.Scan(&n.Name, &n.Reason, &first, &last, &n.Count); err != nil {
			return nil, err
		}
		n.FirstSeen, n.LastSeen = time.UnixMilli(first), time.UnixMilli(last)
		out = append(out, n)
	}
	return out, rows.Err()
}

func saveRefusedNames(ctx context.Context, db *sql.DB, refused *remotegate.Refused) error {
	names, changed := refused.Snapshot()
	if !changed {
		return nil
	}
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM remote_refused_names`); err != nil {
			return err
		}
		for _, n := range names {
			if _, err := tx.ExecContext(ctx, `INSERT INTO remote_refused_names(name,reason,first_seen_ms,last_seen_ms,count) VALUES(?,?,?,?,?)`,
				n.Name, n.Reason, n.FirstSeen.UnixMilli(), n.LastSeen.UnixMilli(), n.Count); err != nil {
				return err
			}
		}
		return nil
	})
}
