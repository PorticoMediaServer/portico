package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

type ActivitySummary struct {
	Scope              playback.ActivityScope      `json:"scope"`
	ObservedAt         string                      `json:"observedAt"`
	ServerName         string                      `json:"serverName"`
	Streams            playbackv1.ActivitySessions `json:"streams"`
	Scans              []ActivitySummaryScan       `json:"scans"`
	Tasks              ActivitySummaryTasks        `json:"tasks"`
	Update             ActivitySummaryUpdate       `json:"update"`
	Health             ActivitySummaryHealth       `json:"health"`
	MajorIssues        []telemetry.MajorIssue      `json:"majorIssues"`
	LastScanFinishedAt string                      `json:"lastScanFinishedAt,omitempty"`
}
type ActivitySummaryScan struct {
	LibraryID   string `json:"libraryId"`
	LibraryName string `json:"libraryName"`
	Kind        string `json:"kind"`
	Discovered  int64  `json:"discovered"`
	Analyzed    int64  `json:"analyzed"`
	Total       *int64 `json:"total,omitempty"`
	Current     string `json:"current,omitempty"`
	Phase       string `json:"phase"`
}
type ActivitySummaryTasks struct {
	Count int64                 `json:"count"`
	Items []ActivitySummaryTask `json:"items"`
}
type ActivitySummaryTask struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	LibraryID   string `json:"libraryId,omitempty"`
	LibraryName string `json:"libraryName,omitempty"`
	State       string `json:"state"`
	Phase       string `json:"phase"`
	Processed   *int64 `json:"processed,omitempty"`
	Total       *int64 `json:"total,omitempty"`
	Unit        string `json:"unit,omitempty"`
}
type ActivitySummaryUpdate struct {
	Available bool   `json:"available"`
	State     string `json:"state"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
}
type ActivitySummaryHealth struct {
	Degraded bool `json:"degraded"`
}

func registerActivitySummary(registry *apikit.Registry, d Dependencies) error {
	if err := registerDashboardStatus(registry, d); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, ActivitySummary]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneAdmin, ID: "get_admin_activity", Method: "GET", Path: "/v1/admin/activity", Summary: "Read the owner's bounded server activity summary", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ActivitySummary, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		p, e := d.ownerContext(ctx, r)
		if e != nil {
			return ActivitySummary{}, foundationAuthError(e)
		}
		if r.URL.RawQuery != "" {
			return ActivitySummary{}, &apierror.Error{Code: "invalid_request"}
		}
		out, e := d.readActivitySummary(ctx, p)
		if e != nil {
			return ActivitySummary{}, foundationAuthError(e)
		}
		if _, e = d.ownerContext(ctx, r); e != nil {
			return ActivitySummary{}, foundationAuthError(e)
		}
		return out, nil
	}})
}
func (d Dependencies) readActivitySummary(ctx context.Context, p identity.Principal) (ActivitySummary, error) {
	out := ActivitySummary{MajorIssues: []telemetry.MajorIssue{}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ServerName: d.Identity.Name(), Scans: []ActivitySummaryScan{}, Tasks: ActivitySummaryTasks{Items: []ActivitySummaryTask{}}}
	out.Scope = playback.ActivityScope{ServerID: d.Identity.ID(), ViewerFence: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}
	service := d.v1
	if service == nil {
		service = &playbackv1.Service{DB: d.DB}
	}
	var e error
	if out.Streams, e = service.ActivitySummarySessions(ctx); e != nil {
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
	if e = d.activitySummaryScans(ctx, tx, &out); e != nil {
		return out, e
	}
	if e = d.activitySummaryTasks(ctx, tx, &out); e != nil {
		return out, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX((SELECT r.finished_at FROM inventory_runs r WHERE r.source_id=s.id AND r.finished_at<>'' ORDER BY r.finished_at DESC LIMIT 1)),'') FROM library_sources s`).Scan(&out.LastScanFinishedAt); e != nil {
		return out, e
	}
	if e = d.activityMajorIssues(ctx, tx, &out); e != nil {
		return out, e
	}
	if e = gate.Commit(); e != nil {
		return out, e
	}
	info := buildinfo.Info()
	current := administration.UpdateBuild{Version: info["version"], BuildID: info["buildId"], SourceDigest: info["sourceDigest"], BuiltAt: info["builtAt"]}
	out.Update = ActivitySummaryUpdate{State: "unconfigured", Current: current.Version}
	if d.Administration != nil {
		channel, feed := d.updatePolicy(ctx, p)
		u := d.Administration.CachedUpdates(ctx, current, channel, feed)
		out.Update = ActivitySummaryUpdate{Available: u.UpdateAvailable, State: u.State, Current: u.Current.Version}
		if u.Latest != nil {
			out.Update.Latest = u.Latest.Version
		}
	}
	if d.Watchdog != nil {
		out.Health.Degraded = d.Watchdog.Report().State != dbwork.HealthyState
	}
	out.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return out, nil
}
func (d Dependencies) activitySummaryScans(ctx context.Context, tx *sql.Tx, out *ActivitySummary) error {
	rows, e := tx.QueryContext(ctx, `SELECT l.id,l.name,l.kind,r.job_id,r.phase,r.discovered,r.analyzed,r.inventory_completed_at
 FROM inventory_source_active a CROSS JOIN inventory_runs r ON r.job_id=a.job_id CROSS JOIN jobs j ON j.id=r.job_id AND j.status IN('queued','running') CROSS JOIN library_sources s ON s.id=a.source_id CROSS JOIN libraries l ON l.id=s.library_id ORDER BY l.id,r.job_id`)
	if e != nil {
		return e
	}
	defer rows.Close()
	indexes := map[string]int{}
	unknown := map[string]bool{}
	for rows.Next() {
		var id, name, kind, job, phase, complete string
		var discovered, analyzed int64
		if e = rows.Scan(&id, &name, &kind, &job, &phase, &discovered, &analyzed, &complete); e != nil {
			return e
		}
		n, ok := indexes[id]
		if !ok {
			n = len(out.Scans)
			indexes[id] = n
			total := int64(0)
			out.Scans = append(out.Scans, ActivitySummaryScan{LibraryID: id, LibraryName: name, Kind: kind, Phase: phase, Total: &total})
		}
		scan := &out.Scans[n]
		scan.Discovered += discovered
		scan.Analyzed += analyzed
		if complete == "" {
			unknown[id] = true
			scan.Phase = phase
		}
		if scan.Total != nil {
			*scan.Total += discovered
		}
		if d.Ingestion != nil {
			if file := d.Ingestion.CurrentActivityFile(job); file != "" && scan.Current == "" {
				scan.Current = file
			}
		}
	}
	for n := range out.Scans {
		if unknown[out.Scans[n].LibraryID] {
			out.Scans[n].Total = nil
		}
	}
	return rows.Err()
}

