// Package dlna is the owner's one-switch DLNA server. While it is on, any UPnP player on the
// owner's home networks can browse the video and music libraries and play every original
// file, without signing in. By the owner's decision it ignores profiles, library access,
// rating limits, blocked tags, viewing hours and stream limits, never converts, and leaves
// nothing behind: no session, no play history, no watched mark, no Now Playing entry. It will
// never be expanded.
//
// The only boundary is the home network, and it is checked on every request:
//
//   - the switch is on and the owner has listed home networks (the defaults the server
//     guesses before the owner chooses do not count);
//   - the socket peer is in one of them and is not this machine's loopback, so a proxy on
//     this machine cannot make the internet look local;
//   - the request carries no forwarding header: a proxied request is never served.
//
// A file that is streaming asks again every few seconds, so turning DLNA off, or taking a
// network off the list, stops what is playing too. Subscriptions are accepted but never
// called back (CallStranger, CVE-2020-12695): the server makes no request on a player's behalf.
package dlna

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
)

// Server answers /dlna/: the device and service descriptions, the SOAP control addresses and
// the original files a player fetches.
type Server struct {
	DB  *db.DB
	Log *slog.Logger
	// ServerID is the persisted server identity the device's UUID is worked out from.
	ServerID func() string
	Name     func() string
	Version  string
	// Home is the home networks the owner listed while DLNA is on, and nil while it is off or
	// the owner has listed none. It is read on every request, so a change applies at once.
	Home func() []netip.Prefix
}

const (
	Prefix          = "/dlna/"
	descriptionPath = Prefix + "device.xml"
	mediaPath       = Prefix + "media/"
	artPath         = Prefix + "art/"
	// maxControlBytes bounds a SOAP request. Real Browse calls are a few hundred bytes.
	maxControlBytes = 16 << 10
	// recheckEvery is how often a streaming file asks the switch and the networks again.
	recheckEvery = 5 * time.Second
	// maxBrowse is the most entries one Browse answers.
	maxBrowse = 200
)

