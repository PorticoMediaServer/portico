package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/discovery"
	"github.com/PorticoMediaServer/portico/server-next/internal/portmap"
	"github.com/PorticoMediaServer/portico/server-next/internal/publicaddr"
	"github.com/PorticoMediaServer/portico/server-next/internal/tlsport"
)

// Remote access: what this server answers from outside the owner's home networks. The home
// networks (network.go) are the only definition of home, and nothing here ever changes what
// works at home: plain HTTP, any address, anyone with an account.
//
// From outside, in this order:
//   - Remote access Off refuses everything.
//   - Secure connections Required, or Remote security Enhanced, refuses plain HTTP.
//   - Enhanced answers only at the approved addresses (the Portico address, the owner's own
//     domain and the addresses they added), each a host and a port.
//
// Enhanced is the default, but it is active only once first-run setup has finished and there
// is at least one approved address. Before that it works like Standard and the owner is told
// why, so accepting the default can never refuse everything from outside. Any save that would
// refuse the connection the owner is using right now is refused unless they confirm it, and
// can approve that connection's address in the same save.
//
// An owner who is shut out anyway (remote access Off, Enhanced or Required with no way in,
// home networks that no longer hold where they are) creates an empty file named
// reset-remote-access in the data folder and restarts the server: the remote access and
// network settings go back to safe defaults (remote access On, Standard, Secure connections
// Preferred, the server's own home networks) and the file is removed.
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
	// EnhancedWithoutAddress is the owner's explicit choice to keep Enhanced in force with no
	// address it can allow, which refuses every connection from outside the home networks.
	// Off, Enhanced with no address waits and works as Standard; the default never turns it on.
	EnhancedWithoutAddress bool `json:"enhancedWithoutAddress"`
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
	return RemoteAccessSettings{RemoteAccess: "on", RemoteSecurity: "enhanced", SecureConnections: "preferred",
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
	// finished says first-run setup has finished, so Enhanced may take effect.
	finished bool
	// portico is the Portico address, approved under Enhanced like the owner's own, and
	// porticoReady says its certificate has been issued. Until it has, Enhanced waits and the
	// server works as Standard; porticoProblem says why it has not, when something failed.
	// Nothing sets them yet: they come with Portico Services (internal/hosted).
	portico        string
	porticoReady   bool
	porticoProblem string
	// ownCertificateUsable says the certificate files the owner named for their own address
	// are loaded and valid for it. Until the server serves them (internal/tlsport), it is nil
	// and they count as not usable. A reverse proxy at the own address (no files) needs none.
	ownCertificateUsable func() bool
	// listenPort is the port the server listens on, the one the router forwards to.
	listenPort int
	// finder announces the server to apps at home while Find this server at home is on.
	finder *discovery.Announcer
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

func (st *remoteState) set(v RemoteAccessSettings, finished bool) {
	st.mu.Lock()
	st.value, st.allowed, st.finished = v, approved(v, st.portico), finished
	st.mu.Unlock()
}

// approved are the host:port pairs Enhanced answers at under v.
func approved(v RemoteAccessSettings, portico string) []string {
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
	if hp, ok := hostPort(portico, 443); portico != "" && ok {
		allowed = append(allowed, hp)
	}
	return allowed
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
	// The public address is asked of outside services under the same conditions, mapping or not.
	s.public.Set(owned && v.RemoteAccess == "on" && s.listenPort() != 0)
	s.access.mu.RLock()
	finder := s.access.finder
	s.access.mu.RUnlock()
	if finder != nil {
		finder.Set(v.FindAtHome)
	}
}

// ServeNetwork announces the server to apps at home, keeps its port open on the home router
// and finds its public address, each while the owner's settings ask for it, until ctx ends.
// port is the port the server listens on, or 0 when nothing outside this machine can reach it:
// then nothing is announced, the router is never asked and no outside service either.
func (s *Server) ServeNetwork(ctx context.Context, port int) {
	s.access.mu.Lock()
	s.access.listenPort = port
	s.access.mu.Unlock()
	// Apps at home find the server by its announcement, which needs a port they can reach.
	if port != 0 {
		finder := discovery.New(s.log, discovery.Record{ServerID: s.id, Fingerprint: s.identity.fingerprint, Port: port})
		s.access.mu.Lock()
		s.access.finder = finder
		s.access.mu.Unlock()
		go finder.Run(ctx)
	}
	v, _ := s.access.current()
	s.applyCertificate(v)
	if port != 0 {
		go s.public.Run(ctx)
	}
	s.ports.Run(ctx)
}

func (s *Server) listenPort() int {
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	return s.access.listenPort
}

// Certificates is the owner's certificate as the listener serves it.
func (s *Server) Certificates() *tlsport.Store { return s.certs }

func (st *remoteState) current() (RemoteAccessSettings, []string) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.value, st.allowed
}

