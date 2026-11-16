package api

import (
	"bytes"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// subtitleFontRoutes gives device subtitle renderers the same CJK fallback as
// burned-in subtitles. It is a bundled, licensed asset, unrelated to any title.
func (s *Server) subtitleFontRoutes() {
	route := &Route{Pattern: "GET /v1/subtitle-fonts/cjk", Access: Public, Scope: ScopeOpen}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		font, tag := mediaexec.CJKFont()
		w.Header().Set("Content-Type", "font/otf")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", tag)
		http.ServeContent(w, r, "cjk.otf", time.Time{}, bytes.NewReader(font))
	})
}
