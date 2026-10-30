package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func activitySummaryFixture(t *testing.T) (Dependencies, map[string]identity.Envelope, http.Handler) {
	t.Helper()
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	tokens := map[string]identity.Envelope{}
	for _, role := range []string{"owner", "admin", "member"} {
		if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, role, role, role+"-profile"); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`UPDATE direct_memberships SET role=? WHERE account_id=?`, role, role); e != nil {
			t.Fatal(e)
		}
		v, e := ident.Issue(role, role+"-profile", "local", role, 1)
		if e != nil {
			t.Fatal(e)
		}
		tokens[role] = v
	}
	d := Dependencies{DB: db, Identity: ident, Administration: administration.New(db)}
	registry := apikit.NewContextual(d.v1Authorize, policyCatalogue())
	if e = registerActivitySummary(registry, d); e != nil {
		t.Fatal(e)
	}
	return d, tokens, registry
}
func activitySummaryRequest(h http.Handler, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/v1/admin/activity", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestActivitySummaryAuthorityQuietAndRevocation(t *testing.T) {
	d, tokens, h := activitySummaryFixture(t)
	for _, role := range []string{"", "member", "admin"} {
		w := activitySummaryRequest(h, tokens[role].AccessToken)
		want := 403
		if role == "" {
			want = 401
		}
		if w.Code != want || strings.Contains(w.Body.String(), "serverName") || strings.Contains(w.Body.String(), "streams") {
			t.Fatalf("%s got %d %s", role, w.Code, w.Body.String())
		}
	}
	owner := tokens["owner"]
	w := activitySummaryRequest(h, owner.AccessToken)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	var out ActivitySummary
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Streams.Count != 0 || len(out.Streams.Items) != 0 || len(out.Scans) != 0 || out.Tasks.Count != 0 || out.Update.Available || out.Health.Degraded {
		t.Fatalf("quiet snapshot: %+v", out)
	}
	if out.Scope.ServerID == "" || out.Scope.ViewerFence == "" || out.ObservedAt == "" {
		t.Fatal("missing authority fence")
	}
	p, e := d.Identity.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.DB.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id IN(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash); e != nil {
		t.Fatal(e)
	}
	if w = activitySummaryRequest(h, owner.AccessToken); w.Code != 401 || strings.Contains(w.Body.String(), "streams") {
		t.Fatal("revocation leaked summary", w.Code, w.Body.String())
	}
	if _, e = d.readActivitySummary(context.Background(), p); e == nil {
		t.Fatal("stale principal reached summary")
	}
}
func TestActivitySummaryActualScanProgressAndTaskCaps(t *testing.T) {
	d, tokens, h := activitySummaryFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := d.DB.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('lib','Movies','movie','/owner-secret-root')`)
	for n := 0; n < 2; n++ {
		id := fmt.Sprint(n)
		exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES(?,'lib',?,?)`, id, "/private/"+id, "/private/"+id)
		exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'lib','running','2026-10-22')`, id)
		exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,discovered,analyzed,inventory_completed_at) VALUES(?,?,1,'inc','root',1,'basic_running',10,4,?)`, id, id, []string{"2026-10-22", ""}[n])
		exec(`INSERT INTO inventory_source_active VALUES(?,?)`, id, id)
	}
	// The console scan receipt wraps these same source jobs. It sorts before
	// real background work so filtering after the three-row cap would fail.
	exec(`INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision,processed) VALUES('scan-wrapper','library-scan','lib','owner','manual','running','basic_running',1,0,0,1,1,'scan-wrapper','','',1,20)`)
	for _, job := range []string{"0", "1"} {
		exec(`INSERT INTO inventory_operation_jobs(operation_id,job_id) VALUES('scan-wrapper',?)`, job)
	}
	for n := 0; n < 5; n++ {
		id := fmt.Sprint(n)
		exec(`INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision,processed) VALUES(?,'test',?,'private-actor','manual','running','working',1,0,1,1,1,'private-domain','','',1,7)`, "task-"+id, "resource-"+id)
	}
	get := func() ActivitySummary {
		w := activitySummaryRequest(h, tokens["owner"].AccessToken)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "owner-secret") || strings.Contains(w.Body.String(), "private-") {
			t.Fatal("non-display/private fields leaked", w.Body.String())
		}
		var out ActivitySummary
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	out := get()
	if len(out.Scans) != 1 || out.Scans[0].Discovered != 20 || out.Scans[0].Analyzed != 8 || out.Scans[0].Total != nil || out.Scans[0].Current != "" {
		t.Fatalf("unknown total must stay unknown: %+v", out.Scans)
	}
	if out.Tasks.Count != 5 || len(out.Tasks.Items) != 3 || out.Tasks.Items[0].Processed == nil || *out.Tasks.Items[0].Processed != 7 || out.Tasks.Items[0].Total != nil {
		t.Fatalf("capped actual tasks: %+v", out.Tasks)
	}
	for _, task := range out.Tasks.Items {
		if task.Kind == "library-scan" {
			t.Fatal("scan duplicated as task", out.Tasks)
		}
	}
	exec(`UPDATE inventory_runs SET inventory_completed_at='2026-10-22'`)
	out = get()
	if out.Scans[0].Total == nil || *out.Scans[0].Total != 20 {
		t.Fatalf("known total: %+v", out.Scans)
	}
	exec(`UPDATE inventory_runs SET finished_at='2026-10-22T01:00:00Z' WHERE job_id='0'`)
	out = get()
	if out.LastScanFinishedAt != "2026-10-22T01:00:00Z" {
		t.Fatalf("last scan missing: %+v", out)
	}
}
func TestActivitySummaryRegistrationIsOwnerScoped(t *testing.T) {
	deps, _, _ := activitySummaryFixture(t)
	r := apikit.NewContextual(deps.v1Authorize, policyCatalogue())
	if e := registerActivitySummary(r, deps); e != nil {
		t.Fatal(e)
	}
	d := r.Definitions()[0]
	if d.Access != apikit.Owner || d.Requires != apikit.ScopeAdmin || d.Admission != laneAdmin {
		t.Fatalf("summary authority changed: %+v", d)
	}
}

