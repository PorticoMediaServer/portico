// Package connectivity answers "how can clients reach this server, and how is
// that allowed to happen". The policy itself lives in the settings registry, so
// this package only projects it, observes the host, and reports the two together.
package connectivity

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Policy is the typed projection of the connectivity rows in the settings
// registry. Its revision is the settings document revision, so a write here and
// a write on the settings page cannot silently overwrite each other.
type Policy struct {
	SecureConnections       string   `json:"secureConnections"`
	OwnAddressEnabled       bool     `json:"ownAddressEnabled"`
	ConnectionLimit         int      `json:"connectionLimit"`
	AdvertiseURL            string   `json:"advertiseUrl"`
	Revision                int64    `json:"revision"`
	RemoteSignIn            string   `json:"remoteSignInPolicy"`
	RemoteBitrateLimitKbps  int      `json:"remoteBitrateLimitKbps"`
	LANNetworks             []string `json:"lanNetworks"`
	AccessURLs              []string `json:"accessUrls"`
	LANDiscoveryEnabled     bool     `json:"lanDiscoveryEnabled"`
	DLNAEnabled             bool     `json:"dlnaEnabled"`
	TrustedProxies          []string `json:"trustedProxies"`
	UploadCapacityKbps      int      `json:"uploadCapacityKbps"`
	PausedSessionTimeout    int      `json:"pausedSessionTimeoutMinutes"`
	AdvertisedInterface     string   `json:"advertisedInterface"`
	CustomCertificatePath   string   `json:"customCertificatePath"`
	CustomCertificateKey    string   `json:"customCertificateKeyPath"`
	CustomCertificateDomain string   `json:"customCertificateDomain"`
}

// ProjectPolicy reads the connectivity rows out of a settings document.
func ProjectPolicy(document operations.SettingsDocument) Policy {
	v := document.Effective
	out := Policy{SecureConnections: v.SecureConnections, OwnAddressEnabled: v.OwnAddressEnabled, ConnectionLimit: v.ConnectionLimit, AdvertiseURL: v.AdvertiseURL, Revision: document.Revision, RemoteSignIn: v.RemoteSignInPolicy, RemoteBitrateLimitKbps: v.RemoteBitrateLimitKbps,
		LANNetworks: v.LANNetworks, AccessURLs: v.AccessURLs, LANDiscoveryEnabled: v.LANDiscoveryEnabled, DLNAEnabled: v.DLNAEnabled,
		TrustedProxies: v.TrustedProxies, UploadCapacityKbps: v.UploadCapacityKbps, PausedSessionTimeout: v.PausedSessionTimeoutMinutes,
		AdvertisedInterface: v.AdvertisedInterface, CustomCertificatePath: v.CustomCertificatePath, CustomCertificateKey: v.CustomCertificateKeyPath, CustomCertificateDomain: v.CustomCertificateDomain}
	if out.SecureConnections == "" {
		out.SecureConnections = "preferred"
	}
	if out.TrustedProxies == nil {
		out.TrustedProxies = []string{}
	}
	if out.LANNetworks == nil {
		out.LANNetworks = []string{}
	}
	if out.AccessURLs == nil {
		out.AccessURLs = []string{}
	}
	return out
}

// Interface is one network interface as the status report shows it.
type Interface struct {
	Name      string   `json:"name"`
	Up        bool     `json:"up"`
	Loopback  bool     `json:"loopback"`
	Virtual   bool     `json:"virtual"`
	Addresses []string `json:"addresses"`
}

// Address is one address the server believes it can be reached at.
type Address struct {
	Address string `json:"address"`
	Family  string `json:"family"`
	// Class is "public", "lan" or "loopback". A "lan" address is one inside a
	// configured LAN network, or a private address when none is configured.
	Class string `json:"class"`
	// Source is where the address came from: "interface" or "accessUrl".
	Source string `json:"source"`
}

// TLS is the certificate side of the report, read from internal/networking.
type TLS struct {
	Configured      bool               `json:"configured"`
	State           string             `json:"state,omitempty"`
	ErrorCode       string             `json:"errorCode,omitempty"`
	DNSName         string             `json:"dnsName,omitempty"`
	Issuer          string             `json:"issuer,omitempty"`
	NotAfter        string             `json:"notAfter,omitempty"`
	TLSReady        bool               `json:"tlsReady"`
	PubliclyTrusted bool               `json:"publiclyTrusted"`
	ListenerBound   bool               `json:"listenerBound"`
	ListenPort      int                `json:"listenPort"`
	RouteURL        string             `json:"routeUrl,omitempty"`
	Reachability    string             `json:"reachability,omitempty"`
	Custom          *CustomCertificate `json:"customCertificate,omitempty"`
}

// CustomCertificate is the owner-supplied certificate side of the report.
type CustomCertificate struct {
	Serving   bool   `json:"serving"`
	State     string `json:"state,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	Domain    string `json:"domain,omitempty"`
	NotAfter  string `json:"notAfter,omitempty"`
	Issuer    string `json:"issuer,omitempty"`
}

// Discovery reports the LAN advertisement: what the owner asked for, and what
// this host can actually do.
type Discovery struct {
	Enabled   bool   `json:"enabled"`
	Supported bool   `json:"supported"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
	Service   string `json:"serviceType,omitempty"`
}

// Status is the whole report.
type Status struct {
	ObservedAt string      `json:"observedAt"`
	Policy     Policy      `json:"policy"`
	TLS        TLS         `json:"tls"`
	Discovery  Discovery   `json:"discovery"`
	Addresses  []Address   `json:"addresses"`
	Interfaces []Interface `json:"interfaces"`
	// Warnings names policy that the observed host cannot satisfy, so an owner
	// sees a contradiction on the page that caused it.
	Warnings []string `json:"warnings"`
}

