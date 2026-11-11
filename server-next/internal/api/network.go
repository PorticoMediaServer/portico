package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
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
	// Warnings are what the owner should know about these settings, each a code and a plain
	// sentence, for as long as it holds. It is empty when nothing is wrong, and is not part of
	// what is saved. Three codes so far:
	//   - proxy_everywhere: the trusted proxies cover most of the internet, so anyone can say
	//     they are at home. The owner's choice: it is saved, and said.
	//   - proxy_without_header: requests came from a trusted proxy's address with no
	//     X-Forwarded-For in the last day, so they count as away. One for each such address,
	//     naming it.
	//   - proxy_not_listed: requests carrying X-Forwarded-For came in the last day from an
	//     address on this machine or a private network that is not a trusted proxy, so
	//     everyone through it is one visitor. One for each such address, naming it.
	Warnings []Warning `json:"warnings"`

	revision
}

// Warning is one risky choice in force.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// proxyEverywhereMessage is proposed wording, listed for the owner's approval.
const proxyEverywhereMessage = "A trusted proxy covers most of the internet, so anyone can say they are at home " +
	"and skip what applies to people away from home. List only your own proxy's address unless you mean this."

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
	mu         sync.Mutex
	stored     storedNetworks
	home       []netip.Prefix // the owner's choice, when there is one
	proxies    []netip.Prefix
	defaults   []netip.Prefix
	defaultsAt time.Time
	// silent is when each trusted proxy last sent a request with no visitor's address.
	silent map[string]time.Time
	// unlisted is when each address on this machine or a private network that is not a
	// trusted proxy last sent a request carrying X-Forwarded-For.
	unlisted map[string]time.Time
}

const (
	// silentProxyFor is how long a proxy stays in the owner's warnings after it last sent a
	// request with no visitor's address: long enough to be seen, gone once it is fixed.
	silentProxyFor   = 24 * time.Hour
	maxSilentProxies = 16
)

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
	// A saved change starts the warnings afresh: the proxy named may no longer be listed.
	n.stored, n.home, n.proxies, n.silent, n.unlisted = stored, home, proxies, nil, nil
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

// fromHome reports whether a request came from home, and notes a trusted proxy that passed
// on no visitor's address: its visitors all count as away, and only the owner can fix that.
func (s *Server) fromHome(r *http.Request) bool {
	networks := s.networks()
	if networks.ProxyGaveNoAddress(r) {
		s.noteSilentProxy(peerAddr(r))
	}
	if networks.UnlistedLocalProxy(r) {
		s.noteUnlistedProxy(peerAddr(r))
	}
	return networks.FromHome(r)
}

// unlistedProxyNotice is the one sentence the owner's network settings and the log both use.
func unlistedProxyNotice(proxy string) string {
	return "Requests are arriving through a proxy at " + proxy + " that is not listed as trusted, so everyone who comes through it is treated as one visitor: " +
		"they all count as away from home, and wrong passwords from any one of them can block sign-in for all of them. " +
		"If it is your proxy, add " + proxy + " to Trusted proxies."
}

// noteUnlistedProxy remembers, for the owner's warnings, an address that is behaving as a
// proxy nobody listed. Like noteSilentProxy it keeps at most sixteen and logs each once.
func (s *Server) noteUnlistedProxy(proxy string) {
	now := s.now()
	s.network.mu.Lock()
	_, known := s.network.unlisted[proxy]
	if s.network.unlisted == nil {
		s.network.unlisted = map[string]time.Time{}
	}
	added := known || len(s.network.unlisted) < maxSilentProxies
	if added {
		s.network.unlisted[proxy] = now
	}
	s.network.mu.Unlock()
	if !known && added {
		s.log.Warn(unlistedProxyNotice(proxy), "proxy", proxy)
	}
}

// blockedNetworkMessage is what a person is told when their network is blocked for wrong
// guesses, in place of the plain "wait" of their own wrong tries: the wait may be long, it
// may not be their doing, and there is someone who can end it.
const blockedNetworkMessage = "Too many wrong sign-in attempts have come from your network, so sign-in from it is blocked for a while. " +
	"Wait and try again, or ask the person who runs this server to unblock it."

// tellBlocked gives a "too many attempts" refusal the sentence above when what refuses the
// request is the block on its whole network and not the person's own short wait.
func (s *Server) tellBlocked(r *http.Request, e *Error) {
	if e.Status != http.StatusTooManyRequests || (e.Code != "rate_limited" && e.Code != "profile_pin_locked") {
		return
	}
	limits := s.sourceLimit(r)
	if e.Code == "profile_pin_locked" {
		limits = s.pinSourceLimit(r)
	}
	if wait, err := s.db.LimitWait(r.Context(), s.now(), limits...); err == nil && wait > 0 {
		e.Message = blockedNetworkMessage
	}
}

