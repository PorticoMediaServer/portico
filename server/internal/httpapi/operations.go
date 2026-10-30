package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"time"
)

func (d Dependencies) operationsRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/operations/{panel}", d.operationsHandler(operations.Read))
}
func (d Dependencies) operationsHandler(read func(context.Context, *sql.DB, string) (operations.Panel, error)) http.HandlerFunc {
	slots := make(chan struct{}, 4)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		// Authenticate against the current local account or installed Hosted policy.
		before, err := d.supportOwner(ctx, r)
		if err != nil {
			operationsAuthFailure(w, err)
			return
		}
		name := r.PathValue("panel")
		if r.URL.RawQuery != "" || (name != "memory" && name != "database" && name != "build") {
			write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_operations_panel", "message": "Choose an available server panel.", "retryable": false}})
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			write(w, 429, map[string]any{"error": map[string]any{"code": "operations_busy", "message": "Server observations are busy. Try again shortly.", "retryable": true}})
			return
		}
		panel, err := read(ctx, d.DB, name)
		after, authErr := d.supportOwner(ctx, r)
		if authErr != nil {
			operationsAuthFailure(w, authErr)
			return
		}
		if before.Hash != after.Hash || before.Epoch != after.Epoch || before.ProfileID != after.ProfileID {
			write(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
			return
		}
		if err != nil || ctx.Err() != nil {
			code := 503
			if errors.Is(err, operations.ErrPanel) {
				code = 400
			}
			write(w, code, map[string]any{"error": map[string]any{"code": "operations_unavailable", "message": "This server panel could not be read. Try again.", "retryable": true}})
			return
		}
		write(w, 200, map[string]any{"scope": map[string]string{"serverId": d.Identity.ID(), "viewerFence": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", before.Hash, before.Epoch))))}, "panel": panel})
	}
}

func operationsAuthFailure(w http.ResponseWriter, err error) {
	if status, code, ok := identity.Refusal(err); ok && status != 401 {
		write(w, status, map[string]any{"error": map[string]any{"code": code, "message": err.Error(), "retryable": false}})
		return
	}
	if errors.Is(err, identity.ErrUnauthorized) {
		write(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
		return
	}
	write(w, 503, map[string]any{"error": map[string]any{"code": "operations_unavailable", "message": "Owner access could not be checked. Try again.", "retryable": true}})
}
