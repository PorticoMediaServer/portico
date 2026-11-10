package api

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
)

// imageRoutes serves artwork. An item, a person or a credit carries the address of each of
// its pictures, and an app fetches it as it is, adding only the size it will draw at.
//
// The address is its own permission: it is named by a long hash that only a reply about
// something the caller may see ever contains, so the route asks for no token. That is what
// lets a web page put it in an <img>, and lets every cache keep it for good, because the
// picture behind one address never changes.
func (s *Server) imageRoutes() {
	route := &Route{Pattern: "GET /v1/images/{id}", Access: Public, Scope: ScopeOpen}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		size := func(name string) (int, bool) {
			text := r.URL.Query().Get(name)
			if text == "" {
				return 0, true
			}
			n, err := strconv.Atoi(text)
			return n, err == nil && n >= 1 && n <= 10000
		}
		width, okW := size("w")
		height, okH := size("h")
		if !okW || !okH {
			s.writeError(w, r, ErrInvalid("Give w and h as whole numbers of pixels."))
			return
		}
		data, contentType, err := s.artwork.Read(r.PathValue("id"), width, height)
		switch {
		case errors.Is(err, artwork.ErrNotFound), err == nil && !strings.HasPrefix(contentType, "image/"):
			s.writeError(w, r, ErrNotFound())
			return
		case err != nil:
			s.writeError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+r.PathValue("id")+"-"+strconv.Itoa(len(data))+`"`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	})
}