// security is Remote security as it works now under v. With ready, a certificate still being
// issued is counted as there, which is what a save is checked against: it will turn Enhanced on
// by itself when it arrives.
func (st *remoteState) security(v RemoteAccessSettings, allowed []string, finished, ready bool) RemoteSecurity {
	st.mu.RLock()
	pending, problem := st.portico != "" && !st.porticoReady, st.porticoProblem
	if v.OwnAddress.Enabled && v.OwnAddress.CertificateFile != "" && (st.ownCertificateUsable == nil || !st.ownCertificateUsable()) {
		// Enhanced waits on a usable certificate of either kind: the Portico one or the owner's.
		pending = true
	}
	st.mu.RUnlock()
	out := RemoteSecurity{Chosen: v.RemoteSecurity, Active: "standard"}
	switch {
	case v.RemoteSecurity != "enhanced":
	case !finished:
		out.Waiting = "setup_not_finished"
	case len(allowed) == 0 && !v.EnhancedWithoutAddress:
		out.Waiting = "no_approved_address"
	case pending && !ready:
		out.Waiting, out.Problem = "certificate_not_ready", problem
	default:
		out.Active = "enhanced"
	}
	out.Message = waitingMessages[out.Waiting]
	return out
}

// SetPorticoAddress is how Portico Services' side of the server says what the Portico address
// is and whether its certificate has been issued, with why not when something failed (Portico
// Services out of reach, the certificate authority busy). Enhanced waits for the certificate,
// working as Standard, and turns on by itself when it arrives. An empty address removes it.
func (s *Server) SetPorticoAddress(address string, certificateReady bool, problem string) {
	v, allowed := s.access.current()
	finished := s.setupFinished()
	before := s.access.security(v, allowed, finished, false).Active
	s.access.mu.Lock()
	s.access.portico, s.access.porticoReady, s.access.porticoProblem = address, certificateReady, problem
	s.access.mu.Unlock()
	s.access.set(v, finished)
	v, allowed = s.access.current()
	if after := s.access.security(v, allowed, finished, false).Active; after != before {
		// The owner sees it on the Remote access page; a notice joins this when notices exist.
		s.log.Info("remote security changed by itself", "inForce", after, "portico", address)
	}
}

// RemoteSecurity is the owner's choice and what is in force.
type RemoteSecurity struct {
	// Chosen is "enhanced" or "standard"; Active is what the server does now.
	Chosen string `json:"chosen"`
	Active string `json:"active"`
	// Waiting says why Enhanced, chosen, is not active yet: "setup_not_finished",
	// "no_approved_address" (no Portico address, own address or added address to answer at)
	// or "certificate_not_ready" (the Portico certificate is still being issued; Enhanced turns
	// on by itself when it is ready).
	Waiting string `json:"waiting,omitempty"`
	// Message says it in words the owner reads, when Enhanced is waiting.
	Message string `json:"message,omitempty"`
	// Problem says why the certificate has not been issued, when something failed.
	Problem string `json:"problem,omitempty"`
}

// waitingMessages are what the owner reads while Enhanced waits. The certificate's is the
// owner's own wording; the other two are proposed and listed for his approval.
var waitingMessages = map[string]string{
	"certificate_not_ready": "Enhanced Security requires an SSL certificate, which may take a few minutes to be issued to your server. " +
		"This feature will be enabled automatically once that certificate is available.",
	"setup_not_finished":  "Enhanced Security turns on when setup is finished. Until then this server works as Standard.",
	"no_approved_address": "Enhanced Security needs an address it can allow: a Portico address, your own address, or one you add. Until then this server works as Standard.",
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
		// The oldest goes, so a stranger's made-up addresses cannot keep out the one the
		// owner needs to see.
		oldest := ""
		for k, v := range st.refused {
			if oldest == "" || v.LastAt.Before(st.refused[oldest].LastAt) {
				oldest = k
			}
		}
		delete(st.refused, oldest)
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
	_, finished, err := s.db.Setting(ctx, setupFinishedKey)
	if err != nil {
		return err
	}
	s.access.set(v, finished)
	s.applyCertificate(v)
	return nil
}