// DeviceUUID is this server's stable UPnP device id, worked out from its persisted identity
// so it survives restarts with nothing stored.
func DeviceUUID(serverID string) string {
	sum := sha256.Sum256([]byte("portico dlna device\x00" + serverID))
	sum[6] = sum[6]&0x0f | 0x50 // name-based (version 5 layout)
	sum[8] = sum[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func randomUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// forwardingHeaders mark a request passed on by a proxy.
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip"}

func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// peer is the address the request's connection came from.
func peer(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap().WithZone(""), true
}

// admitted says whether r may be served now, and the status to refuse it with.
func (s *Server) admitted(r *http.Request) (bool, int) {
	home := s.Home()
	if len(home) == 0 {
		return false, http.StatusNotFound
	}
	for key := range r.Header {
		for _, forwarded := range forwardingHeaders {
			if strings.EqualFold(key, forwarded) {
				return false, http.StatusForbidden
			}
		}
	}
	addr, ok := peer(r)
	if !ok || addr.IsLoopback() || !contains(home, addr) {
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
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serverHeader() string {
	return "Portico UPnP/1.0 DLNADOC/1.50"
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

// serveSubscription accepts event subscriptions so strict players carry on, and never sends
// an event: no request is ever made to the CALLBACK a player names.
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

// baseURL is how the player reached this server: the local address the request arrived on,
// never the Host header the player sent.
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

// The tree: "0" is the root, whose children are the video and music libraries; a library's
// or a title's id is its Portico id. A title that plays carries its original file.

type object struct {
	id, parent, title, class string
	container                bool
	children                 int
	year, track              int
	artist, album            string
	art                      string // unused: DLNA shows no artwork
	res                      *resource
}

type resource struct {
	token, container, video        string
	size                           int64
	duration                       float64
	width, height                  int
	sampleRate, bitDepth, channels int
}

func (s *Server) browse(r *http.Request, args map[string]string) ([]string, error) {
	id := args["ObjectID"]
	start, err1 := strconv.ParseUint(args["StartingIndex"], 10, 32)
	count, err2 := strconv.ParseUint(args["RequestedCount"], 10, 32)
	if args["StartingIndex"] == "" {
		start, err1 = 0, nil
	}
	if args["RequestedCount"] == "" {
		count, err2 = 0, nil
	}
	if id == "" || len(id) > 128 || err1 != nil || err2 != nil {
		return nil, soapFault{errInvalidArgs, "Invalid Args"}
	}
	if count == 0 || count > maxBrowse {
		count = maxBrowse
	}
	ctx := r.Context()
	var objects []object
	var total int
	switch args["BrowseFlag"] {
	case "BrowseMetadata":
		o, err := s.metadata(ctx, id)
		if err != nil {
			return nil, err
		}
		objects, total = []object{o}, 1
	case "BrowseDirectChildren":
		var err error
		if objects, total, err = s.children(ctx, id, int(start), int(count)); err != nil {
			return nil, err
		}
	default:
		return nil, soapFault{errInvalidArgs, "Invalid Args"}
	}
	return []string{"Result", didl(baseURL(r), objects), "NumberReturned", strconv.Itoa(len(objects)),
		"TotalMatches", strconv.Itoa(total), "UpdateID", "1"}, nil
}

func (s *Server) systemUpdateID(context.Context) (uint32, error) { return 1, nil }

func (s *Server) metadata(ctx context.Context, id string) (object, error) {
	if id == "0" {
		libs, err := s.DB.DLNALibraries(ctx)
		return object{id: "0", parent: "-1", title: s.friendlyName(), class: "object.container", container: true, children: len(libs)}, err
	}
	e, err := s.DB.DLNAEntryByID(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		return object{}, errNoObject
	}
	if err != nil {
		return object{}, err
	}
	parent := "0"
	if !e.Library {
		if parent, err = s.DB.DLNAParent(ctx, id); err != nil {
			return object{}, err
		}
	}
	return toObject(e, parent), nil
}

func (s *Server) children(ctx context.Context, id string, start, count int) ([]object, int, error) {
	if id == "0" {
		libs, err := s.DB.DLNALibraries(ctx)
		var out []object
		for i, l := range libs {
			if i >= start && len(out) < count {
				out = append(out, toObject(l, "0"))
			}
		}
		return out, len(libs), err
	}
	entries, total, err := s.DB.DLNAChildren(ctx, id, start, count)
	if errors.Is(err, db.ErrNotFound) {
		return nil, 0, errNoObject
	}
	var out []object
	for _, e := range entries {
		out = append(out, toObject(e, id))
	}
	return out, total, err
}

func toObject(e db.DLNAEntry, parent string) object {
	o := object{id: e.ID, parent: parent, title: e.Title, year: e.Year, children: e.Children}
	switch e.Kind {
	case db.KindMovie:
		o.class = "object.item.videoItem.movie"
	case db.KindEpisode:
		o.class = "object.item.videoItem"
	case db.KindTrack:
		o.class, o.track, o.artist, o.album = "object.item.audioItem.musicTrack", e.Index, e.Artist, e.Album
	case db.KindArtist:
		o.class, o.container = "object.container.person.musicArtist", true
	case db.KindAlbum:
		o.class, o.container = "object.container.album.musicAlbum", true
	default:
		o.class, o.container = "object.container.storageFolder", true
	}
	if e.Library {
		o.class, o.container = "object.container.storageFolder", true
	}
	if !o.container && e.FileID != 0 {
		o.res = &resource{token: e.ID, container: e.Container, video: e.VideoCodec, size: e.Size, duration: float64(e.DurationMS) / 1000,
			width: e.Width, height: e.Height, channels: e.Channels}
	}
	return o
}

// serveMedia sends a title's original file as it is, with ranges. Nothing is recorded.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	id, _, _ := strings.Cut(name, ".")
	e, err := s.DB.DLNAEntryByID(r.Context(), id)
	if err != nil || e.Library || e.FileID == 0 {
		http.NotFound(w, r)
		return
	}
	file, err := s.open(r.Context(), e.FileID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mediaType(e.Container, e.VideoCodec))
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", contentFeatures)
	http.ServeContent(w, r, "", info.ModTime(), &stillAllowed{File: file, s: s, r: r, checked: time.Now()})
}

// open opens a file the scanner recorded, by the one rule for reading library files. A file
// on remote storage is not served.
func (s *Server) open(ctx context.Context, fileID int64) (*os.File, error) {
	f, err := s.DB.FileOnDisk(ctx, fileID)
	if err != nil {
		return nil, err
	}
	folders, err := s.DB.LibraryFolders(ctx)
	if err != nil {
		return nil, err
	}
	return libraryfile.Open(f.Path, folders)
}

// stillAllowed stops a stream when DLNA is turned off or the player's network leaves the list.
type stillAllowed struct {
	*os.File
	s       *Server
	r       *http.Request
	checked time.Time
}

var errStopped = errors.New("DLNA was turned off for this player")

func (a *stillAllowed) Read(p []byte) (int, error) {
	if time.Since(a.checked) >= recheckEvery {
		if ok, _ := a.s.admitted(a.r); !ok {
			return 0, errStopped
		}
		a.checked = time.Now()
	}
	return a.File.Read(p)
}

var _ io.ReadSeeker = (*stillAllowed)(nil)
