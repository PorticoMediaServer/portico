package httpapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

var defaultLocality = newLocalityPolicy(operations.DefaultSettings())

type localityPolicy struct{ lan, proxies []netip.Prefix }

func newLocalityPolicy(s operations.Settings) localityPolicy {
	out := localityPolicy{}
	for _, raw := range s.LANNetworks {
		if p, ok := operations.ParseNetwork(raw); ok {
			out.lan = append(out.lan, p)
		}
	}
	for _, raw := range s.TrustedProxies {
		if p, ok := operations.ParseNetwork(raw); ok {
			out.proxies = append(out.proxies, p)
		}
	}
	return out
}
func (d Dependencies) trustedProxyPrefixes(ctx context.Context) []netip.Prefix {
	setting, err := d.connectionSetting(ctx)
	if err != nil || setting == nil {
		return d.TrustedProxies
	}
	return setting.locality.proxies
}

// RequestIsRemote is the sole viewer locality decision. Unknown clients are remote.
func (d Dependencies) RequestIsRemote(r *http.Request) bool { return d.requestIsRemote(r) }
func (d Dependencies) requestIsRemote(r *http.Request) bool {
	setting, err := d.connectionSetting(r.Context())
	if err != nil {
		return true
	}
	policy := defaultLocality
	if setting != nil {
		policy = setting.locality
	}
	remote := remoteClient(r, d.trustedProxyPrefixes(r.Context()), policy)
	if remote && len(policy.lan) > 0 && d.Networking != nil {
		if client, ok := networkpolicy.Client(r, d.trustedProxyPrefixes(r.Context())); ok {
			for _, own := range d.Networking.PublicAddresses() {
				if client.Unmap() == own.Unmap() {
					return false
				}
			}
		}
	}
	return remote
}
func remoteClient(r *http.Request, proxies []netip.Prefix, policy localityPolicy) bool {
	client, ok := networkpolicy.Client(r, proxies)
	return !ok || !networkpolicy.Contains(policy.lan, client)
}

func (d Dependencies) sameRequestOrigin(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}
	actual := networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context()))
	if origin == actual {
		return true
	}
	// A proxy can preserve Host before the owner has configured proxy trust.
	// Browser Origin is checked against that exact host, without accepting any
	// forwarded header from an untrusted peer or asserting that HTTP is TLS.
	submitted, err := url.Parse(origin)
	target, targetErr := url.Parse(actual)
	return r.TLS == nil && err == nil && targetErr == nil && submitted.Scheme == "https" && submitted.Host == target.Host && submitted.Path == "" && submitted.User == nil && submitted.RawQuery == "" && !submitted.ForceQuery && submitted.Fragment == ""
}