// resetRemoteFile is the file an owner shut out creates in the data folder.
const resetRemoteFile = "reset-remote-access"

// resetRemoteAccess puts the remote access and network settings back to safe defaults when the
// owner has created the reset file, then removes it. Safe means nothing from anywhere is
// refused: Standard, not the Enhanced default, which could shut the owner out again.
func (s *Server) resetRemoteAccess(ctx context.Context, dataDir string) error {
	path := filepath.Join(dataDir, resetRemoteFile)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	v := defaultRemoteAccess()
	v.RemoteSecurity = "standard"
	raw, _ := json.Marshal(v)
	if err := s.db.SaveSetting(ctx, remoteAccessKey, string(raw), s.now()); err != nil {
		return err
	}
	if err := s.db.DeleteSetting(ctx, networkSettingsKey); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.log.Warn("the remote access and network settings were put back to their safe defaults, as the reset-remote-access file in the data folder asked")
	return nil
}

// setupFinishedKey is the setting that says first-run setup has finished, after which Enhanced
// may take effect.
const setupFinishedKey = "setup.finished"

// RemoteAccessStatus is what is true now, for the owner's Remote access page.
type RemoteAccessStatus struct {
	// ThisDevice is how the server sees the device asking: at home or away, and its address.
	ThisDevice ThisDevice `json:"thisDevice"`
	// Refused are the outside addresses Enhanced refused since the server started, newest first.
	Refused []RefusedAddress `json:"refused"`
	// RemoteSecurity is the owner's choice and what is in force, with why they differ.
	RemoteSecurity RemoteSecurity `json:"remoteSecurity"`
	// Warnings are the risky choices in force now, each in plain words, shown for as long as
	// they hold. The owner may keep any of them; none is refused.
	Warnings []Warning `json:"warnings"`
	// Certificate is the owner's own certificate: none, served, or what is wrong with it.
	Certificate tlsport.Status `json:"certificate"`
	// Router is what the home router last said about opening the public port.
	Router portmap.Status `json:"router"`
	// PublicAddress is this server's public IPv4 address as at least two outside services
	// (Cloudflare, Google, Amazon, icanhazip; none of them Portico's) agree it is.
	PublicAddress publicaddr.Answer `json:"publicAddress"`
	// IdentityCode is the server's identity in twelve characters, for the owner to give to
	// someone whose app asks for it.
	IdentityCode string `json:"identityCode"`
}

// warningMessages are proposed wording, listed for the owner's approval.
var warningMessages = map[string]string{
	"remote_access_off": "Remote access is off: nobody outside your home networks can reach this server, you included. " +
		"If that shuts you out, create an empty file named reset-remote-access in the server's data folder and restart it.",
	"enhanced_without_address": "Enhanced Security is on with no address to allow, so every connection from outside your home networks is refused. " +
		"If that shuts you out, create an empty file named reset-remote-access in the server's data folder and restart it.",
	"plain_http_outside": "Connections from outside your home networks may use plain HTTP, so what they send, passwords included, " +
		"can be read on the way. Add a certificate, or use Portico Accounts, to secure them.",
	"no_home_networks": "No home networks are listed, so every connection counts as away from home, even from this server's own machine.",
}

// warnings are the risky choices in force under v and the home networks n.
func (s *Server) warnings(v RemoteAccessSettings, sec RemoteSecurity, n auth.Networks) []Warning {
	var codes []string
	switch {
	case v.RemoteAccess == "off":
		codes = append(codes, "remote_access_off")
	case sec.Active == "enhanced" && v.EnhancedWithoutAddress && len(approved(v, s.porticoAddress())) == 0:
		codes = append(codes, "enhanced_without_address")
	case sec.Active == "standard" && v.SecureConnections == "preferred":
		codes = append(codes, "plain_http_outside")
	}
	if len(n.Home) == 0 {
		codes = append(codes, "no_home_networks")
	}
	out := []Warning{}
	for _, c := range codes {
		out = append(out, Warning{Code: c, Message: warningMessages[c]})
	}
	return out
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
		v, _ := s.access.current()
		v.revision = revision(saved)
		return v, err
	})
	Handle(s, "PUT /v1/admin/settings/remote-access", Owner, s.saveRemoteAccess)
	Handle(s, "GET /v1/admin/remote-access", Owner, func(r *http.Request, _ None) (RemoteAccessStatus, error) {
		return s.remoteStatus(r), nil

	})
	// The dry run: would the connection making this request be refused under these settings?
	Handle(s, "POST /v1/admin/remote-access/route-check", Owner, func(r *http.Request, req RemoteAccessSettings) (RouteCheck, error) {
		if err := cleanRemoteAccess(&req); err != nil {
			return RouteCheck{}, err
		}
		return s.judge(r, req, approved(req, s.porticoAddress()), true, true), nil
	})
	// Check now, and Try again: ask the router again and read the certificate files again.
	Handle(s, "POST /v1/admin/remote-access/check", Owner, func(r *http.Request, _ None) (RemoteAccessStatus, error) {
		s.certs.Check()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		s.ports.CheckNow(ctx)
		s.public.CheckNow(ctx)
		return s.remoteStatus(r), nil
	})
}

