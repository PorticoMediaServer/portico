package scanevents

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func TestScanAnalysisProgressChangesWithStableFoundCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	setupScanLibrary(t, db, "lib", "src", "job")
	insertActive(t, db, "src", "job")
	analyzed := int64(1)
	phase := "inventory"
	p := New(db, func(context.Context, string) (catalog.InventoryStatus, error) {
		return statusWith(catalog.InventorySourceStatus{ID: "src", Status: "running", Discovered: 10, Analyzed: analyzed, Phase: phase}), nil
	}, nil)
	at := time.Now()
	if _, e = p.tick(context.Background(), at); e != nil {
		t.Fatal(e)
	}
	analyzed = 2
	phase = "basic_running"
	if _, e = p.tick(context.Background(), at.Add(100*time.Millisecond)); e != nil {
		t.Fatal(e)
	}
	if len(readScanEvents(t, db)) != 1 {
		t.Fatal("running updates exceeded one per second")
	}
	if _, e = p.tick(context.Background(), at.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	events := readScanEvents(t, db)
	if len(events) != 2 || events[1].data["state"] != "progress" || len(events[1].data) != 2 {
		t.Fatalf("analysis hint missing or viewer payload widened: %+v", events)
	}
}
func TestBackgroundActivityHintsAreAdminOnlyAndRateBound(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if _, e = db.Exec(`INSERT INTO segment_files(object_id,source_revision,library_id,mode,state,updated_ms) VALUES('file','rev','lib','standard','pending',1)`); e != nil {
		t.Fatal(e)
	}
	p := New(db, nil, nil)
	at := time.Now()
	if _, e = p.tickTaskProgress(ctx, at); e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE segment_files SET updated_ms=2`)
	if _, e = p.tickTaskProgress(ctx, at.Add(time.Millisecond)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='job.updated' AND data=''`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("admin hint/rate: %d %v", count, e)
	}
	if _, e = p.tickTaskProgress(ctx, at.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='job.updated' AND data=''`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	db.Exec(`UPDATE segment_files SET state='complete'`)
	due, e := p.tickTaskProgress(ctx, at.Add(1100*time.Millisecond))
	if e != nil || due != nil || len(p.tasks) != 0 {
		t.Fatal("finished task retained timer/state", due, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience<>'admin' OR data<>''`).Scan(&count); e != nil || count != 0 {
		t.Fatal("private task facts leaked", count, e)
	}
}

