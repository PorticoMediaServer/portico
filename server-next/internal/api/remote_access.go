package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/portmap"
	"github.com/PorticoMediaServer/portico/server-next/internal/tlsport"
)

// Remote access: what this server answers from outside the owner's home networks. The home
// networks (network.go) are the only definition of home, and nothing here ever changes what
// works at home: plain HTTP, any address, anyone with an account.
//
// From outside, in this order:
//   - Remote access Off refuses everything.
//   - Secure connections Required, or Remote security Enhanced, refuses plain HTTP.
//   - Enhanced answers only at the addresses the owner approved (their own domain and the
//     addresses they added), each a host and a port.
//   - "Only the owner" refuses everyone else's sign-in and every request they make.
//
// The settings are one document, kept in memory and read by every request.

// RemoteAccessSettings is the owner's remote access document.
type RemoteAccessSettings struct {
	// RemoteAccess is "on" or "off". Off refuses every connection from outside the home
	// networks; a VPN counts as home once its range is in the home networks.
	RemoteAccess string `json:"remoteAccess"`
	// RemoteSecurity is "standard" or "enhanced". Enhanced answers outside connections only
	// over HTTPS and only at the approved addresses, so it includes Secure connections Required.
	RemoteSecurity string `json:"remoteSecurity"`
	// SecureConnections is "preferred" or "required". Required refuses plain HTTP from
	// outside the home networks only.
	SecureConnections string `json:"secureConnections"`
	// SignInFromOutside is "anyone" (anyone with an account) or "owner".
	SignInFromOutside string `json:"signInFromOutside"`
	// PortMapping is "automatic" (ask the home router to open PublicPort) or "off".
	PortMapping string `json:"portMapping"`
	// PublicPort is the port people outside connect to.
	PublicPort int `json:"publicPort"`
	// IPv6 is meant to let connections in over IPv6 too. Nothing reads it yet: no IPv6 pinhole
	// is asked of the router, so IPv6 from outside is neither opened nor closed by it.
	IPv6 bool `json:"ipv6"`
	// OwnAddress is the owner's own domain, with a certificate for it or a reverse proxy in front.
	OwnAddress OwnAddress `json:"ownAddress"`
	// AllowedAddresses are more addresses Enhanced answers at: "host" (port 443) or "host:port".
	AllowedAddresses []string `json:"allowedAddresses"`
	// FindAtHome lets apps on the home network list this server without typing an address.
	FindAtHome bool `json:"findAtHome"`
	// CertificateWarningDays is how many days before the own certificate expires the owner
	// is warned: 7, 14, 30 or 60.
	CertificateWarningDays int `json:"certificateWarningDays"`

	revision
}

