package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Which addresses are home, and which reverse proxies to believe. The owner's list of home
// networks is the only thing that makes a connection local or remote: it decides who is
// never blocked for wrong guesses and what "Home network" and "Away from home" mean in the
// device list. Setting a server up, and signing in, work from any network.

// NetworkSettings is the owner's network settings.
type NetworkSettings struct {
	// HomeNetworks are the addresses and networks (such as 192.168.1.0/24) that count as
	// home. An empty list makes every connection away from home.
	HomeNetworks []string `json:"homeNetworks"`
	// HomeNetworksDefault is true while the owner has not chosen, and HomeNetworks is then
	// what the server uses by itself: this machine and the private networks it is attached to.
	HomeNetworksDefault bool `json:"homeNetworksDefault"`
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header is believed. The
	// header from any other address is ignored.
	TrustedProxies []string `json:"trustedProxies"`

	revision
}

// NetworkSettingsEdit replaces the network settings. HomeNetworks left out or null goes
// back to the server's own choice.
type NetworkSettingsEdit struct {
	HomeNetworks   *[]string `json:"homeNetworks,omitempty"`
	TrustedProxies []string  `json:"trustedProxies"`
}

const (
	networkSettingsKey = "network"
	maxNetworks        = 64
	// defaultHomeFor is how long the server's own list of attached networks is reused.
	defaultHomeFor = time.Minute
)

// storedNetworks is the settings document. Home is nil until the owner chooses.
type storedNetworks struct {
	Home    *[]string `json:"home,omitempty"`
	Proxies []string  `json:"proxies"`
}

// networkState is the settings as requests use them, kept in memory.
type networkState struct {
	mu          sync.Mutex
	stored      storedNetworks
	home        []netip.Prefix // the owner's choice, when there is one
	proxies     []netip.Prefix
	defaults    []netip.Prefix
	defaultsAt  time.Time
	warnedProxy bool
}

func (n *networkState) set(stored storedNetworks) error {
	var home []netip.Prefix
	var err error
	if stored.Home != nil {
		if home, err = auth.ParseNetworks(*stored.Home); err != nil {
			return err
		}
	}
	proxies, err := auth.ParseNetworks(stored.Proxies)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stored, n.home, n.proxies = stored, home, proxies
	return nil
}

func (n *networkState) current(now time.Time) (auth.Networks, storedNetworks) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stored.Home != nil {
		return auth.Networks{Home: n.home, Proxies: n.proxies}, n.stored
	}
	// The machine's own networks can change while the server runs: a laptop, a new lease.
	if n.defaults == nil || now.Sub(n.defaultsAt) > defaultHomeFor {
		n.defaults, n.defaultsAt = auth.DefaultHome(), now
	}
	return auth.Networks{Home: n.defaults, Proxies: n.proxies}, n.stored
}

func (s *Server) networks() auth.Networks {
	networks, _ := s.network.current(s.now())
	return networks
}

func (s *Server) loadNetworks(ctx context.Context) error {
	raw, _, err := s.db.SettingDocument(ctx, networkSettingsKey)
	if err != nil || raw == "" {
		return err
	}
	var stored storedNetworks
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return errors.New("the stored network settings are damaged")
	}
	return s.network.set(stored)
}

// noteForwarded says once in the log that requests are arriving with a forwarded address
// from a proxy the owner has not listed. Until they list it, nobody behind that proxy
// counts as at home, and everyone behind it counts as the proxy's own address, so one
// person's wrong guesses count against all of them.
func (s *Server) noteForwarded(r *http.Request) {
	if r.Header.Get("X-Forwarded-For") == "" {
		return
	}
	networks := s.networks()
	if addr, ok := networks.Client(r); !ok || addr.String() != peerAddr(r) {
		return
	}
	s.network.mu.Lock()
	warned := s.network.warnedProxy
	s.network.warnedProxy = true
	s.network.mu.Unlock()
	if !warned {
		s.log.Warn("a request came through a proxy that is not in the trusted proxies, so its X-Forwarded-For was ignored "+
			"and it does not count as coming from home: add the proxy's address in the network settings if it is yours", "proxy", peerAddr(r))
	}
}

func peerAddr(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return ap.Addr().Unmap().WithZone("").String()
}

func (s *Server) networkRoutes() {
	Handle(s, "GET /v1/admin/settings/network", Owner, func(r *http.Request, _ None) (NetworkSettings, error) {
		_, saved, err := s.db.SettingDocument(r.Context(), networkSettingsKey)
		return s.networkSettings(saved), err
	})
	Handle(s, "PUT /v1/admin/settings/network", Owner, s.saveNetworkSettings)
}

func prefixStrings(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.String())
	}
	return out
}

func (s *Server) networkSettings(saved int64) NetworkSettings {
	networks, stored := s.network.current(s.now())
	return NetworkSettings{HomeNetworks: prefixStrings(networks.Home), HomeNetworksDefault: stored.Home == nil,
		TrustedProxies: prefixStrings(networks.Proxies), revision: revision(saved)}
}

func (s *Server) saveNetworkSettings(r *http.Request, req NetworkSettingsEdit) (NetworkSettings, error) {
	stored := storedNetworks{Home: req.HomeNetworks, Proxies: req.TrustedProxies}
	if stored.Proxies == nil {
		stored.Proxies = []string{}
	}
	if req.HomeNetworks != nil {
		if home, err := auth.ParseNetworks(*req.HomeNetworks); err != nil || len(home) > maxNetworks {
			return NetworkSettings{}, ErrField("homeNetworks", "List up to 64 addresses or networks such as 192.168.1.0/24.")
		}
	}
	proxies, err := auth.ParseNetworks(stored.Proxies)
	if err != nil || len(proxies) > maxNetworks {
		return NetworkSettings{}, ErrField("trustedProxies", "List up to 64 addresses or networks such as 10.0.0.2 or 172.18.0.0/16.")
	}
	for _, p := range proxies {
		// Believing a forwarded address from everywhere would let anyone claim to be at home.
		if p.Bits() < 8 {
			return NetworkSettings{}, ErrField("trustedProxies", "A trusted proxy is your own proxy's address or network, not most of the internet.")
		}
	}
	match, err := settingsIfMatch(r)
	if err != nil {
		return NetworkSettings{}, err
	}
	raw, _ := json.Marshal(stored)
	saved, err := s.db.SaveSettingDocument(r.Context(), CallerFrom(r.Context()).UserID, networkSettingsKey, string(raw), match, s.now())
	if errors.Is(err, db.ErrStale) {
		return NetworkSettings{}, errStale()
	}
	if err != nil {
		return NetworkSettings{}, err
	}
	if err := s.network.set(stored); err != nil {
		return NetworkSettings{}, err
	}
	return s.networkSettings(saved), nil
}

// An owner settings document can be open on two of the owner's devices. Its ETag is how
// many times it has been saved; a save that sends the ETag it read as If-Match is refused
// if the document has been saved since, and a save without If-Match replaces what is there.

// revision is embedded in a settings answer to give it its ETag.
type revision int64

func (v revision) reply(h http.Header) int {
	h.Set("ETag", `"`+strconv.FormatInt(int64(v), 10)+`"`)
	return 0
}

func errStale() *Error {
	return refusal(http.StatusPreconditionFailed, "changed_elsewhere", "These settings were changed somewhere else. Read them again.", false)
}

func settingsIfMatch(r *http.Request) (int64, error) {
	raw := r.Header.Get("If-Match")
	if raw == "" || raw == "*" {
		return -1, nil
	}
	revision, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64)
	if err != nil || revision < 0 {
		return 0, errStale()
	}
	return revision, nil
}