func TestActivityTaskHintsNeverReachMembers(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO segment_files(object_id,source_revision,library_id,mode,state,updated_ms) VALUES('private-file','r','private-library','standard','pending',1)`); e != nil {
		t.Fatal(e)
	}
	p := New(db, nil, nil)
	h := eventfeed.New(db)
	roles := []string{"member", "admin", "owner"}
	principals, starts := map[string]identity.Principal{}, map[string]string{}
	for _, role := range roles {
		principals[role] = identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: role, ProfileID: role, Role: role}}
		first, e := h.Read(context.Background(), principals[role], role != "member", "")
		if e != nil {
			t.Fatal(e)
		}
		starts[role] = first.NextAfter
	}
	if _, e = p.tickTaskProgress(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, role := range roles {
		out, e := h.Read(context.Background(), principals[role], role != "member", starts[role])
		if e != nil {
			t.Fatal(e)
		}
		want := 1
		if role == "member" {
			want = 0
		}
		if len(out.Events) != want {
			t.Fatalf("%s events: %+v", role, out)
		}
		for _, event := range out.Events {
			if len(event.Data) != 0 || event.Type != "job.updated" {
				t.Fatalf("private fields escaped to %s: %+v", role, event)
			}
		}
	}
}

type activityFilesFunc func(string) string

func (f activityFilesFunc) CurrentActivityFile(job string) string { return f(job) }

func TestActivityCurrentFileHintsArePathlessRateBoundAndSeparateFromScans(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	setupScanLibrary(t, db, "lib", "src", "job")
	insertActive(t, db, "src", "job")
	if _, e = db.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,discovered,analyzed) VALUES('job','src',1,'inc','root',1,'basic_running',10,2)`); e != nil {
		t.Fatal(e)
	}
	file := "private/first.mkv"
	p := New(db, func(context.Context, string) (catalog.InventoryStatus, error) {
		return statusWith(catalog.InventorySourceStatus{ID: "src", Status: "running", Discovered: 10, Analyzed: 2}), nil
	}, nil, activityFilesFunc(func(job string) string {
		if job != "job" {
			t.Errorf("unexpected job %s", job)
		}
		return file
	}))
	ctx, at := context.Background(), time.Now()
	if _, e = p.tick(ctx, at); e != nil {
		t.Fatal(e)
	}
	file = "private/second.mkv"
	if _, e = p.tick(ctx, at.Add(100*time.Millisecond)); e != nil {
		t.Fatal(e)
	}
	var admin, viewer, leaked int
	counts := func() {
		t.Helper()
		if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='job.updated' AND data=''`).Scan(&admin); e != nil {
			t.Fatal(e)
		}
		if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='library:lib'`).Scan(&viewer); e != nil {
			t.Fatal(e)
		}
		if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE data LIKE '%private%' OR resource_id LIKE '%private%' OR revision LIKE '%private%'`).Scan(&leaked); e != nil {
			t.Fatal(e)
		}
	}
	counts()
	if admin != 1 || viewer != 1 || leaked != 0 {
		t.Fatalf("throttled hint: %d admin, %d viewer, %d leaks", admin, viewer, leaked)
	}
	if _, e = p.tick(ctx, at.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	counts()
	if admin != 2 || viewer != 1 || leaked != 0 {
		t.Fatalf("stable counters missed private change or widened viewer feed: %d/%d/%d", admin, viewer, leaked)
	}
	if _, e = p.tick(ctx, at.Add(2*time.Second)); e != nil {
		t.Fatal(e)
	}
	counts()
	if admin != 2 {
		t.Fatal("unchanged file emitted progress", admin)
	}
	file = ""
	if _, e = p.tick(ctx, at.Add(3*time.Second)); e != nil {
		t.Fatal(e)
	}
	counts()
	if admin != 3 || viewer != 1 || leaked != 0 {
		t.Fatalf("current-file clearing was not invalidated: %d/%d/%d", admin, viewer, leaked)
	}
}

func TestActivityEventCommitFailureRetainsRetry(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO segment_files(object_id,source_revision,library_id,mode,state,updated_ms) VALUES('file','rev','lib','standard','pending',1)`,
		`CREATE TRIGGER fail_activity_event BEFORE INSERT ON api_events BEGIN SELECT RAISE(ABORT,'test failure'); END`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	p, at := New(db, nil, nil), time.Now()
	due, e := p.tick(context.Background(), at)
	if e == nil || due == nil || !due.Equal(at.Add(time.Second)) || len(p.tasks) != 0 {
		t.Fatalf("failed commit advanced state or lost retry: %v %v %+v", due, e, p.tasks)
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM api_events`).Scan(&n); e != nil || n != 0 {
		t.Fatal("failed event persisted", n, e)
	}
	if _, e = db.Exec(`DROP TRIGGER fail_activity_event`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.tick(context.Background(), at.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='job.updated'`).Scan(&n); e != nil || n != 1 || len(p.tasks) != 1 {
		t.Fatal("unchanged queue did not retry", n, e, p.tasks)
	}
}

func TestActivityScanConsoleWrapperIsNotABackgroundTaskHint(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, kind := range []string{"library-scan", "generated-playback-cleanup"} {
		if _, e = db.Exec(`INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision,processed) VALUES(?,?,'resource','owner','manual','running','working',1,0,1,1,1,'domain','','',1,0)`, kind, kind); e != nil {
			t.Fatal(e)
		}
	}
	p := New(db, nil, nil)
	if _, e = p.tickTaskProgress(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	if len(p.tasks) != 1 || p.tasks["generated-playback-cleanup"].signature == "" {
		t.Fatalf("scan wrapper counted as background: %+v", p.tasks)
	}
	var id string
	if e = db.QueryRow(`SELECT resource_id FROM api_events WHERE audience='admin' AND type='job.updated'`).Scan(&id); e != nil || id != "generated-playback-cleanup" {
		t.Fatal(id, e)
	}
}