// silentProxyNotice is the one sentence the owner's network settings and the log both use.
func silentProxyNotice(proxy string) string {
	return "Requests are arriving from the trusted proxy address " + proxy + " with no X-Forwarded-For, so they count as away from home. " +
		"If they come through your proxy, set it to add X-Forwarded-For to every request: until it does, your own devices count as away, " +
		"and a visitor who sends that header themselves can count as at home."
}

func (s *Server) noteSilentProxy(proxy string) {
	now := s.now()
	s.network.mu.Lock()
	_, known := s.network.silent[proxy]
	if s.network.silent == nil {
		s.network.silent = map[string]time.Time{}
	}
	// A full set takes no new address, and says nothing about it either: with a wide
	// trusted range anyone could otherwise add a line to the log with every request.
	added := known || len(s.network.silent) < maxSilentProxies
	if added {
		s.network.silent[proxy] = now
	}
	s.network.mu.Unlock()
	if !known && added {
		s.log.Warn(silentProxyNotice(proxy), "proxy", proxy)
	}
}

// networkWarnings are what the owner's network settings warn about, the wide proxy list first,
// then each trusted proxy address seen sending no X-Forwarded-For in the last day.
func (s *Server) networkWarnings(networks auth.Networks) []Warning {
	out := []Warning{}
	// Believing a forwarded address from most of the internet lets anyone claim to be at home.
	// It is the owner's choice about their own server, so it is saved, and said plainly.
	for _, p := range networks.Proxies {
		if p.Bits() < 8 {
			out = append(out, Warning{Code: "proxy_everywhere", Message: proxyEverywhereMessage})
			break
		}
	}
	now := s.now()
	var silent []string
	s.network.mu.Lock()
	for proxy, at := range s.network.silent {
		if now.Sub(at) > silentProxyFor {
			delete(s.network.silent, proxy)
			continue
		}
		silent = append(silent, proxy)
	}
	var unlisted []string
	for proxy, at := range s.network.unlisted {
		if now.Sub(at) > silentProxyFor {
			delete(s.network.unlisted, proxy)
			continue
		}
		unlisted = append(unlisted, proxy)
	}
	s.network.mu.Unlock()
	slices.Sort(silent)
	for _, proxy := range silent {
		out = append(out, Warning{Code: "proxy_without_header", Message: silentProxyNotice(proxy)})
	}
	slices.Sort(unlisted)
	for _, proxy := range unlisted {
		out = append(out, Warning{Code: "proxy_not_listed", Message: unlistedProxyNotice(proxy)})
	}
	return out
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
	Handle(s, "GET /v1/admin/blocked-networks", Admin, s.blockedNetworks)
	Handle(s, "DELETE /v1/admin/blocked-networks/{id}", Admin, s.unblockNetwork)
}

// BlockedNetwork is a network away from home that sent so many wrong passwords, PINs or
// codes that every sign-in from it is refused until BlockedUntil.
type BlockedNetwork struct {
	ID string `json:"id"`
	// Network is an IPv4 address, or the /64 network of an IPv6 address.
	Network      string    `json:"network"`
	BlockedUntil time.Time `json:"blockedUntil"`
}

func (s *Server) blockedNetworks(r *http.Request, _ None) (Page[BlockedNetwork], error) {
	blocked, err := s.db.BlockedNetworks(r.Context(), s.now())
	out := Page[BlockedNetwork]{Items: make([]BlockedNetwork, 0, len(blocked))}
	for _, b := range blocked {
		out.Items = append(out.Items, BlockedNetwork{ID: b.ID, Network: b.Network, BlockedUntil: b.Until.UTC()})
	}
	return out, err
}

// unblockNetwork lets a blocked network try again at once. Its people still wait out any
// wait of their own for wrong passwords, which is a minute at most on Standard.
func (s *Server) unblockNetwork(r *http.Request, _ None) (None, error) {
	err := s.db.UnblockNetwork(r.Context(), CallerFrom(r.Context()).UserID, r.PathValue("id"), s.now())
	if errors.Is(err, db.ErrNotFound) {
		return None{}, ErrNotFound()
	}
	return None{}, err
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
		TrustedProxies: prefixStrings(networks.Proxies), Warnings: s.networkWarnings(networks), revision: revision(saved)}
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
