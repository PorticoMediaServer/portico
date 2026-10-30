package httpapi

import (
	"bytes"
	"crypto/rand"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

const webContentSecurityPolicy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval' https://www.gstatic.com/cv/js/sender/ https://www.gstatic.com/eureka/clank/ https://www.gstatic.com/cast/sdk/libs/sender/; style-src 'self'; connect-src 'self' https://*.direct.getportico.tv https://web.getportico.tv; img-src 'self' blob: data: https://*.direct.getportico.tv; media-src 'self' blob:; worker-src 'self' blob:; frame-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'"

const styleNoncePlaceholder = "__PORTICO_STYLE_NONCE__"
const maximumWebDocumentBytes = 1 << 20

var apiNamespace = regexp.MustCompile(`^/(?:v[0-9]+|health)(?:/|$)`)

// hashedWebAsset is the name the web build gives a file whose content it
// hashed: assets/<name>-<eight characters>.<extension>, and that file's source
// map. Such a file never changes under its name, so it can be cached for good.
// A file copied from the build's public folder keeps the name it was given,
// may change under it, and must not match.
var hashedWebAsset = regexp.MustCompile(`^assets/[^/]+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+(?:\.map)?$`)

func contentHashedWebAsset(name string) bool { return hashedWebAsset.MatchString(name) }

func (d Dependencies) installationRoutes(mux *http.ServeMux) {
	// Readiness is exposed only after root composition has completed. These public
	// endpoints contain no account, build, route or dependency details.
	d.httpHandle(mux, "GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, map[string]bool{"alive": true})
	})
	d.httpHandle(mux, "GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		result := d.readiness(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		if !result.Ready {
			write(w, http.StatusServiceUnavailable, map[string]bool{"ready": false})
			return
		}
		write(w, http.StatusOK, map[string]bool{"ready": true})
	})
	// A browser draws styled subtitles itself and has no font with Chinese, Japanese and Korean
	// letters. It fetches the one burned-in subtitles use, only when a subtitle needs it.
	d.httpHandle(mux, "GET /v1/subtitle-fonts/cjk", func(w http.ResponseWriter, r *http.Request) {
		font, tag := mediaexec.CJKFont()
		w.Header().Set("Content-Type", "font/otf")
		w.Header().Set("Cache-Control", "public, max-age=2592000")
		w.Header().Set("ETag", tag)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(font))
	})
	if d.WebDirectory == "" {
		return
	}
	// A method-free fallback composes with protocol handlers such as DLNA,
	// which accept SUBSCRIBE as well as GET under their own path prefix.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			policyError(w, "method_not_allowed")
			return
		}
		if apiNamespace.MatchString(r.URL.Path) {
			policyError(w, "not_found")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
			policyError(w, "not_found")
			return
		}
		// os.Root confines symlinks; no state/config/media directory is mounted here.
		root, e := os.OpenRoot(d.WebDirectory)
		if e != nil {
			policyError(w, "web_unavailable")
			return
		}
		defer root.Close()
		f, e := root.Open(name)
		if e != nil {
			if strings.HasPrefix(name, "assets/") || !strings.Contains(r.Header.Get("Accept"), "text/html") {
				policyError(w, "not_found")
				return
			}
			name = "index.html"
			f, e = root.Open(name)
		}
		if e != nil {
			policyError(w, "not_found")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			policyError(w, "not_found")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-cache")
		if contentHashedWebAsset(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if name == "index.html" {
			// The build leaves one nonce slot for the dialog library's scroll-lock stylesheet.
			// A fresh nonce authorizes that sheet without allowing arbitrary inline styles.
			if info.Size() > maximumWebDocumentBytes {
				policyError(w, "web_unavailable")
				return
			}
			document, err := io.ReadAll(io.LimitReader(f, maximumWebDocumentBytes+1))
			if err != nil || len(document) > maximumWebDocumentBytes {
				policyError(w, "web_unavailable")
				return
			}
			if bytes.Contains(document, []byte(styleNoncePlaceholder)) {
				nonce := rand.Text()
				document = bytes.ReplaceAll(document, []byte(styleNoncePlaceholder), []byte(nonce))
				w.Header().Set("Content-Security-Policy", strings.Replace(webContentSecurityPolicy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1))
				// A 304 could combine a cached document with a different response's nonce.
				w.Header().Set("Cache-Control", "no-store")
				http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(document))
				return
			}
			http.ServeContent(w, r, "index.html", info.ModTime(), bytes.NewReader(document))
			return
		}
		http.ServeContent(w, r, path.Base(name), info.ModTime(), f)
	})
}