// Only work queues are read here, never a library/inventory walk. Source joins
// start at the active operations/stages; item enrichment stays capped at three.
const activityTasksCTE = `WITH tasks AS (
 SELECT c.id,c.kind,COALESCE(l.id,'') library_id,COALESCE(l.name,'') library_name,c.state,c.phase,
 CASE WHEN p.id IS NOT NULL THEN p.done+p.failed ELSE c.processed END processed,
 CASE WHEN p.selection_complete=1 THEN p.total ELSE (SELECT total FROM console_operation_totals WHERE operation_id=c.id) END total,CASE WHEN p.id IS NOT NULL THEN 'items' ELSE '' END unit,c.created_ms ordering
 FROM console_operations c INDEXED BY console_active_work LEFT JOIN libraries l ON l.id=c.resource LEFT JOIN personal_jobs p ON p.id=c.resource
 WHERE c.state IN ('queued','running','cancellation-requested','reconciling','paused')
 AND c.kind<>'library-scan'
 UNION ALL
 SELECT 'analysis:'||a.job_id||':'||a.object_id||':'||a.stage,a.stage,l.id,l.name,'running',a.stage,a.processed_bytes,
 CASE WHEN a.stage='checksum' AND lower(substr(o.relative_path,-5))<>'.strm' AND asset.container<>'strm' THEN o.size END,'bytes',a.updated_ms
 FROM inventory_source_active active CROSS JOIN inventory_runs r ON r.job_id=active.job_id
 CROSS JOIN jobs j ON j.id=r.job_id AND j.status='running'
 CROSS JOIN analysis_stage_runs a ON a.job_id=r.job_id AND a.state='running' AND a.policy_revision=r.policy_revision
 CROSS JOIN inventory_objects o ON o.id=a.object_id AND o.revision=a.source_revision
 CROSS JOIN catalog_assets asset ON asset.token=o.asset_id
 CROSS JOIN library_sources s ON s.id=r.source_id CROSS JOIN libraries l ON l.id=s.library_id
 UNION ALL
 SELECT 'segments:'||a.library_id,'segments',a.library_id,COALESCE(l.name,''),'queued','segment_evidence',NULL,NULL,'files',a.min_updated_ms
 FROM segment_pending_activity a LEFT JOIN libraries l ON l.id=a.library_id
 ) `