func (s *Server) remoteStatus(r *http.Request) RemoteAccessStatus {
	v, allowed := s.access.current()
	sec := s.access.security(v, allowed, s.setupFinished(), false)
	return RemoteAccessStatus{ThisDevice: ThisDevice{AtHome: s.fromHome(r), Address: s.remoteHost(r)},
		Refused: s.access.refusedList(), RemoteSecurity: sec, Warnings: s.warnings(v, sec, s.networks()),
		Certificate: s.certs.Status(), Router: s.ports.Status(), PublicAddress: s.public.Answer(),
		IdentityCode: s.identity.code()}
}

func (s *Server) setupFinished() bool {
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	return s.access.finished
}

func (s *Server) porticoAddress() string {
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	return s.access.portico
}

// RouteCheck says whether the connection that asked would be refused, why, and what fixes it.
type RouteCheck struct {
	Refused bool `json:"refused"`
	// Reason is "remote_access_off", "http" (plain HTTP from outside) or "address" (not an
	// approved address under Enhanced).
	Reason string `json:"reason,omitempty"`
	// Address is the host and port this connection uses, as the server sees it.
	Address string `json:"address"`
	// Fixes are what would let it in: "allow" (approve this address), "standard" (use
	// Standard) and "preferred" (Secure connections Preferred). None for Remote access Off.
	Fixes []string `json:"fixes"`
	// RemoteSecurity is what these settings would put in force.
	RemoteSecurity RemoteSecurity `json:"remoteSecurity"`
}

// judge works out what v would do to the request r. Through listed proxies only, the scheme,
// host and port are the person's; otherwise they are this server's connection's own.
// With ready, a Portico certificate still being issued is counted as there (see security).
func (s *Server) judge(r *http.Request, v RemoteAccessSettings, allowed []string, finished, ready bool) RouteCheck {
	return s.judgeWith(r, v, allowed, finished, ready, s.networks())
}

// judgeWith is judge with the home networks given, for a save of the network settings.
func (s *Server) judgeWith(r *http.Request, v RemoteAccessSettings, allowed []string, finished, ready bool, n auth.Networks) RouteCheck {
	secure := s.secure(r)
	out := RouteCheck{Address: s.requestAddress(r, secure), Fixes: []string{}, RemoteSecurity: s.access.security(v, allowed, finished, ready)}
	// This save preview uses the proposed networks rather than the current ones.
	if n.ProxyGaveNoAddress(r) {
		s.noteSilentProxy(peerAddr(r))
	}
	if n.FromHome(r) {
		return out
	}
	enhanced := out.RemoteSecurity.Active == "enhanced"
	switch {
	case v.RemoteAccess == "off":
		out.Refused, out.Reason = true, "remote_access_off"
	case !secure && (enhanced || v.SecureConnections == "required"):
		out.Refused, out.Reason = true, "http"
		if enhanced {
			out.Fixes = append(out.Fixes, "standard")
		}
		if v.SecureConnections == "required" {
			out.Fixes = append(out.Fixes, "preferred")
		}
	case enhanced && !slices.Contains(allowed, out.Address):
		out.Refused, out.Reason, out.Fixes = true, "address", []string{"allow", "standard"}
	}
	return out
}

func cleanRemoteAccess(v *RemoteAccessSettings) error {
	if v.AllowedAddresses == nil {
		v.AllowedAddresses = []string{}
	}
	v.OwnAddress.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v.OwnAddress.Domain), "."))
	if field, message := v.problem(); field != "" {
		return ErrField(field, message)
	}
	return nil
}