func TestActivitySummaryPublishedBounds(t *testing.T) {
	shape, e := apikit.Schema(reflect.TypeFor[ActivitySummary]())
	if e != nil {
		t.Fatal(e)
	}
	props := shape["properties"].(map[string]any)
	for key, want := range map[string]int{"streams": 3, "tasks": 3} {
		fields := props[key].(map[string]any)["properties"].(map[string]any)
		if fields["items"].(map[string]any)["maxItems"] != want || fields["count"].(map[string]any)["minimum"] != 0 {
			t.Fatalf("wire bounds missing for %s: %+v", key, fields)
		}
	}
}

func TestActivitySummaryChecksumTotalMatchesAnalyzedInput(t *testing.T) {
	for _, tc := range []struct {
		name, path, container string
		size, processed       int64
		known                 bool
	}{
		{"local input", "movie.mkv", "mkv", 10000000, 2000000, true},
		{"STRM target bytes exceed descriptor", "movie.STRM", "strm", 100, 2000000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tokens, h := activitySummaryFixture(t)
			c := catalogtest.New(t, d.DB)
			lib := c.Library("lib", "Movies", "movie", "/private-root")
			item := c.Movie(lib, "/private-root/"+tc.path, "Movie", 2026)
			c.Exec(`UPDATE catalog_assets SET container=?,size=? WHERE token=?`, tc.container, tc.size, item.Token)
			c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('job','lib','running','2026-10-22')`)
			c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,discovered,analyzed,inventory_completed_at) VALUES('job','lib',1,'inc','root',1,'advanced_running',1,0,'2026-10-22')`)
			c.Exec(`INSERT INTO inventory_source_active VALUES('lib','job')`)
			c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','lib',?,'inc',?,'revision','{}',?,1)`, item.Token, tc.path, tc.size)
			c.Exec(`INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,processed_bytes,updated_ms) VALUES('job','object','revision',1,'checksum','checksum-v1','running',?,1)`, tc.processed)
			w := activitySummaryRequest(h, tokens["owner"].AccessToken)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var out ActivitySummary
			if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			if out.Tasks.Count != 1 || len(out.Tasks.Items) != 1 {
				t.Fatalf("analysis task missing: %+v", out.Tasks)
			}
			task := out.Tasks.Items[0]
			if task.Processed == nil || *task.Processed != tc.processed || task.Unit != "bytes" || (task.Total != nil) != tc.known {
				t.Fatalf("input progress mismatch: %+v", task)
			}
			if tc.known && *task.Total != tc.size {
				t.Fatalf("local input total: %d", *task.Total)
			}
			if strings.Contains(w.Body.String(), "private-root") || strings.Contains(w.Body.String(), tc.path) {
				t.Fatal("analysis input leaked", w.Body.String())
			}
			// A stale stage must not survive the existing revision fence.
			c.Exec(`UPDATE analysis_stage_runs SET source_revision='stale'`)
			w = activitySummaryRequest(h, tokens["owner"].AccessToken)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			if out.Tasks.Count != 0 || len(out.Tasks.Items) != 0 {
				t.Fatalf("stale input progress exposed: %+v", out.Tasks)
			}
		})
	}
}

