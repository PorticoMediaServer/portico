package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/diagnostics"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

func (d Dependencies) supportOwner(ctx context.Context, r *http.Request) (identity.Principal, error) {
	return d.ownerContext(ctx, r)
}

func (d Dependencies) supportRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/support-report", d.supportHandler(diagnostics.Read))
}
func (d Dependencies) supportHandler(read func(context.Context, *sql.DB, bool) (diagnostics.Report, error)) http.HandlerFunc {
	// A small dedicated gate bounds expensive snapshot requests independently of scanning.
	slots := make(chan struct{}, 2)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// The deadline includes initial authorization, snapshot and fresh authorization.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		p, e := d.supportOwner(ctx, r)
		if e != nil {
			failure(w, e)
			return
		}
		if r.URL.RawQuery != "" {
			write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_support_query", "message": "Support reports do not accept query parameters.", "retryable": false}})
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			write(w, 429, map[string]any{"error": map[string]any{"code": "support_busy", "message": "Another support report is being prepared. Try again shortly.", "retryable": true}})
			return
		}
		report, e := read(ctx, d.DB, d.Hosted != nil && d.Hosted.Configured())
		if e != nil {
			write(w, 503, map[string]any{"error": map[string]any{"code": "support_unavailable", "message": "The support report could not be read. Try again.", "retryable": true}})
			return
		}
		if _, e = d.supportOwner(ctx, r); e != nil {
			failure(w, e)
			return
		}
		// Scope is transport-only. The downloadable artifact contains report alone.
		value := map[string]any{"scope": map[string]string{"serverId": d.Identity.ID(), "viewerFence": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}, "report": report}
		encoded, e := json.Marshal(value)
		if e != nil || len(encoded) > diagnostics.MaxReportBytes {
			write(w, 503, map[string]any{"error": map[string]any{"code": "support_unavailable", "message": "The support report could not be prepared.", "retryable": true}})
			return
		}
		if ctx.Err() != nil {
			write(w, 503, map[string]any{"error": map[string]any{"code": "support_unavailable", "message": "The support report could not be prepared.", "retryable": true}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(encoded)
	}
}
