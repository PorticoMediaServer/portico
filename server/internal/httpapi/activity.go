package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strconv"
	"time"
)

func activityFailure(w http.ResponseWriter, e error) {
	code, status := "activity_unavailable", 503
	message := "Server activity could not be loaded. Try again."
	switch {
	case errors.Is(e, playback.ErrActivityQuery):
		code, status, message = "invalid_activity_request", 400, publicErrorMessage("invalid_activity_request")
	case errors.Is(e, playback.ErrActivityConflict):
		code, status, message = "playback_command_conflict", 409, publicErrorMessage("playback_command_conflict")
	case errors.Is(e, playback.ErrActivityExpired):
		code, status, message = "playback_operation_expired", 410, publicErrorMessage("playback_operation_expired")
	case errors.Is(e, playback.ErrActivityCapacity):
		code, status, message = "playback_command_capacity", 429, publicErrorMessage("playback_command_capacity")
	case errors.Is(e, identity.ErrUnauthorized), errors.Is(e, sql.ErrNoRows):
		failure(w, e)
		return
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == 503, "serverTime": time.Now().UTC().Format(time.RFC3339Nano)}})
}
func (d Dependencies) activityRoutes(mux *http.ServeMux) {
	for _, route := range []string{"GET /v1/admin/playback-sessions", "POST /v1/admin/playback-sessions/{id}/stop", "GET /v1/admin/playback-operations/{operationId}"} {
		d.httpHandle(mux, route, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			p, e := d.owner(r)
			if e != nil {
				failure(w, e)
				return
			}
			scope := playback.ActivityScope{ServerID: d.Identity.ID(), ViewerFence: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}
			var out any
			statusCode := 200
			if route == "GET /v1/admin/playback-sessions" {
				status, limit, cursor := "open", 40, ""
				for k, v := range r.URL.Query() {
					if len(v) != 1 {
						activityFailure(w, playback.ErrActivityQuery)
						return
					}
					switch k {
					case "status":
						status = v[0]
					case "limit":
						limit, e = strconv.Atoi(v[0])
					case "cursor":
						cursor = v[0]
					default:
						e = playback.ErrActivityQuery
					}
					if e != nil {
						activityFailure(w, playback.ErrActivityQuery)
						return
					}
				}
				out, e = d.Playback.Activity(ctx, p, scope, status, limit, cursor)
			} else {
				if r.URL.RawQuery != "" {
					activityFailure(w, playback.ErrActivityQuery)
					return
				}
				var cmd playback.ActivityStop
				recoverOnly := r.Method == "GET"
				if recoverOnly {
					cmd.OperationID = r.PathValue("operationId")
				} else {
					if e = decode(w, r, &cmd); e != nil {
						activityFailure(w, playback.ErrActivityQuery)
						return
					}
					statusCode = 202
				}
				out, e = d.Playback.ActivityStop(ctx, p, scope, r.PathValue("id"), cmd, recoverOnly)
			}
			if e != nil {
				activityFailure(w, e)
				return
			}
			if _, e = d.owner(r); e != nil {
				failure(w, e)
				return
			}
			write(w, statusCode, out)
		})
	}
}