// OwnAddress is "Use your own address". With certificate files the server serves HTTPS for the
// domain itself; without them a reverse proxy in front of it does.
type OwnAddress struct {
	Enabled bool   `json:"enabled"`
	Domain  string `json:"domain"`
	// CertificateFile and KeyFile are the names of PEM files in the Keys folder of the server's
	// data folder. Empty means a reverse proxy serves HTTPS for the domain.
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

const (
	remoteAccessKey   = "remote-access"
	defaultPublicPort = 32500
	maxAllowed        = 64
	// maxRefused bounds the outside addresses Enhanced has refused, kept since start.
	maxRefused = 50
)

func defaultRemoteAccess() RemoteAccessSettings {
	return RemoteAccessSettings{RemoteAccess: "on", RemoteSecurity: "standard", SecureConnections: "preferred",
		SignInFromOutside: "anyone", PortMapping: "automatic", PublicPort: defaultPublicPort, AllowedAddresses: []string{}, FindAtHome: true,
		CertificateWarningDays: 14}
}

// problem names the first field holding a value it does not take, with what to say.
func (v *RemoteAccessSettings) problem() (string, string) {
	choice := "That is not one of this setting's choices."
	switch {
	case v.RemoteAccess != "on" && v.RemoteAccess != "off":
		return "remoteAccess", choice
	case v.RemoteSecurity != "standard" && v.RemoteSecurity != "enhanced":
		return "remoteSecurity", choice
	case v.SecureConnections != "preferred" && v.SecureConnections != "required":
		return "secureConnections", choice
	case v.SignInFromOutside != "anyone" && v.SignInFromOutside != "owner":
		return "signInFromOutside", choice
	case v.PortMapping != "automatic" && v.PortMapping != "off":
		return "portMapping", choice
	case v.PublicPort < 1 || v.PublicPort > 65535:
		return "publicPort", "Choose a port from 1 to 65535."
	case v.OwnAddress.Enabled && !validHost(v.OwnAddress.Domain):
		return "ownAddress.domain", "Enter the domain alone, such as media.example.com."
	case (v.OwnAddress.CertificateFile == "") != (v.OwnAddress.KeyFile == ""):
		return "ownAddress.keyFile", "Give both the certificate file and its key file, or neither for a reverse proxy."
	case v.OwnAddress.CertificateFile != "" && !keysFileName(v.OwnAddress.CertificateFile):
		return "ownAddress.certificateFile", "Give the name of a file in the Keys folder, such as certificate.pem."
	case v.OwnAddress.KeyFile != "" && !keysFileName(v.OwnAddress.KeyFile):
		return "ownAddress.keyFile", "Give the name of a file in the Keys folder, such as certificate.key."
	case !slices.Contains([]int{7, 14, 30, 60}, v.CertificateWarningDays):
		return "certificateWarningDays", choice
	case len(v.AllowedAddresses) > maxAllowed:
		return "allowedAddresses", "List up to 64 addresses."
	}
	for i, a := range v.AllowedAddresses {
		if _, ok := hostPort(a, 443); !ok {
			return "allowedAddresses", "Address " + strconv.Itoa(i+1) + " is not a host or a host and port, such as media.example.com:8443."
		}
	}
	return "", ""
}

// validHost reports whether s is a host name or an IP address, with no port or path.
func validHost(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, "/\\:@ ") && net.ParseIP(s) == nil {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// keysFileName reports whether name is a plain file name, which is all the owner may give:
// the certificate files are always read from the Keys folder, never from a path in a request.
func keysFileName(name string) bool {
	return len(name) <= 255 && name != "" && !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "/\\\x00")
}

// hostPort reads "host" or "host:port" (IPv6 in brackets) into one lower-case "host:port".
func hostPort(s string, defaultPort int) (string, bool) {
	host, port := strings.ToLower(strings.TrimSpace(s)), strconv.Itoa(defaultPort)
	if h, p, err := net.SplitHostPort(host); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", false
		}
		host, port = h, p
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if !validHost(host) {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

// remoteState is the document as requests use it, and what Enhanced has refused since start.
type remoteState struct {
	mu      sync.RWMutex
	value   RemoteAccessSettings
	allowed []string // the approved host:port pairs, the own domain included
	refused map[string]RefusedAddress
	// listenPort is the port the server listens on, the one the router forwards to.
	listenPort int
}

// RefusedAddress is an outside address Enhanced refused, and what would fix it.
type RefusedAddress struct {
	// Address is the host and port the person used.
	Address string `json:"address"`
	// Reason is "http" (a plain HTTP connection) or "address" (not an approved address).
	Reason string `json:"reason"`
	// Fixes are what the owner can do: "allow" (Allow this address), "certificate" (Add
	// certificate) and "standard" (Use Standard).
	Fixes   []string  `json:"fixes"`
	LastAt  time.Time `json:"lastAt"`
	Refused int       `json:"refused"`
}

func (st *remoteState) set(v RemoteAccessSettings) {
	var allowed []string
	for _, a := range v.AllowedAddresses {
		hp, _ := hostPort(a, 443)
		allowed = append(allowed, hp)
	}
	if v.OwnAddress.Enabled {
		for _, port := range []int{443, v.PublicPort} {
			hp, _ := hostPort(v.OwnAddress.Domain, port)
			allowed = append(allowed, hp)
		}
	}
	st.mu.Lock()
	st.value, st.allowed = v, allowed
	st.mu.Unlock()
}

// applyCertificate serves the owner's certificate for their own address, if they gave one.
func (s *Server) applyCertificate(v RemoteAccessSettings) {
	src := tlsport.Source{WarnBefore: time.Duration(v.CertificateWarningDays) * 24 * time.Hour}
	if v.OwnAddress.Enabled && v.OwnAddress.CertificateFile != "" {
		src.Domain, src.CertificateFile, src.KeyFile = v.OwnAddress.Domain, v.OwnAddress.CertificateFile, v.OwnAddress.KeyFile
	}
	s.certs.Use(src)
	// The router is asked nothing until the server has an owner: an unclaimed server never puts
	// its setup page on the internet by itself.
	owned, err := s.db.HasUsers(context.Background())
	if err != nil {
		s.log.Error("could not tell whether this server has an owner", "error", err)
	}
	s.ports.Set(owned && v.RemoteAccess == "on" && v.PortMapping == "automatic", s.listenPort(), v.PublicPort)
}

// ServePortMapping keeps the server's port open on the home router while the owner's settings
// ask for it, until ctx ends. port is the port the server listens on, or 0 when nothing
// outside this machine can reach it, and the router is then never asked.
func (s *Server) ServePortMapping(ctx context.Context, port int) {
	s.remote.mu.Lock()
	s.remote.listenPort = port
	s.remote.mu.Unlock()
	v, _ := s.remote.current()
	s.applyCertificate(v)
	s.ports.Run(ctx)
}

func (s *Server) listenPort() int {
	s.remote.mu.RLock()
	defer s.remote.mu.RUnlock()
	return s.remote.listenPort
}

// Certificates is the owner's certificate as the listener serves it.
func (s *Server) Certificates() *tlsport.Store { return s.certs }

func (st *remoteState) current() (RemoteAccessSettings, []string) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.value, st.allowed
}

func (st *remoteState) noteRefused(address, reason string, fixes []string, now time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.refused == nil {
		st.refused = map[string]RefusedAddress{}
	}
	key := reason + " " + address
	r, seen := st.refused[key]
	if !seen && len(st.refused) >= maxRefused {
		return
	}
	r.Address, r.Reason, r.Fixes, r.LastAt = address, reason, fixes, now.UTC()
	r.Refused++
	st.refused[key] = r
}

func (st *remoteState) refusedList() []RefusedAddress {
	st.mu.RLock()
	out := make([]RefusedAddress, 0, len(st.refused))
	for _, r := range st.refused {
		out = append(out, r)
	}
	st.mu.RUnlock()
	slices.SortFunc(out, func(a, b RefusedAddress) int { return b.LastAt.Compare(a.LastAt) })
	return out
}

func (s *Server) loadRemoteAccess(ctx context.Context) error {
	v := defaultRemoteAccess()
	raw, _, err := s.db.SettingDocument(ctx, remoteAccessKey)
	if err != nil {
		return err
	}
	// A field a newer version added and this one's document lacks keeps its default.
	if raw != "" {
		if json.Unmarshal([]byte(raw), &v) != nil {
			return errors.New("the stored remote access settings are damaged")
		}
		if field, _ := v.problem(); field != "" {
			return errors.New("the stored remote access settings are damaged at " + field)
		}
	}
	s.remote.set(v)
	s.applyCertificate(v)
	return nil
}

// RemoteAccessStatus is what is true now, for the owner's Remote access page.
type RemoteAccessStatus struct {
	// ThisDevice is how the server sees the device asking: at home or away, and its address.
	ThisDevice ThisDevice `json:"thisDevice"`
	// Refused are the outside addresses Enhanced refused since the server started, newest first.
	Refused []RefusedAddress `json:"refused"`
	// Certificate is the owner's own certificate: none, served, or what is wrong with it.
	Certificate tlsport.Status `json:"certificate"`
	// Router is what the home router last said about opening the public port.
	Router portmap.Status `json:"router"`
}

// ThisDevice is the asking device as the server sees it.
type ThisDevice struct {
	AtHome bool `json:"atHome"`
	// Address is the address the request came from; empty when it came through a proxy the
	// owner has not listed, whose own address says nothing about the device.
	Address string `json:"address"`
}

func (s *Server) remoteAccessRoutes() {
	Handle(s, "GET /v1/admin/settings/remote-access", Owner, func(r *http.Request, _ None) (RemoteAccessSettings, error) {
		_, saved, err := s.db.SettingDocument(r.Context(), remoteAccessKey)
		v, _ := s.remote.current()
		v.revision = revision(saved)
		return v, err
	})
	Handle(s, "PUT /v1/admin/settings/remote-access", Owner, s.saveRemoteAccess)
	Handle(s, "GET /v1/admin/remote-access", Owner, func(r *http.Request, _ None) (RemoteAccessStatus, error) {
		return s.remoteStatus(r), nil
	})
	// Check now, and Try again: ask the router again and read the certificate files again.
	Handle(s, "POST /v1/admin/remote-access/check", Owner, func(r *http.Request, _ None) (RemoteAccessStatus, error) {
		s.certs.Check()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		s.ports.CheckNow(ctx)
		return s.remoteStatus(r), nil
	})
}

func (s *Server) remoteStatus(r *http.Request) RemoteAccessStatus {
	return RemoteAccessStatus{ThisDevice: ThisDevice{AtHome: s.networks().FromHome(r), Address: s.remoteHost(r)},
		Refused: s.remote.refusedList(), Certificate: s.certs.Status(), Router: s.ports.Status()}
}

// saveRemoteAccess replaces the whole document. Saving it from outside the home networks can
// shut the owner out of what they are using; the app warns before it sends that.
func (s *Server) saveRemoteAccess(r *http.Request, req RemoteAccessSettings) (RemoteAccessSettings, error) {
	if req.AllowedAddresses == nil {
		req.AllowedAddresses = []string{}
	}
	req.OwnAddress.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.OwnAddress.Domain), "."))
	if field, message := req.problem(); field != "" {
		return RemoteAccessSettings{}, ErrField(field, message)
	}
	match, err := settingsIfMatch(r)
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	req.revision = 0
	raw, _ := json.Marshal(req)
	saved, err := s.db.SaveSettingDocument(r.Context(), CallerFrom(r.Context()).UserID, remoteAccessKey, string(raw), match, s.now())
	if errors.Is(err, db.ErrStale) {
		return RemoteAccessSettings{}, errStale()
	}
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	s.remote.set(req)
	s.applyCertificate(req)
	req.revision = revision(saved)
	return req, nil
}

