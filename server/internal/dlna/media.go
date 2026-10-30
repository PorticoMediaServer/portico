package dlna

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

var errWithdrawn = errors.New("dlna: switched off or no longer on the home network")

// validToken accepts the public tokens catalogue files and libraries use.
func validToken(v string) bool {
	if v == "" || len(v) > 128 {
		return false
	}
	return strings.Trim(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == ""
}

// serveMedia serves one catalogued original by its opaque token, never by a
// path, and only while the file belongs to a live title in a live library.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	token, _, _ := strings.Cut(name, ".")
	if !validToken(token) || s.Storage == nil {
		http.NotFound(w, r)
		return
	}
	var container, video string
	// A song's or book's file is audio, whatever its embedded cover probes as.
	err := dbwork.QueryRow(r.Context(), s.DB, `SELECT a.container,
 CASE WHEN EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE l.asset_id=a.id AND e.kind IN(7,9)) THEN '' ELSE a.video_codec END FROM catalog_assets a
 WHERE a.token=? AND a.available=1 AND a.container!='strm' AND EXISTS(SELECT 1 FROM catalog_asset_links l
  JOIN catalog_entities e ON e.id=l.entity_id AND e.retired=0 JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1
  JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
  WHERE l.asset_id=a.id AND l.available=1)`, token).Scan(&container, &video)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	source, err := assets.ResolveCatalogPlaybackSource(r.Context(), s.DB, token)
	if err != nil {
		// Storage that is only offline is a temporary state, not a withdrawn
		// title: the same renderer request works once the source reconnects.
		if errors.Is(err, storage.ErrStorageOffline) {
			w.Header().Set("Retry-After", "2")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
		return
	}
	file, err := assets.OpenIsolatedSource(r.Context(), s.Storage, source)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusConflict), http.StatusConflict)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", mediaType(container, video))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	// Set directly, not through Set, so the names keep the casing older TV
	// firmware matches on.
	w.Header()["contentFeatures.dlna.org"] = []string{contentFeatures}
	w.Header()["transferMode.dlna.org"] = []string{transferMode(r, "Streaming")}
	http.ServeContent(w, r, "", file.Modified, &guardedSource{ReadSeeker: file, server: s, request: r, checked: time.Now()})
}

// guardedSource asks the switch and the home network again while a file
// streams, so turning DLNA off ends a stream already under way.
type guardedSource struct {
	io.ReadSeeker
	server  *Server
	request *http.Request
	checked time.Time
}

func (g *guardedSource) Read(p []byte) (int, error) {
	if time.Since(g.checked) >= recheckEvery {
		if ok, _ := g.server.admitted(g.request); !ok {
			return 0, errWithdrawn
		}
		g.checked = time.Now()
	}
	return g.ReadSeeker.Read(p)
}

// transferMode echoes a player's requested DLNA transfer mode when it is one
// this response can honour.
func transferMode(r *http.Request, fallback string) string {
	switch v := r.Header.Get("transferMode.dlna.org"); v {
	case "Streaming", "Interactive", "Background":
		return v
	}
	return fallback
}

// serveArt serves a live entity's display artwork at thumbnail size.
func (s *Server) serveArt(w http.ResponseWriter, r *http.Request, public string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if _, ok := entityid.Decode(public); !ok || s.Artwork == nil {
		http.NotFound(w, r)
		return
	}
	var kind int
	if err := dbwork.QueryRow(r.Context(), s.DB, `SELECT e.kind FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 WHERE e.public_id=pid_blob(?) AND e.retired=0`, public).Scan(&kind); err != nil {
		http.NotFound(w, r)
		return
	}
	source, ok := artwork[kind]
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, mime, err := s.Artwork(r.Context(), source[0], public, source[1])
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
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header()["transferMode.dlna.org"] = []string{transferMode(r, "Interactive")}
	http.ServeContent(w, r, "", info.ModTime(), file)
}
