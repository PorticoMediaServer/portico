// Package webapp serves the built web app from a folder, so one server answers both the app
// and its API and nobody needs a separate web server in front.
package webapp

import (
	"bytes"
	"crypto/rand"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// policy is what the app's page may load and talk to: itself, the Cast sender script, and
// this server reached through Portico's remote-access names.
const policy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval' https://www.gstatic.com/cv/js/sender/ " +
	"https://www.gstatic.com/eureka/clank/ https://www.gstatic.com/cast/sdk/libs/sender/; style-src 'self'; " +
	"connect-src 'self' https://*.direct.getportico.tv https://web.getportico.tv; img-src 'self' blob: data: https://*.direct.getportico.tv; " +
	"media-src 'self' blob:; worker-src 'self' blob:; frame-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; " +
	"form-action 'self'"

// noncePlaceholder is the slot the web build leaves in its page for one inline stylesheet.
// Each answer fills it with a fresh value and allows exactly that value.
const noncePlaceholder = "__PORTICO_STYLE_NONCE__"

const maxPageBytes = 1 << 20

// hashed is the name the web build gives a file whose content it hashed:
// assets/<name>-<eight characters>.<extension>, and that file's source map. Such a file never
// changes under its name, so it is cached for good. A file copied from the build's public
// folder keeps the name it was given, may change under it, and does not match.
var hashed = regexp.MustCompile(`^assets/[^/]+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+(?:\.map)?$`)

// Find returns the folder of the built web app: the one given, or a folder named "web"
// beside the server's binary. It is "" when there is none with an index.html in it.
func Find(given string) string {
	candidates := []string{given}
	if exe, err := os.Executable(); err == nil && given == "" {
		candidates = []string{filepath.Join(filepath.Dir(exe), "web")}
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && info.Mode().IsRegular() {
			if abs, err := filepath.Abs(dir); err == nil {
				return abs
			}
		}
	}
	return ""
}

// Holds reports whether other is dir or a folder inside it. The web folder is handed to
// anyone who can reach the server, so it must never hold the data folder.
func Holds(dir, other string) bool {
	for _, p := range []*string{&dir, &other} {
		if abs, err := filepath.Abs(*p); err == nil {
			*p = abs
		}
		if real, err := filepath.EvalSymlinks(*p); err == nil {
			*p = real
		}
	}
	rel, err := filepath.Rel(dir, other)
	return err == nil && filepath.IsLocal(rel)
}

// Handler serves the web app in dir. A file that is there is served; any other address a
// browser opens as a page gets the app's own page, which routes inside itself. It answers
// GET and HEAD only.
func Handler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "This address takes GET only.", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		// A hidden file (.env, .git) is never part of the build.
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
			http.NotFound(w, r)
			return
		}
		// Opened beneath the folder: a link in it cannot lead the server to another file.
		root, err := os.OpenRoot(dir)
		if err != nil {
			http.Error(w, "The web app is not available on this server.", http.StatusServiceUnavailable)
			return
		}
		defer root.Close()
		file, err := root.Open(name)
		if err == nil {
			if info, statErr := file.Stat(); statErr != nil || !info.Mode().IsRegular() {
				file.Close()
				err = fs.ErrNotExist
			}
		}
		if err != nil {
			// A missing file of the build is missing. Anything else a browser opens as a page
			// is an address inside the app.
			if strings.HasPrefix(name, "assets/") || !strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			if file, err = root.Open(name); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		switch {
		case hashed.MatchString(name):
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// Asked about every time, and answered "not changed" while it has not.
			h.Set("Cache-Control", "no-cache")
		}
		if name != "index.html" {
			http.ServeContent(w, r, path.Base(name), info.ModTime(), file)
			return
		}
		page, err := io.ReadAll(io.LimitReader(file, maxPageBytes+1))
		if err != nil || len(page) > maxPageBytes {
			http.Error(w, "The web app is not available on this server.", http.StatusServiceUnavailable)
			return
		}
		h.Set("Content-Security-Policy", policy)
		modified := info.ModTime()
		if bytes.Contains(page, []byte(noncePlaceholder)) {
			nonce := rand.Text()
			page = bytes.ReplaceAll(page, []byte(noncePlaceholder), []byte(nonce))
			h.Set("Content-Security-Policy", strings.Replace(policy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1))
			// A "not changed" answer could pair a kept page with another answer's value.
			h.Set("Cache-Control", "no-store")
			modified = time.Time{}
		}
		http.ServeContent(w, r, "index.html", modified, bytes.NewReader(page))
	})
}