// fromTrustedProxy reports whether the connection itself is one of the owner's trusted proxies,
// whose forwarded scheme and host are then believed.
func (s *Server) fromTrustedProxy(r *http.Request) bool {
	peer, err := netip.ParseAddr(peerAddr(r))
	if err != nil {
		return false
	}
	for _, p := range s.networks().Proxies {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

// secure reports whether the person's connection is HTTPS: this server's own, or a trusted
// reverse proxy's that says so.
func (s *Server) secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.fromTrustedProxy(r) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// requestAddress is the host and port the person used, as "host:port".
func (s *Server) requestAddress(r *http.Request, secure bool) string {
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" && s.fromTrustedProxy(r) {
		host, _, _ = strings.Cut(forwarded, ",")
	}
	port := 80
	if secure {
		port = 443
	}
	if hp, ok := hostPort(host, port); ok {
		return hp
	}
	return strings.ToLower(host)
}

// outsideRefusal is the refusal for a request from outside the home networks that the owner's
// remote access settings do not allow, or nil. Requests from home are never refused here.
func (s *Server) outsideRefusal(r *http.Request) *Error {
	v, allowed := s.remote.current()
	if v.RemoteAccess == "on" && v.RemoteSecurity == "standard" && v.SecureConnections == "preferred" {
		return nil
	}
	if s.networks().FromHome(r) {
		return nil
	}
	if v.RemoteAccess == "off" {
		return refusal(http.StatusForbidden, "remote_access_off", "This server can be used only from its home network.", false)
	}
	secure := s.secure(r)
	enhanced := v.RemoteSecurity == "enhanced"
	if !secure {
		if enhanced {
			fixes := []string{"certificate", "standard"}
			s.remote.noteRefused(s.requestAddress(r, false), "http", fixes, s.now())
		}
		return refusal(http.StatusForbidden, "secure_connection_required",
			"This server takes connections from outside its home network only over a secure connection (https).", false)
	}
	if address := s.requestAddress(r, true); enhanced && !slices.Contains(allowed, address) {
		s.remote.noteRefused(address, "address", []string{"allow", "standard"}, s.now())
		return refusal(http.StatusForbidden, "address_not_allowed", "This server does not answer at this address from outside its home network.", false)
	}
	return nil
}

// ownerOnlyAway reports whether this request comes from outside the home networks while only
// the owner may sign in from there.
func (s *Server) ownerOnlyAway(r *http.Request) bool {
	v, _ := s.remote.current()
	return v.SignInFromOutside == "owner" && !s.networks().FromHome(r)
}

func errOwnerOnlyAway() *Error {
	return refusal(http.StatusForbidden, "owner_only_away", "Only the server's owner can use it from outside its home network.", false)
}

// refuseNonOwnerAway refuses an account other than the owner's while only the owner may use
// the server from outside the home networks.
func (s *Server) refuseNonOwnerAway(r *http.Request, userID int64) error {
	if userID == 0 || !s.ownerOnlyAway(r) {
		return nil
	}
	account, err := s.db.Account(r.Context(), userID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}
	if account.Role != "owner" {
		return errOwnerOnlyAway()
	}
	return nil
}
