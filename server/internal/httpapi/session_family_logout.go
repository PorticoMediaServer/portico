package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// sessionFamilyLogout intentionally does not call principal: an authentic retired
// or expired generation can revoke its family, but cannot authorize other work.
func (d Dependencies) sessionFamilyLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// This endpoint has no request body. Reject unsupported framing immediately;
	// do not wait for EOF on an unknown/chunked body or accept an extra payload.
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_logout_request", "message": "Sign-out does not accept query parameters or a request body.", "retryable": false}})
		return
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > 2055 || !strings.HasPrefix(values[0], "Bearer ") {
		write(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
		return
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if len(token) < 43 || len(token) > 2048 || strings.ContainsAny(token, " \t\r\n,") {
		write(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err := d.Identity.LogoutToken(ctx, token)
	if errors.Is(err, identity.ErrUnauthorized) {
		write(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
		return
	}
	if err != nil || ctx.Err() != nil {
		w.Header().Set("Retry-After", "1")
		write(w, 503, map[string]any{"error": map[string]any{"code": "session_logout_unavailable", "message": "Sign-out could not be confirmed. Try again.", "retryable": true}})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