func (d Dependencies) activitySummaryTasks(ctx context.Context, tx *sql.Tx, out *ActivitySummary) error {
	if e := tx.QueryRowContext(ctx, activityTasksCTE+`SELECT count(*) FROM tasks`).Scan(&out.Tasks.Count); e != nil {
		return e
	}
	rows, e := tx.QueryContext(ctx, activityTasksCTE+`SELECT id,kind,library_id,library_name,state,phase,processed,total,unit FROM tasks ORDER BY ordering,id LIMIT 3`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var v ActivitySummaryTask
		if e = rows.Scan(&v.ID, &v.Kind, &v.LibraryID, &v.LibraryName, &v.State, &v.Phase, &v.Processed, &v.Total, &v.Unit); e != nil {
			return e
		}
		out.Tasks.Items = append(out.Tasks.Items, v)
	}
	return rows.Err()
}

// This is an owner-only projection of measured state, not Attention's wider
// diagnostic list. Unknown/unconfigured remote, backup or certificate facts
// cannot manufacture a warning. Future remote producers use the same shape.
func (d Dependencies) activityMajorIssues(ctx context.Context, tx *sql.Tx, out *ActivitySummary) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.id,COALESCE(NULLIF(s.name,''),l.name) FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.enabled=1 AND s.health IN('offline','root_changed') ORDER BY s.id LIMIT 20`)
	if err != nil {
		return err
	}
	unreachable := []telemetry.NamedRecord{}
	for rows.Next() {
		var v telemetry.NamedRecord
		if err = rows.Scan(&v.ID, &v.Name); err != nil {
			rows.Close()
			return err
		}
		unreachable = append(unreachable, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var backupFailed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_alerts WHERE code='backup-failed' AND status!='resolved')`).Scan(&backupFailed); err != nil {
		return err
	}
	nearlyFull := false
	if free, total, ok := telemetry.VolumeUsage(d.stateDirectory()); ok && total > 0 {
		nearlyFull = float64(free)/float64(total) < telemetry.StorageWarningRatio
	}
	renewalFailed := false
	if d.Networking != nil && d.Networking.Certificates() != nil {
		status, e := d.Networking.Certificates().Status(ctx)
		if e == nil && status.Config.Enabled && status.NotAfter != nil && status.ErrorCode != "" && status.RenewAt != nil && !status.RenewAt.After(time.Now()) {
			renewalFailed = true
		}
	}
	out.MajorIssues = telemetry.MajorIssues(unreachable, nearlyFull, backupFailed, renewalFailed)
	remotes, e := tx.QueryContext(ctx, `SELECT id,name FROM remote_sources WHERE removed=0 AND state='offline' ORDER BY id LIMIT 20`)
	if e != nil {
		return e
	}
	defer remotes.Close()
	for remotes.Next() {
		var id, name string
		if e = remotes.Scan(&id, &name); e != nil {
			return e
		}
		if len(out.MajorIssues) < 20 {
			out.MajorIssues = append(out.MajorIssues, telemetry.MajorIssue{Kind: "remote-source", Code: "remote_unreachable", Severity: "playback_stop", Subject: name, Destination: telemetry.Action{Kind: "remote-source", Target: id}})
		}
	}
	if e = remotes.Err(); e != nil {
		return e
	}
	return nil
}

// Dashboard exposes only playback facts to scoped administrators. Neither an
// owner-only panel nor a stale profile's paging cursor can cross this boundary.
func (d Dependencies) dashboardSessions(ctx context.Context, r *http.Request, limit int, cursor string) (playbackv1.AdminSessionPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	metadata := apikit.Metadata{Audience: apikit.Clients, Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "playback"}
	admitted, err := d.v1Authorize(ctx, r, metadata)
	if err != nil {
		return playbackv1.AdminSessionPage{}, err
	}
	p := callerFrom(admitted).Principal
	scope := playback.ActivityScope{ServerID: d.Identity.ID(), ViewerFence: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}
	if cursor != "" {
		prefix := scope.ViewerFence + "~"
		if !strings.HasPrefix(cursor, prefix) {
			return playbackv1.AdminSessionPage{}, &playbackv1.FieldError{Path: "cursor"}
		}
		cursor = strings.TrimPrefix(cursor, prefix)
	}
	out, err := d.playbackV1().AdminSessions(admitted, limit, cursor)
	if err != nil {
		return out, err
	}
	current, err := d.v1Authorize(ctx, r, metadata)
	if err != nil {
		return playbackv1.AdminSessionPage{}, err
	}
	fresh := callerFrom(current).Principal
	if presentationIdentity(p) != presentationIdentity(fresh) || p.Epoch != fresh.Epoch {
		return playbackv1.AdminSessionPage{}, &apierror.Error{Code: "not_permitted"}
	}
	out.Scope = scope
	if out.Page.NextCursor != "" {
		out.Page.NextCursor = scope.ViewerFence + "~" + out.Page.NextCursor
	}
	return out, nil
}
