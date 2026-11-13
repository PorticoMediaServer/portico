// Package receiver embeds the static web bundles the server serves directly.
//
// The Cast receiver application lives under cast/. It is plain HTML and
// JavaScript plus the canonical generated text catalogue, embedded so every GOOS target
// and the Docker image serve identical bytes and no deployment has to publish
// the page separately.
package receiver

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed cast/*.html cast/*.css cast/*.js cast/assets/*
var bundles embed.FS

// Files is the filesystem mounted at /receiver/.
func Files() fs.FS { return bundles }

// Handler serves the embedded receiver and its own fonts. Google's CAF dependencies
// are the only remote scripts. The page can be framed only by the Cast host.
func Handler() http.Handler {
	files := http.StripPrefix("/receiver/", http.FileServerFS(Files()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := receiverCSP
		if r.URL.Path == "/receiver/cast/" || r.URL.Path == "/receiver/cast/index.html" {
			policy = strings.Replace(policy, "https://www.gstatic.com/cast/;", "https://www.gstatic.com/cast/ https://ajax.googleapis.com/ajax/libs/shaka-player/4.15.56/;", 1)
			policy = strings.Replace(policy, "style-src 'self' 'unsafe-inline';", "style-src 'self'; style-src-elem 'self' https://www.gstatic.com 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=';", 1)
		}
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Del("X-Frame-Options")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		files.ServeHTTP(w, r)
	})
}

const receiverCSP = "default-src 'none'; script-src 'self' https://www.gstatic.com/cast/; style-src 'self' 'unsafe-inline'" +
	"; connect-src 'self' https:; img-src 'self' blob: data:; font-src 'self'; media-src 'self' blob: https:; worker-src 'self' blob:" +
	"; frame-ancestors https://www.gstatic.com; base-uri 'none'; object-src 'none'; form-action 'self'"
