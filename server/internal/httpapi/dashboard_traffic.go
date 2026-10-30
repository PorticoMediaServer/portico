package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"strings"
	"time"
)

// Only successful media bodies are metered. Authorization stays with the
// existing handler; headers, errors and seek indexes are not media traffic.
func (d Dependencies) meteredMedia(pattern string, next http.Handler) http.Handler {
	if d.streamTraffic == nil || !strings.HasPrefix(pattern, "GET /v1/media/") || strings.Contains(pattern, "audio-index") || strings.Contains(pattern, "subtitles") {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if grant := r.PathValue("grant"); grant != "" {
			_ = d.DB.QueryRowContext(r.Context(), `SELECT id FROM playback_sessions WHERE grant_hash=?`, identity.Digest(grant)).Scan(&id)
		} else {
			id = r.PathValue("playbackId")
		}
		host := ""
		if address, ok := networkpolicy.Client(r, d.trustedProxyPrefixes(r.Context())); ok {
			host = address.String()
		}
		remote := d.requestIsRemote(r)

		m := &mediaTrafficWriter{ResponseWriter: w, record: func(n int) { d.streamTraffic.Add(id, host, remote, n, time.Now()) }}
		next.ServeHTTP(m, r)
	})
}

type mediaTrafficWriter struct {
	http.ResponseWriter
	status int
	record func(int)
}

func (w *mediaTrafficWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mediaTrafficWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *mediaTrafficWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(p)
	if w.status == 200 || w.status == 206 {
		w.record(n)
	}
	return n, e
}
