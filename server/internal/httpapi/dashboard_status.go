package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"time"
)

type DashboardAlert struct {
	Code     string            `json:"code"`
	Severity string            `json:"severity"`
	Params   map[string]string `json:"params"`
	Count    *int64            `json:"count,omitempty"`
	Action   telemetry.Action  `json:"action"`
	Major    bool              `json:"major"`
}
type DashboardStatus struct {
	ObservedAt string                `json:"observedAt"`
	Alerts     []DashboardAlert      `json:"alerts"`
	Scans      []ActivitySummaryScan `json:"scans"`
	Tasks      ActivitySummaryTasks  `json:"tasks"`
}

func registerDashboardStatus(registry *apikit.Registry, d Dependencies) error {
	return apikit.RegisterRaw(registry, apikit.RawRoute[struct{}, DashboardStatus]{Metadata: apikit.Metadata{Audience: apikit.Clients,
		ID: "get_admin_dashboard_status", Method: "GET", Path: "/v1/admin/dashboard/status", Summary: "Read qualified owner Dashboard alerts and tasks",
		Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"}},
		Serve: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			out, e := d.readDashboardStatus(r.Context(), r)
			if e != nil {
				consoleError(w, e)
				return
			}
			write(w, 200, out)
		}})
}
func (d Dependencies) readDashboardStatus(ctx context.Context, r *http.Request) (DashboardStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	p, e := d.ownerContext(ctx, r)
	if e != nil {
		return DashboardStatus{}, e
	}
	if r.URL.RawQuery != "" {
		return DashboardStatus{}, operations.ErrInvalid
	}
	activity, e := d.readActivitySummary(ctx, p)
	if e != nil {
		return DashboardStatus{}, e
	}
	out := DashboardStatus{ObservedAt: activity.ObservedAt, Alerts: []DashboardAlert{}, Scans: activity.Scans, Tasks: activity.Tasks}
	for _, issue := range activity.MajorIssues {
		out.Alerts = append(out.Alerts, DashboardAlert{Code: issue.Code, Severity: "critical", Params: map[string]string{"subject": issue.Subject}, Action: issue.Destination, Major: true})
	}
	if activity.Update.Available {
		out.Alerts = append(out.Alerts, DashboardAlert{Code: "update_available", Severity: "info", Params: map[string]string{"current": activity.Update.Current, "latest": activity.Update.Latest}, Action: telemetry.Action{Kind: "update", Target: ""}})
	}
	document, e := d.settingsDocument(ctx)
	if e != nil {
		return out, e
	}
	gate, e := dbwork.BeginSnapshot(ctx, d.DB)
	if e != nil {
		return out, e
	}
	defer gate.Rollback()
	tx := gate.Tx()
	if e = d.ownerAuthorityTx(ctx, tx, p); e != nil {
		return out, e
	}
	var permission bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_alerts WHERE code='state-permissions' AND status='open')`).Scan(&permission); e != nil {
		return out, e
	}
	if permission {
		out.Alerts = append(out.Alerts, DashboardAlert{Code: "state_permissions", Severity: "warning", Params: map[string]string{}, Action: telemetry.Action{Kind: "storage", Target: "permissions"}})
	}
	if document.Effective.UploadCapacityKbps > 0 {
		var count int64
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_v1_sessions INDEXED BY playback_v1_sessions_active WHERE ended_ms=0 AND lease_expires_ms>? AND location='remote' AND state IN('playing','buffering') AND upload_limited=1`, time.Now().UnixMilli()).Scan(&count); e != nil {
			return out, e
		}
		if count > 0 {
			out.Alerts = append(out.Alerts, DashboardAlert{Code: "upload_budget_exhausted", Severity: "warning", Params: map[string]string{}, Count: &count, Action: telemetry.Action{Kind: "streaming", Target: "uploadSpeed"}})
		}
	}
	if e = gate.Commit(); e != nil {
		return out, e
	}
	if _, e = d.ownerContext(ctx, r); e != nil {
		return out, e
	}
	return out, nil
}
