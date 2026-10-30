package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strconv"
	"strings"
	"time"
)

func (d Dependencies) consoleAuthority(p identity.Principal, owner bool) operations.Authorize {
	return func(ctx context.Context, tx *sql.Tx, item string) error {
		current, e := d.playbackAuthorityTx(ctx, tx, p, item)
		if e != nil {
			// A title outside this profile's limits is answered exactly as a
			// title that does not exist: a report about it, or a question
			// about it, must not be a way to learn that it is there.
			if item != "" && errors.Is(e, identity.ErrContentRestricted) {
				return identity.ErrNotVisible
			}
			return e
		}
		if owner {
			return d.ownerAuthorityTx(ctx, tx, current)
		}
		return nil
	}
}
func consoleError(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	if revisionFailure(w, e) {
		return
	}
	status, code, message := 503, "console_unavailable", "The server could not complete this request. Retry with the same operation key."
	switch {
	// CD-51: a refused but valid session is 403 (or a hidden 404), never 401.
	case errors.Is(e, identity.ErrNotVisible):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, identity.ErrForbidden):
		status, code, message = 403, "forbidden", identity.ErrForbidden.Error()
	case errors.Is(e, identity.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Authentication is required."
	case errors.Is(e, operations.ErrInvalid):
		status, code, message = 400, "invalid_console_request", "Check the submitted fields and their limits."
	case errors.Is(e, operations.ErrIdempotencyKeyReused):
		status, code, message = 409, "idempotency_key_reused", "This request conflicts with an earlier request."
	case errors.Is(e, operations.ErrConflict):
		status, code, message = 409, "console_conflict", publicErrorMessage("console_conflict")
	case errors.Is(e, operations.ErrCapacity):
		status, code, message = 429, "console_capacity", publicErrorMessage("console_capacity")
	case errors.Is(e, operations.ErrExpired):
		status, code, message = 410, "console_expired", publicErrorMessage("console_expired")
	case errors.Is(e, sql.ErrNoRows):
		status, code, message = 404, "not_found", "This record is not available in the selected scope."
	}
	if status == 429 {
		w.Header().Set("Retry-After", "60")
	}
	detail := map[string]any{"code": code, "message": message, "retryable": status == 503 || status == 429}
	var conflict *operations.ConflictError
	if errors.As(e, &conflict) {
		detail["currentRevision"] = conflict.CurrentRevision
	}
	var fields *operations.ValidationError
	if errors.As(e, &fields) {
		detail["fields"] = fields.Fields
	}
	consoleWrite(w, status, map[string]any{"error": detail})
}

