package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"strconv"
	"strings"
)

// artworkSize maps a request to the representation it names. size=small or
// large names a slot; the legacy w maps to a slot by the small edge, never
// resizes, and keeps its priority over size; anything else is large. A bad
// width maps to an invalid size, which every artwork reader refuses.
func artworkSize(r *http.Request) metadata.ArtworkSize {
	q := r.URL.Query()
	if q.Has("w") {
		width, err := strconv.Atoi(q.Get("w"))
		if err != nil || width < 1 {
			return -1
		}
		return metadata.ArtworkSizeForWidth(width)
	}
	switch q.Get("size") {
	case "small", "thumbnail":
		return metadata.ArtworkSmall
	case "ambient-v1":
		return metadata.ArtworkAmbient
	}
	return metadata.ArtworkLarge
}

// artworkTokenMatches reports whether the request's rendition token r names
// the served file, whose name is its full SHA-256.
func artworkTokenMatches(r *http.Request, digest string) bool {
	token := r.URL.Query().Get("r")
	return len(token) == artworkref.TokenLength && strings.HasPrefix(digest, token)
}

// Called only after authorization and a successful open of this exact version.
func serveArtwork(w http.ResponseWriter, r *http.Request, f *os.File, mime string) {
	info, err := f.Stat()
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	digest := strings.TrimSuffix(filepath.Base(f.Name()), ".img")
	if len(digest) == 64 && strings.Trim(digest, "0123456789abcdef") == "" {
		w.Header().Set("ETag", `"`+digest+`"`)
		// Only a URL that names these exact bytes may be cached for good: the
		// selected source (checked by the caller) and the file's token r. Any
		// other request is answered with the current file and revalidated.
		if r.URL.Query().Get("v") != "" && r.URL.Query().Get("candidate") == "" && artworkTokenMatches(r, digest) {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		}
	}
	http.ServeContent(w, r, "artwork", info.ModTime(), f)
}

func artworkVersionFailure(w http.ResponseWriter, err error) bool {
	if err != metadata.ErrArtworkVersion {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	policyError(w, "artwork_gone")
	return true
}
