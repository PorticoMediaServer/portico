// Package dlna is the owner's one-switch DLNA server: when it is on, any UPnP
// player on the home network can browse every library and play every file,
// without signing in. There are deliberately no profiles, library pickers or
// rating filters, and no conversion: originals are served as they are.
//
// The only boundary is the home network. Every request is refused unless the
// switch is on and the socket peer is inside the owner's home networks; a
// request carrying forwarding headers came through a proxy and is refused
// too, because a proxy can make a remote client look local. Subscriptions are
// accepted but never called back (CallStranger, CVE-2020-12695): the server
// makes no outbound request on a player's behalf.
package dlna

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Settings is the owner's switch and the home networks it is limited to.
type Settings struct {
	Enabled bool
	Home    []netip.Prefix
}

// Server answers /dlna/: the device and service descriptions, the SOAP
// control endpoints, and the media and artwork a player fetches.
type Server struct {
	DB      *sql.DB
	Storage *storage.Client
	// ServerID is the persisted server identity the device UUID derives from.
	ServerID func() string
	Name     func() string
	Version  string
	// Settings is read on every request (and while a file streams), so turning
	// DLNA off or narrowing the home networks applies at once.
	Settings func(context.Context) (Settings, error)
	// Artwork opens an entity's display artwork: namespace and role as the
	// metadata service names them.
	Artwork func(ctx context.Context, namespace, id, role string) (*os.File, string, error)
}

const (
	Prefix          = "/dlna/"
	descriptionPath = Prefix + "device.xml"
	mediaPath       = Prefix + "media/"
	artPath         = Prefix + "art/"
	// maxControlBytes bounds a SOAP request. Real Browse calls are a few
	// hundred bytes.
	maxControlBytes = 16 << 10
	// recheckEvery is how often a streaming file asks the switch again.
	recheckEvery = 5 * time.Second
)

// DeviceUUID is this server's stable UPnP device id, derived from its
// persisted identity so it survives restarts without a table of its own.
func DeviceUUID(serverID string) string {
	sum := sha256.Sum256([]byte("portico dlna device\x00" + serverID))
	sum[6] = sum[6]&0x0f | 0x50 // name-based (version 5 layout)
	sum[8] = sum[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// forwardingHeaders mark a request relayed by a proxy.
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip"}

// admitted reports whether r may be served: the switch is on and the socket
// peer is on the home network.
func (s *Server) admitted(r *http.Request) (bool, int) {
	if s == nil || s.Settings == nil {
		return false, http.StatusNotFound
	}
	settings, err := s.Settings(r.Context())
	if err != nil || !settings.Enabled {
		return false, http.StatusNotFound
	}
	for key := range r.Header {
		for _, forwarded := range forwardingHeaders {
			if strings.EqualFold(key, forwarded) {
				return false, http.StatusForbidden
			}
		}
	}
	peer, ok := networkpolicy.Peer(r)
	if !ok || !networkpolicy.Contains(settings.Home, peer) {
		return false, http.StatusForbidden
	}
	return true, 0
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ok, status := s.admitted(r); !ok {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Server", s.serverHeader())
	path := r.URL.Path
	switch {
	case path == descriptionPath:
		s.serveDocument(w, r, s.deviceDescription())
	case strings.HasPrefix(path, Prefix+"scpd/"):
		document, ok := serviceDescriptions[strings.TrimPrefix(path, Prefix+"scpd/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.serveDocument(w, r, document)
	case strings.HasPrefix(path, Prefix+"control/"):
		s.serveControl(w, r, strings.TrimPrefix(path, Prefix+"control/"))
	case strings.HasPrefix(path, Prefix+"event/"):
		serveSubscription(w, r)
	case strings.HasPrefix(path, mediaPath):
		s.serveMedia(w, r, strings.TrimPrefix(path, mediaPath))
	case strings.HasPrefix(path, artPath):
		s.serveArt(w, r, strings.TrimPrefix(path, artPath))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serverHeader() string {
	version := s.Version
	if version == "" {
		version = "dev"
	}
	return "Portico/" + version + " UPnP/1.0 DLNADOC/1.50"
}

func (s *Server) serveDocument(w http.ResponseWriter, r *http.Request, document string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", time.Time{}, strings.NewReader(document))
}

// serveSubscription accepts GENA subscriptions so strict players carry on, and
// never sends an event: no request is ever made to the CALLBACK a player names.
func serveSubscription(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "SUBSCRIBE":
		sid := r.Header.Get("SID")
		if sid == "" {
			if r.Header.Get("NT") != "upnp:event" || r.Header.Get("CALLBACK") == "" {
				http.Error(w, "Precondition Failed", http.StatusPreconditionFailed)
				return
			}
			sid = "uuid:" + randomUUID()
		} else if len(sid) > 64 || strings.ContainsAny(sid, "\r\n") {
			http.Error(w, "Precondition Failed", http.StatusPreconditionFailed)
			return
		}
		w.Header().Set("SID", sid)
		w.Header().Set("TIMEOUT", "Second-1800")
		w.WriteHeader(http.StatusOK)
	case "UNSUBSCRIBE":
		w.WriteHeader(http.StatusOK)
	default:
		w.Header().Set("Allow", "SUBSCRIBE, UNSUBSCRIBE")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// baseURL is how the player reached this server: the local socket address the
// request arrived on, never the Host header the player sent.
func baseURL(r *http.Request) string {
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if address, err := netip.ParseAddrPort(local.String()); err == nil {
			ip := address.Addr().Unmap().WithZone("")
			if !ip.IsUnspecified() {
				return "http://" + netip.AddrPortFrom(ip, address.Port()).String()
			}
		}
	}
	return "http://" + r.Host
}
