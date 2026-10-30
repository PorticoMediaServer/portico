package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/connectivity"
	"github.com/PorticoMediaServer/portico/server/internal/diagnostics"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

// LogSettings is the diagnostics projection of the settings document: the saved
// level, the level actually in force, any open debug window, and per-category
// retention.
type LogSettings struct {
	DetailMinutes  operations.LogDetailMinutes `json:"logDetailMinutes,omitempty"`
	Revision       int64                       `json:"revision"`
	Level          string                      `json:"logLevel"`
	EffectiveLevel string                      `json:"effectiveLevel"`
	DebugUntil     string                      `json:"debugWindowUntil,omitempty"`
	Retention      []operations.LogRetention   `json:"retention"`
	Levels         []string                    `json:"levels"`
	Categories     []string                    `json:"categories"`
}

// diagnosticsRoutes registers the message log, the live tail, the debug window,
// client log uploads and the bundle export.
//
// Tier: reads and the debug window need the admin tier, because diagnosing a
// server is exactly what an administrator is for. Writing the log settings and
// exporting the bundle are owner-only: the first is a settings write and the
// second carries the settings document itself.
func (d Dependencies) diagnosticsRoutes(mux *http.ServeMux) {
	recorder := d.Access.Logs
	if recorder == nil {
		return
	}
	d.httpHandle(mux, "GET /v1/admin/logs", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.administrator(r); e != nil {
			accessFailure(w, e)
			return
		}
		query := r.URL.Query()
		limit := 0
		if raw := query.Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil {
				accessFailure(w, access.ErrInvalid)
				return
			}
			limit = n
		}
		out, e := recorder.Read(servicelog.Query{Level: query.Get("level"), Category: query.Get("category"), Cursor: query.Get("cursor"), Limit: limit})
		if e != nil {
			accessFailure(w, fmt.Errorf("%w: %s", access.ErrInvalid, e))
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/admin/logs/events", d.adminLogEvents(recorder, &adminLogStreams{}))
	d.httpHandle(mux, "GET /v1/admin/logs/settings", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.administrator(r); e != nil {
			accessFailure(w, e)
			return
		}
		document, e := d.settingsDocument(r.Context())
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, d.logSettings(document))
	})
	d.httpHandle(mux, "PATCH /v1/admin/logs/settings", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body struct {
			ExpectedRevision int64                       `json:"expectedRevision"`
			OperationID      string                      `json:"operationId"`
			Level            string                      `json:"logLevel"`
			DetailMinutes    operations.LogDetailMinutes `json:"logDetailMinutes,omitempty"`
			Retention        []operations.LogRetention   `json:"retention"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		input, _ := json.Marshal(body)
		document, e := d.applySettingsInput(r, p, body.ExpectedRevision, body.OperationID, input, func(v *operations.Settings) {
			if body.DetailMinutes != 0 {
				v.LogDetailMinutes = body.DetailMinutes
			}
			if body.Level != "" {
				v.LogLevel = body.Level
			}
			if body.Retention != nil {
				v.LogRetention = body.Retention
			}
		})
		if e != nil {
			accessFailure(w, e)
			return
		}
		d.applyLogSettings(document)
		write(w, 200, d.logSettings(document))
	})
	d.httpHandle(mux, "POST /v1/admin/logs/debug-window", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var body struct {
			Minutes     int    `json:"minutes"`
			OperationID string `json:"operationId"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if body.Minutes < 0 || body.Minutes > 240 {
			accessFailure(w, access.ErrInvalid)
			return
		}
		// A debug window is runtime state, not a settings change: raising the
		// level must not bump the settings revision, and it must revert on its
		// own even if nobody asks it to. The expiry is persisted so a restart
		// inside the window keeps debugging rather than silently going quiet.
		until := time.Now().UTC().Add(time.Duration(body.Minutes) * time.Minute)
		if body.Minutes == 0 {
			until = time.Now().UTC().Add(-time.Minute)
		}
		if _, e = dbwork.ExecWrite(r.Context(), d.DB, dbwork.ClassFrom(r.Context(), dbwork.ClassInteractive), `INSERT INTO diagnostics_debug_window VALUES(1,?,?) ON CONFLICT(singleton) DO UPDATE SET expires_ms=excluded.expires_ms,opened_by=excluded.opened_by`, until.UnixMilli(), p.AccountID); e != nil {
			accessFailure(w, e)
			return
		}
		recorder.OpenDebugWindow(until)
		document, e := d.settingsDocument(r.Context())
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, d.logSettings(document))
	})
	d.httpHandle(mux, "GET /v1/admin/diagnostics/bundle", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			accessFailure(w, e)
			return
		}
		document, e := d.settingsDocument(r.Context())
		if e != nil {
			accessFailure(w, e)
			return
		}
		report, _ := diagnostics.Read(r.Context(), d.DB, d.Hosted.Configured())
		page, _ := recorder.Read(servicelog.Query{Level: "debug", Limit: 500})
		status := connectivity.Reporter{Advertiser: d.Access.Advertiser, Certificates: d.certificateStatus()}.Report(r.Context(), connectivity.ProjectPolicy(document))
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="portico-diagnostics.zip"`)
		truncated, e := servicelog.WriteBundle(w, servicelog.BundleInputs{
			Settings: redactSettings(document), Report: report, Connectivity: status, Records: page.Items, Files: recorder.Files()})
		if e != nil {
			// Headers are already out; the archive simply stops.
			return
		}
		_ = truncated
	})
}

// redactSettings removes every field the registry marks secret before the
// settings document enters a bundle that leaves the machine.
func redactSettings(document operations.SettingsDocument) map[string]any {
	raw, err := json.Marshal(document.Effective)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return map[string]any{}
	}
	for _, field := range operations.Registry() {
		if field.Secret {
			delete(out, field.ID)
		}
	}
	out["revision"] = document.Revision
	out["registryRevision"] = document.RegistryRevision
	return out
}

func (d Dependencies) logSettings(document operations.SettingsDocument) LogSettings {
	out := LogSettings{DetailMinutes: document.Effective.LogDetailMinutes, Revision: document.Revision, Level: document.Effective.LogLevel, Retention: document.Effective.LogRetention,
		Levels: servicelog.Levels, Categories: servicelog.Categories}
	if out.Retention == nil {
		out.Retention = []operations.LogRetention{}
	}
	out.EffectiveLevel = out.Level
	if d.Access.Logs != nil {
		effective, until := d.Access.Logs.Effective()
		out.EffectiveLevel = effective
		if !until.IsZero() {
			out.DebugUntil = until.UTC().Format(time.RFC3339)
		}
	}
	return out
}

// applyLogSettings pushes the saved level and retention into the recorder, which
// is what makes a settings save take effect without a restart.
func (d Dependencies) applyLogSettings(document operations.SettingsDocument) {
	if d.Access.Logs == nil {
		return
	}
	d.Access.Logs.SetLevel(document.Effective.LogLevel)
	d.Access.Logs.SetRetention(document.Effective.LogRetentionDays())
}

// applySettings reads the settings document, lets the caller overlay the fields
// its page owns, and applies the result under the caller's expected revision.
// Every administration page writes through this one path, so two pages editing
// different rows of the same document still conflict correctly rather than
// silently overwriting each other.
func (d Dependencies) applySettings(r *http.Request, p identity.Principal, expected int64, operation string, overlay func(*operations.Settings)) (operations.SettingsDocument, error) {
	return d.applySettingsInput(r, p, expected, operation, nil, overlay)
}
func (d Dependencies) applySettingsInput(r *http.Request, p identity.Principal, expected int64, operation string, input json.RawMessage, overlay func(*operations.Settings)) (operations.SettingsDocument, error) {
	if d.Console == nil {
		return operations.SettingsDocument{}, identity.ErrUnauthorized
	}
	if len(operation) < 8 || len(operation) > 128 {
		return operations.SettingsDocument{}, access.ErrInvalid
	}
	current, err := d.settingsDocument(r.Context())
	if err != nil {
		return current, err
	}
	if len(input) == 0 && current.Revision != expected {
		return current, &operations.ConflictError{CurrentRevision: current.Revision}
	}
	values := current.Requested
	overlay(&values)
	change := operations.SettingsChange{ExpectedRevision: expected, IdempotencyKey: operation, Values: values}
	var document operations.SettingsDocument
	if len(input) == 0 {
		document, err = d.Console.ApplySettings(r.Context(), p, d.consoleAuthority(p, true), change)
	} else {
		document, err = d.Console.ApplyLogSettingsInput(r.Context(), p, d.consoleAuthority(p, true), change, input)
	}
	if err == nil && !d.configurationOwnsLiveSettings {
		err = d.applyLiveSettings(r.Context())
	}
	return document, err
}