// RemoteAccessSave is a save of the whole document, with what to do about the connection in use.
type RemoteAccessSave struct {
	RemoteAccessSettings
	// AllowThisAddress approves the address this request uses, in the same save.
	AllowThisAddress bool `json:"allowThisAddress,omitempty"`
	// ConfirmRefusal accepts that this connection will be refused after the save.
	ConfirmRefusal bool `json:"confirmRefusal,omitempty"`
}

// errWouldRefuse answers a save that would refuse the connection in use, unconfirmed.
func errWouldRefuse(c RouteCheck) *Error {
	message := map[string]string{
		"remote_access_off": "Remote access doesn’t include this network.",
		"http":              "Remote security allows only HTTPS from outside.",
		"address":           "Remote security doesn’t allow this address yet.",
	}[c.Reason]
	return ErrConflict("would_refuse_this_route", "This address won’t work after this change. "+message+
		" Approve this address, choose another setting, or confirm. If you are ever shut out, create an empty file named "+
		resetRemoteFile+" in the server's data folder and restart it.")
}

// applyRemoteAccess checks and saves a document for the owner, as Settings and the end of
// setup both do. match is the ETag the owner read, or -1.
func (s *Server) applyRemoteAccess(r *http.Request, req RemoteAccessSave, match int64) (RemoteAccessSettings, error) {
	v := req.RemoteAccessSettings
	if err := cleanRemoteAccess(&v); err != nil {
		return RemoteAccessSettings{}, err
	}
	if req.AllowThisAddress {
		if address := s.requestAddress(r, true); !slices.Contains(approved(v, ""), address) {
			v.AllowedAddresses = append(v.AllowedAddresses, address)
		}
		if err := cleanRemoteAccess(&v); err != nil {
			return RemoteAccessSettings{}, err
		}
	}
	if check := s.judge(r, v, approved(v, s.porticoAddress()), true, true); check.Refused && !req.ConfirmRefusal {
		return RemoteAccessSettings{}, errWouldRefuse(check)
	}
	v.revision = 0
	raw, _ := json.Marshal(v)
	ctx := r.Context()
	saved, err := s.db.SaveSettingDocument(ctx, CallerFrom(ctx).UserID, remoteAccessKey, string(raw), match, s.now())
	if errors.Is(err, db.ErrStale) {
		return RemoteAccessSettings{}, errStale()
	}
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	// Saved after the document, so a failure here only leaves Enhanced waiting a little longer.
	if !s.setupFinished() {
		if err := s.db.SaveSetting(ctx, setupFinishedKey, "1", s.now()); err != nil {
			return RemoteAccessSettings{}, err
		}
	}
	s.access.set(v, true)
	s.applyCertificate(v)
	v.revision = revision(saved)
	return v, nil
}

// saveRemoteAccess replaces the whole document. A save that would refuse the connection
// making it is refused unless confirmed (see applyRemoteAccess).
func (s *Server) saveRemoteAccess(r *http.Request, req RemoteAccessSave) (RemoteAccessSettings, error) {
	match, err := settingsIfMatch(r)
	if err != nil {
		return RemoteAccessSettings{}, err
	}
	return s.applyRemoteAccess(r, req, match)
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
	v, allowed := s.access.current()
	finished := s.setupFinished()
	if v.RemoteAccess == "on" && v.SecureConnections == "preferred" && s.access.security(v, allowed, finished, false).Active == "standard" {
		return nil
	}
	c := s.judge(r, v, allowed, finished, false)
	if !c.Refused {
		return nil
	}
	switch c.Reason {
	case "remote_access_off":
		return refusal(http.StatusForbidden, "remote_access_off", "This server can be used only from its home network.", false)
	case "http":
		if _, ok := hostPort(c.Address, 80); ok && slices.Contains(c.Fixes, "standard") {
			s.access.noteRefused(c.Address, "http", []string{"certificate", "standard"}, s.now())
		}
		return refusal(http.StatusForbidden, "secure_connection_required",
			"This server takes connections from outside its home network only over a secure connection (https).", false)
	}
	// Only an address that reads as a host and port is kept: a made-up Host header is not.
	if _, ok := hostPort(c.Address, 443); ok {
		s.access.noteRefused(c.Address, "address", c.Fixes, s.now())
	}
	return refusal(http.StatusForbidden, "address_not_allowed", "This server does not answer at this address from outside its home network.", false)
}

// ownerOnlyAway reports whether this request comes from outside the home networks while only
// the owner may sign in from there.
func (s *Server) ownerOnlyAway(r *http.Request) bool {
	v, _ := s.access.current()
	return v.SignInFromOutside == "owner" && !s.fromHome(r)
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