// Reporter builds the status report. Every seam is optional: a report on a host
// without TLS or without an advertiser still describes what it can see.
type Reporter struct {
	Certificates func(context.Context) (networking.CertificateStatus, error)
	Custom       func(context.Context) networking.CustomCertificateStatus
	Advertiser   *Advertiser
	Now          func() time.Time
}

func (r Reporter) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Report observes the host and combines it with the policy.
func (r Reporter) Report(ctx context.Context, policy Policy) Status {
	out := Status{ObservedAt: r.now().UTC().Format(time.RFC3339), Policy: policy, Addresses: []Address{}, Interfaces: []Interface{}, Warnings: []string{}}
	lan := parseNetworks(policy.LANNetworks)
	for _, item := range observeInterfaces() {
		out.Interfaces = append(out.Interfaces, item)
		if !item.Up || item.Loopback {
			continue
		}
		for _, raw := range item.Addresses {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				continue
			}
			out.Addresses = append(out.Addresses, Address{Address: prefix.Addr().String(), Family: family(prefix.Addr()), Class: classify(prefix.Addr(), lan), Source: "interface"})
		}
	}
	for _, raw := range append(append([]string{}, policy.AccessURLs...), policy.AdvertiseURL) {
		if raw == "" {
			continue
		}
		origin, err := url.Parse(raw)
		if err != nil || origin.Hostname() == "" {
			continue
		}
		host := origin.Hostname()
		class, kind := "public", "hostname"
		if addr, err := netip.ParseAddr(host); err == nil {
			class = classify(addr, lan)
			kind = family(addr)
		}
		out.Addresses = append(out.Addresses, Address{Address: host, Family: kind, Class: class, Source: "accessUrl"})
	}
	if r.Certificates != nil {
		if status, err := r.Certificates(ctx); err == nil {
			out.TLS = TLS{Configured: status.Configured, State: status.State, ErrorCode: status.ErrorCode, DNSName: status.DNSName, Issuer: status.Issuer,
				TLSReady: status.TLSReady, PubliclyTrusted: status.PubliclyTrusted, ListenerBound: status.ListenerBound, ListenPort: status.ListenPort,
				RouteURL: status.RouteURL, Reachability: status.Reachability}
			if status.NotAfter != nil {
				out.TLS.NotAfter = status.NotAfter.UTC().Format(time.RFC3339)
			}
		}
	}
	if r.Custom != nil {
		custom := r.Custom(ctx)
		entry := CustomCertificate{Serving: custom.Serving, State: custom.State, ErrorCode: custom.ErrorCode, Domain: custom.Domain, Issuer: custom.Issuer}
		if custom.NotAfter != nil {
			entry.NotAfter = custom.NotAfter.UTC().Format(time.RFC3339)
		}
		if entry.State == "" {
			entry.State = "none"
		}
		out.TLS.Custom = &entry
		if entry.Serving {
			out.TLS.Configured = true
			out.TLS.TLSReady = true
		}
	}
	out.Discovery = r.Advertiser.Status(policy.LANDiscoveryEnabled)
	if policy.RemoteSignIn != "off" && !hasClass(out.Addresses, "public") && out.TLS.RouteURL == "" {
		out.Warnings = append(out.Warnings, "remote_sign_in_without_public_address")
	}
	if policy.LANDiscoveryEnabled && !out.Discovery.Supported {
		out.Warnings = append(out.Warnings, "lan_discovery_unsupported_on_this_host")
	}
	return out
}

func hasClass(addresses []Address, class string) bool {
	for _, a := range addresses {
		if a.Class == class {
			return true
		}
	}
	return false
}

func family(addr netip.Addr) string {
	if addr.Is4() || addr.Is4In6() {
		return "ipv4"
	}
	return "ipv6"
}

func parseNetworks(raw []string) []netip.Prefix {
	out := []netip.Prefix{}
	for _, v := range raw {
		if prefix, err := netip.ParsePrefix(v); err == nil {
			out = append(out, prefix)
		}
	}
	return out
}

// classify places an address. A configured LAN list is authoritative when it
// matches; otherwise private and link-local addresses are LAN and globally
// routable ones are public.
func classify(addr netip.Addr, lan []netip.Prefix) string {
	if networkpolicy.Contains(lan, addr) {
		if addr.IsLoopback() {
			return "loopback"
		}
		return "lan"
	}
	return "public"
}

// observeInterfaces enumerates interfaces portably. net.Interfaces is available
// on every target this server builds for; a host that refuses the enumeration
// (a container without CAP_NET_ADMIN, a restricted service account on Windows)
// yields an empty list rather than an error, because a status report that cannot
// see the network is still worth showing.
func observeInterfaces() []Interface {
	raw, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := []Interface{}
	for _, item := range raw {
		entry := Interface{Name: item.Name, Up: item.Flags&net.FlagUp != 0, Loopback: item.Flags&net.FlagLoopback != 0, Virtual: networking.VirtualInterface(item.Name), Addresses: []string{}}
		addresses, err := item.Addrs()
		if err == nil {
			for _, address := range addresses {
				if network, ok := address.(*net.IPNet); ok {
					ones, _ := network.Mask.Size()
					if addr, ok := netip.AddrFromSlice(network.IP); ok {
						entry.Addresses = append(entry.Addresses, netip.PrefixFrom(addr.Unmap(), ones).String())
					}
				}
			}
		}
		sort.Strings(entry.Addresses)
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