// Console responses have an explicit length for bounded native fetch readers.
func consoleWrite(w http.ResponseWriter, status int, v any) {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 1<<20 {
		status = 503
		b = []byte(`{"error":{"code":"console_unavailable","message":"Console response exceeded its bound."}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func consoleQuery(r *http.Request, allowed ...string) error {
	for k, v := range r.URL.Query() {
		ok := false
		for _, a := range allowed {
			ok = ok || k == a
		}
		if !ok || len(v) != 1 || len(v[0]) > 160 {
			return operations.ErrInvalid
		}
	}
	return nil
}
func consoleLimit(r *http.Request) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 40, nil
	}
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 || n > 40 {
		return 0, operations.ErrInvalid
	}
	return n, nil
}
func (d Dependencies) consoleRoutes(mux *http.ServeMux) {
	if d.Console == nil {
		return
	}
	slots := make(chan struct{}, 8)
	type action func(*http.Request, identity.Principal, operations.Authorize) (any, error)
	register := func(pattern string, owner bool, query []string, fn action) {
		if d.registry != nil && (pattern == "GET /v1/admin/console/settings" || pattern == "PATCH /v1/admin/console/settings") {
			return
		}
		d.projectedHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			var p identity.Principal
			var e error
			if owner {
				p, e = d.owner(r)
			} else {
				p, e = d.principal(r)
			}
			if e != nil {
				consoleError(w, e)
				return
			}
			if e = consoleQuery(r, query...); e != nil {
				consoleError(w, e)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				consoleError(w, operations.ErrCapacity)
				return
			}
			auth := d.consoleAuthority(p, owner)
			out, e := fn(r, p, auth)
			if e != nil {
				consoleError(w, e)
				return
			}
			// Recheck after observations/export construction; stale-scope data is dropped.
			if e = d.ConsoleCheck(ctx, auth); e != nil {
				consoleError(w, e)
				return
			}
			consoleWrite(w, 200, map[string]any{"scope": map[string]string{"serverId": d.Identity.ID(), "viewerFence": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}, "data": out})
		})
	}
	decodeBody := func(r *http.Request, v any) error {
		var raw json.RawMessage
		if e := decode(nil, r, &raw); e != nil {
			return &operations.ValidationError{Fields: []string{"request"}}
		}
		// Jobs/playback and Home are owned by other lanes.
		if !strings.Contains(r.URL.Path, "/jobs") && !strings.Contains(r.URL.Path, "/streams") && !strings.Contains(r.URL.Path, "/home") {
			if err := requireRevisionJSON(raw, v); err != nil {
				return err
			}
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if e := dec.Decode(v); e != nil {
			return &operations.ValidationError{Fields: []string{"request"}}
		}
		// A complete document write must not silently default an omitted boolean or
		// clear an omitted override. Nullable values still have to be explicit.
		var top map[string]json.RawMessage
		if json.Unmarshal(raw, &top) != nil || top == nil {
			return operations.ErrInvalid
		}
		required := []string{}
		var values map[string]json.RawMessage
		switch v.(type) {
		case *operations.ScheduleChange:
			required = []string{"id", "kind", "resource", "enabled", "timezone", "startMinute", "windowMinutes", "catchUp"}
			_ = json.Unmarshal(top["value"], &values)
		case *operations.PreferenceChange:
			// A preference patch is a merge over the registry: only the submitted
			// keys change, and an explicit null clears that scope's override.
			if _, ok := top["values"]; !ok {
				return &operations.ValidationError{Fields: []string{"values"}}
			}
		}
		for _, field := range required {
			value, ok := values[field]
			nullable := field == "perAccountCap" || field == "serverCap"
			if !ok || !nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return &operations.ValidationError{Fields: []string{"values." + field}}
			}
		}
		return nil
	}
	register("GET /v1/preferences", false, []string{"deviceClass"}, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.Preferences(r.Context(), p, r.URL.Query().Get("deviceClass"), a)
	})
	register("PATCH /v1/preferences", false, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.PreferenceChange
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.ApplyPreferences(r.Context(), p, a, c)
	})
	register("GET /v1/notifications", false, []string{"cursor", "limit"}, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		n, e := consoleLimit(r)
		if e != nil {
			return nil, e
		}
		return d.Console.Inbox(r.Context(), p, a, r.URL.Query().Get("cursor"), n)
	})
	register("POST /v1/notifications/{id}/read", false, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var body struct{}
		if e := decodeBody(r, &body); e != nil {
			return nil, e
		}
		return map[string]bool{"read": true}, d.Console.ReadNotice(r.Context(), p, a, r.PathValue("id"))
	})
	if d.Measurements != nil {
		register("GET /v1/admin/console/panels/{panel}", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
			return d.Measurements.Read(r.Context(), r.PathValue("panel"))
		})
	}
	register("GET /v1/admin/console/stream-options", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.StreamOptions(r.Context(), a)
	})
	register("GET /v1/admin/console/accounts", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.LocalAccountOptions(r.Context(), a)
	})
	register("GET /v1/admin/console/alerts", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.Alerts(r.Context(), a)
	})
	register("POST /v1/admin/console/alerts/{id}/acknowledge", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.JobCommand
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return map[string]bool{"acknowledged": true}, d.Console.Acknowledge(r.Context(), p, a, r.PathValue("id"), c)
	})
	register("GET /v1/admin/console/diagnostics/{lane}", true, []string{"cursor"}, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.Records(r.Context(), p, a, r.PathValue("lane"), r.URL.Query().Get("cursor"))
	})
	register("POST /v1/admin/console/diagnostics/capture", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.Capture
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return c, d.Console.Capture(r.Context(), p, a, c)
	})
	register("POST /v1/admin/console/exports", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.ExportRequest
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.CreateExport(r.Context(), p, a, d.Hosted != nil && d.Hosted.Configured(), c)
	})
	register("GET /v1/admin/console/exports/{id}", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.Export(r.Context(), p, a, r.PathValue("id"))
	})
	if d.Scheduler == nil {
		return
	}
	register("GET /v1/admin/console/job-kinds", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Scheduler.Kinds(), nil
	})
	register("GET /v1/admin/console/jobs", true, []string{"cursor"}, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Scheduler.Jobs(r.Context(), a, r.URL.Query().Get("cursor"))
	})
	register("POST /v1/admin/console/jobs", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.RunJob
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Scheduler.Enqueue(r.Context(), p, a, c)
	})
	register("POST /v1/admin/console/jobs/{id}/{action}", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.JobCommand
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Scheduler.Command(r.Context(), p, a, r.PathValue("id"), r.PathValue("action"), c)
	})
	register("GET /v1/admin/console/schedules", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Scheduler.Schedules(r.Context(), a)
	})
	register("PUT /v1/admin/console/schedules", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.ScheduleChange
		if e := decodeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Scheduler.SaveSchedule(r.Context(), p, a, c)
	})
}
func (d Dependencies) ConsoleCheck(ctx context.Context, a operations.Authorize) error {
	gated, e := dbwork.BeginSnapshot(ctx, d.DB)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return a(ctx, tx, "")
}