func TestActivitySummaryAuthorityChangesDuringOutstandingRead(t *testing.T) {
	for name, mutation := range map[string]string{
		"revoked family":                  `UPDATE authorization_session_families SET revoked=1 WHERE account_id='owner'`,
		"owner demoted":                   `UPDATE direct_memberships SET role='admin' WHERE account_id='owner'`,
		"profile loses primary authority": `UPDATE direct_profiles SET is_primary=0 WHERE id='owner-profile'`,
		"family role changes":             `UPDATE authorization_session_families SET role='admin' WHERE account_id='owner'`,
	} {
		t.Run(name, func(t *testing.T) {
			d, tokens, _ := activitySummaryFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			d.v1 = &playbackv1.Service{DB: d.DB, Now: func() time.Time {
				close(entered)
				<-release
				return time.Now()
			}}
			r := apikit.NewContextual(d.v1Authorize, policyCatalogue())
			if e := registerActivitySummary(r, d); e != nil {
				t.Fatal(e)
			}
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() { response <- activitySummaryRequest(r, tokens["owner"].AccessToken) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("summary never reached the authenticated read")
			}
			if _, e := d.DB.Exec(mutation); e != nil {
				t.Fatal(e)
			}
			// Release once here; the deferred close remains for early failures.
			release <- struct{}{}
			select {
			case w := <-response:
				if w.Code != 401 || strings.Contains(w.Body.String(), "streams") || strings.Contains(w.Body.String(), "serverName") || strings.Contains(w.Body.String(), "current") {
					t.Fatalf("changed authority released private body: %d %s", w.Code, w.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("summary did not finish after authority changed")
			}
		})
	}
}

func TestActivitySummaryThreeSessionWireStillOwnerPrivate(t *testing.T) {
	d, tokens, h := activitySummaryFixture(t)
	now := time.Now().UnixMilli()
	for n := 0; n < 6; n++ {
		id := fmt.Sprintf("activity-session-%d", n)
		presentation := `{"decision":{"video":{"action":"copy","reasons":[]}}}`
		if n < 3 {
			presentation = `{"decision":{"video":{"action":"transcode","reasons":[]}}}`
		}
		if _, e := d.DB.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms,presentation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, "device", "owner", "owner-profile", "local", "key-"+id, "digest", "vod", "local", "playing", "{}", 1, 1, now+3600000, now+int64(n), now, presentation); e != nil {
			t.Fatal(e)
		}
	}
	w := activitySummaryRequest(h, tokens["owner"].AccessToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out ActivitySummary
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Streams.Count != 6 || out.Streams.ConvertingCount != 3 || out.Streams.Overflow.Count != 3 || out.Streams.Overflow.ConvertingCount != 0 || len(out.Streams.Items) != 3 {
		t.Fatalf("wire totals: %+v", out.Streams)
	}
	for n, row := range out.Streams.Items {
		if row.ID != fmt.Sprintf("activity-session-%d", 2-n) || row.User.Username != "owner" || row.StartedAt == "" || !row.Converting {
			t.Fatalf("wire facts: %+v", row)
		}
	}
	if strings.Contains(w.Body.String(), `"url"`) || strings.Contains(w.Body.String(), `"user":{"id":"owner","name"`) {
		t.Fatal("private capability or legacy fallback escaped", w.Body.String())
	}
	for _, role := range []string{"", "member", "admin"} {
		denied := activitySummaryRequest(h, tokens[role].AccessToken)
		if denied.Code != 401 && denied.Code != 403 {
			t.Fatal(role, denied.Code)
		}
		if strings.Contains(denied.Body.String(), "activity-session") || strings.Contains(denied.Body.String(), "streams") {
			t.Fatal("owner facts leaked to", role)
		}
	}
}

func TestActivityMajorIssuesExcludeOrdinaryDiagnosticsAndResolve(t *testing.T) {
	d, tokens, h := activitySummaryFixture(t)
	for _, q := range []string{
		`INSERT INTO remote_sources(id,kind,name,root,state,error_code) VALUES('remote','webdav','Archive','https://example.invalid/private','rate_limited','rate_limited')`,
		`INSERT INTO console_alerts VALUES('overload','server_overloaded','critical','open',1,1,1,1)`,
		`INSERT INTO console_alerts VALUES('scan','scan-failed','warning','open',1,1,1,1)`,
	} {
		if _, e := d.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	read := func() ActivitySummary {
		t.Helper()
		w := activitySummaryRequest(h, tokens["owner"].AccessToken)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out ActivitySummary
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(w.Body.String(), "https://") || strings.Contains(w.Body.String(), "private") {
			t.Fatal("secret/path escaped", w.Body.String())
		}
		return out
	}
	if out := read(); len(out.MajorIssues) != 0 {
		t.Fatal("ordinary alerts/provider waits became major", out.MajorIssues)
	}
	if _, e := d.DB.Exec(`UPDATE remote_sources SET state='offline',error_code='offline' WHERE id='remote'`); e != nil {
		t.Fatal(e)
	}
	out := read()
	if len(out.MajorIssues) != 1 || out.MajorIssues[0].Code != "remote_unreachable" || out.MajorIssues[0].Subject != "Archive" || out.MajorIssues[0].Destination.Target != "remote" || out.MajorIssues[0].Severity != "playback_stop" {
		t.Fatal(out.MajorIssues)
	}
	if _, e := d.DB.Exec(`UPDATE remote_sources SET state='healthy',error_code='' WHERE id='remote'`); e != nil {
		t.Fatal(e)
	}
	if out = read(); len(out.MajorIssues) != 0 {
		t.Fatal("resolved issue retained", out.MajorIssues)
	}
	// The scheduled backup's actual producer raises this alert, not an operation.
	for _, status := range []string{"open", "acknowledged", "resolved"} {
		if _, e := d.DB.Exec(`INSERT INTO console_alerts VALUES('backup','backup-failed','warning',?,1,1,1,1) ON CONFLICT(code) DO UPDATE SET status=excluded.status`, status); e != nil {
			t.Fatal(e)
		}
		out = read()
		if status == "resolved" {
			if len(out.MajorIssues) != 0 {
				t.Fatal("successful backup retained warning", out.MajorIssues)
			}
		} else if len(out.MajorIssues) != 1 || out.MajorIssues[0].Code != "backup_failed" {
			t.Fatal("actual backup failure missing", status, out.MajorIssues)
		}
	}
}

func TestActivityDashboardAdminOnlyProjectionAndCurrentCursorFence(t *testing.T) {
	d, tokens, _ := activitySummaryFixture(t)
	d.v1 = &playbackv1.Service{DB: d.DB}
	r := apikit.NewContextual(d.v1Authorize, policyCatalogue())
	registerPlaybackV1Admin(r, d)
	now := time.Now().UnixMilli()
	for n := 0; n < 12; n++ {
		_, e := d.DB.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES(?,?,'owner','owner-profile','local',?,'digest','audio','owner','paused','{}',1,1,?,?,?)`, fmt.Sprint(n), "dev", fmt.Sprint(n), now+3600000, now+int64(n), now)
		if e != nil {
			t.Fatal(e)
		}
	}
	request := func(role, path string) *httptest.ResponseRecorder {
		q := httptest.NewRequest("GET", path, nil)
		q.Header.Set("Authorization", "Bearer "+tokens[role].AccessToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	if w := request("member", "/v1/admin/sessions"); w.Code != 403 || strings.Contains(w.Body.String(), "totals") {
		t.Fatal("member read Dashboard", w.Code, w.Body.String())
	}
	w := request("admin", "/v1/admin/sessions")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out playbackv1.AdminSessionPage
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Items) != 10 || out.Totals.Streams != 12 || out.Totals.Paused != 12 || out.Scope.ViewerFence == "" || out.Page.NextCursor == "" {
		t.Fatal(out)
	}
	for _, word := range []string{"majorIssues", "serverName", "health", "scans", "tasks", "update"} {
		if strings.Contains(w.Body.String(), `"`+word+`"`) {
			t.Fatal("owner-only fact exposed to admin", word)
		}
	}
	// Another current authority cannot replay the cursor, even on the same server.
	if w = request("owner", "/v1/admin/sessions?cursor="+out.Page.NextCursor); w.Code != 400 || strings.Contains(w.Body.String(), "totals") {
		t.Fatal("cursor crossed authority", w.Code, w.Body.String())
	}
	if w = request("admin", "/v1/admin/sessions?cursor="+out.Page.NextCursor); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestActivityDashboardAuthorityChangesDuringReadDiscardsBody(t *testing.T) {
	d, tokens, _ := activitySummaryFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	d.v1 = &playbackv1.Service{DB: d.DB, Now: func() time.Time { close(entered); <-release; return time.Now() }}
	registry := apikit.NewContextual(d.v1Authorize, policyCatalogue())
	registerPlaybackV1Admin(registry, d)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("GET", "/v1/admin/sessions", nil)
		r.Header.Set("Authorization", "Bearer "+tokens["admin"].AccessToken)
		w := httptest.NewRecorder()
		registry.ServeHTTP(w, r)
		response <- w
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not start")
	}
	if _, e := d.DB.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='admin'`); e != nil {
		t.Fatal(e)
	}
	release <- struct{}{}
	select {
	case w := <-response:
		if w.Code != 401 || strings.Contains(w.Body.String(), "totals") || strings.Contains(w.Body.String(), "items") {
			t.Fatal("revoked administrator received private body", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not return")
	}
}
