package writer

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Runner-only synthetic EFFECT frame. Exact canonical domain table fragments
// plus explicitly proposed fields/children; scope tables are minimal fixtures.
// It does not qualify FK/plan/governor/authority/receipt acceptance or COMMIT.
// Every execution uses its allocated MP descriptor, never an existing ID.
type mpDraftNative struct {
	t    *testing.T
	conn *sql.Conn
	turn *commandTurn
}

func mpNativeFixture(t *testing.T) *mpDraftNative {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 128, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), filepath.Join(t.TempDir(), "mp.sqlite"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	n := &mpDraftNative{t: t, conn: conn}
	for _, table := range []string{"playback_start_preparations", "playback_start_preparation_actor", "playback_start_preparation_source", "playback_start_preparation_queue", "playback_start_preparation_results", "playback_start_preparation_lifecycle", "prepared_media_jobs", "prepared_media_versions", "prepared_media_session_pins"} {
		text := schema.SQL()
		start := strings.Index(text, "CREATE TABLE "+table+"(")
		if start < 0 {
			t.Fatal(table)
		}
		text = text[start:]
		end := strings.Index(text, ";\n")
		if end < 0 {
			t.Fatal(table)
		}
		n.exec(text[:end+1])
	}
	for _, table := range []string{"playback_start_preparations", "prepared_media_jobs"} {
		n.exec("ALTER TABLE " + table + " ADD COLUMN mp_command_incarnation BLOB")
		for _, col := range []string{"mp_command_last_sequence", "mp_command_acknowledged_sequence", "mp_receipt_rows", "mp_receipt_variable_bytes"} {
			n.exec("ALTER TABLE " + table + " ADD COLUMN " + col + " INTEGER NOT NULL DEFAULT 0")
		}
	}
	n.exec("ALTER TABLE prepared_media_versions ADD COLUMN mp_command_incarnation BLOB")
	n.exec(`CREATE TABLE mp_start_execution(operation_id TEXT PRIMARY KEY,execution_id TEXT NOT NULL UNIQUE,physical_exited INTEGER NOT NULL,cleaned INTEGER NOT NULL,exit_digest TEXT NOT NULL,result_digest TEXT NOT NULL,staged_rows INTEGER NOT NULL,staged_bytes INTEGER NOT NULL,cleanup_cursor INTEGER NOT NULL);
 CREATE TABLE mp_job_inputs(job_id TEXT PRIMARY KEY,server_id TEXT,authority TEXT,account_id TEXT,actor_profile_id TEXT,device_id TEXT,family_id TEXT,grant_id TEXT,session_hash TEXT,epoch INTEGER,token_generation INTEGER,original_horizon_ms INTEGER,library_id TEXT,library_source_id TEXT,asset_id TEXT,inventory_object_id TEXT,inventory_revision TEXT,root_identity TEXT,incarnation TEXT,remote_source_id TEXT,effective_configuration TEXT,read_policy_witness TEXT,read_authority_witness TEXT,library_generation INTEGER,raw_configuration_generation INTEGER,profile_revision INTEGER,size INTEGER,modified_ns INTEGER,transformation_digest TEXT,part_index INTEGER);
 CREATE TABLE mp_job_custody(job_id TEXT PRIMARY KEY,generation INTEGER,execution_id TEXT,physical_exited INTEGER,cleaned INTEGER,exit_digest TEXT,cleanup_cursor INTEGER);
 CREATE INDEX prepared_media_readers ON prepared_media_session_pins(version_id);
 CREATE TABLE playback_sessions(id TEXT PRIMARY KEY);
 CREATE TABLE authorization_family_tokens(token_hash TEXT PRIMARY KEY,family_id TEXT,generation INTEGER,retired INTEGER);
 CREATE TABLE authorization_session_families(id TEXT PRIMARY KEY,server_id TEXT,authority TEXT,account_id TEXT,profile_id TEXT,device_id TEXT,epoch INTEGER,current_generation INTEGER,revoked INTEGER,pin_gate INTEGER);
 CREATE TABLE inventory_objects(id TEXT PRIMARY KEY,source_id TEXT,asset_id TEXT,revision TEXT,root_incarnation TEXT,size INTEGER,modified_ns INTEGER,retired INTEGER,state TEXT);
 CREATE TABLE library_sources(id TEXT PRIMARY KEY,library_id TEXT,root_identity TEXT,generation INTEGER,remote_source_id TEXT,incarnation TEXT,enabled INTEGER,identity_confirmed INTEGER,health TEXT);
 CREATE TABLE catalog_assets(id INTEGER PRIMARY KEY,token TEXT UNIQUE,library_id INTEGER,size INTEGER,modified_ns INTEGER,available INTEGER,info_publication_pending INTEGER);
 CREATE TABLE catalog_entities(id INTEGER PRIMARY KEY,public_id BLOB UNIQUE,library_id INTEGER,retired INTEGER);
 CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,library_id TEXT UNIQUE,retired INTEGER);
 CREATE TABLE catalog_asset_links(entity_id INTEGER,asset_id INTEGER,library_id INTEGER,part_index INTEGER,available INTEGER,info_pending_change INTEGER,PRIMARY KEY(entity_id,asset_id));
 CREATE TABLE queues_v1(id TEXT PRIMARY KEY,owner_kind TEXT,owner_id TEXT,revision INTEGER,custody_generation INTEGER,current_entry_id TEXT,authority TEXT,account_id TEXT,profile_id TEXT);
 CREATE TABLE playback_v1_sessions(id TEXT PRIMARY KEY,authority TEXT,account_id TEXT,profile_id TEXT,device_id TEXT,generation INTEGER,ended_ms INTEGER);`)
	a := mpDraftActor()
	n.exec(`INSERT INTO authorization_family_tokens VALUES(?,?,1,0)`, a.sessionHash, a.family)
	n.exec(`INSERT INTO authorization_session_families VALUES(?,?,?,?,?,?,1,1,0,0)`, a.family, a.server, a.authority, a.account, a.profile, a.device)
	n.exec(`INSERT INTO library_sources VALUES('source','library','root',1,NULL,'incarnation',1,1,'ready'); INSERT INTO inventory_objects VALUES('object','source','asset','r1','incarnation',10,20,0,'available'); INSERT INTO catalog_assets VALUES(1,'asset',1,10,20,1,0); INSERT INTO catalog_libraries VALUES(1,'library',0); INSERT INTO catalog_asset_links VALUES(1,1,1,0,1,NULL); INSERT INTO queues_v1 VALUES('queue','device','device',1,1,'entry','local','account','profile')`)
	n.exec(`INSERT INTO catalog_entities VALUES(1,?,1,0)`, make([]byte, 16))
	defs := mpStartPrepDefinitions()
	seen := map[stmt.ID]bool{}
	for _, d := range defs {
		seen[d.ID] = true
	}
	for _, d := range append(mpTranscodeDefinitions(), mpPrepReceiptDefinitions()...) {
		if !seen[d.ID] {
			defs = append(defs, d)
			seen[d.ID] = true
		}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{id: d.ID, work: Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Nanosecond}})
	}
	now := time.Now()
	limit := Work{1000, 1000, 1000, 1000, 20 * time.Millisecond}
	terminal := Work{1, 1, 1, 1, time.Nanosecond}
	quantum, err := NewQuantum(limit, terminal, now)
	if err != nil {
		t.Fatal(err)
	}
	frame := &transaction{writer: &executor{now: func() time.Time { return now }, driverPanics: &driverPanicLog{}}, conn: conn, ctx: t.Context(), custody: &testCustody{}, quantum: quantum, start: now, begun: true}
	n.exec("BEGIN IMMEDIATE")
	t.Cleanup(func() {
		if n.turn != nil {
			_ = n.turn.closeRows()
		}
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	})
	n.turn, err = newCommandTurn(frame, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func (n *mpDraftNative) exec(query string, args ...any) {
	n.t.Helper()
	if _, err := n.conn.ExecContext(n.t.Context(), query, args...); err != nil {
		n.t.Fatal(err)
	}
}
func (n *mpDraftNative) purpose(kind CommandID) *commandTurn {
	n.turn.transaction.descriptor = Descriptor{ID: kind, Durability: Normal}
	return n.turn
}
func mpNativeStart() mpStartPrepBegin {
	return mpStartPrepBegin{rootIncarnation: [16]byte{4}, id: "prep", item: entityid.Encode(make([]byte, 16)), keyDigest: strings.Repeat("d", 64), bodyDigest: strings.Repeat("e", 64), nowMS: 100, deadlineMS: 9000, tokenExpiresMS: 10000, actor: mpDraftActor(), source: mpDraftSource(), queue: mpPrepQueue{id: "queue", entry: "entry", ownerKind: "device", ownerID: "device", revision: 1, custody: 1}, results: mpDraftIDs()}
}
func mpNativeStartFence(r mpStartPrepEffect) mpStartPrepFence {
	return mpStartPrepFence{id: r.preparation, execution: r.execution, revision: r.revision, nowMS: 200, actor: mpDraftActor(), source: mpDraftSource(), queue: mpNativeStart().queue}
}
func mpNativeJob() mpTranscodeCreate {
	return mpTranscodeCreate{rootIncarnation: [16]byte{5}, id: "job", item: 1, nowMS: 100, actor: mpDraftActor(), source: mpDraftSource(), recipe: mpDraftRecipe()}
}
func mpNativeJobFence(r mpTranscodeEffect) mpTranscodeFence {
	return mpTranscodeFence{id: r.job, execution: r.execution, revision: r.revision, generation: r.generation, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, nowMS: 200, actor: mpDraftActor(), source: mpDraftSource(), recipe: mpDraftRecipe()}
}

func TestMPStartPrepNativeLifecycle(t *testing.T) {
	n := mpNativeFixture(t)
	r, err := applyMPStartPrepBegin(t.Context(), n.purpose(MPStartPrepBegin), mpNativeStart())
	if err != nil {
		t.Fatal(err)
	}
	if r.state != "pending" || r.ids != mpDraftIDs() {
		t.Fatal(r)
	}
	r, err = applyMPStartPrepClaim(t.Context(), n.purpose(MPStartPrepClaim), mpNativeStartFence(r), "execution")
	if err != nil {
		t.Fatal(err)
	}
	stale := mpNativeStartFence(r)
	stale.execution = "old"
	if out, e := applyMPStartPrepSeal(t.Context(), n.purpose(MPStartPrepSeal), stale, mpStartPrepCompletion{execution: "old", resultDigest: strings.Repeat("f", 64)}); e == nil || out != (mpStartPrepEffect{}) {
		t.Fatal("old execution", out, e)
	}
	r, err = applyMPStartPrepSeal(t.Context(), n.purpose(MPStartPrepSeal), mpNativeStartFence(r), mpStartPrepCompletion{execution: "execution", resultDigest: strings.Repeat("f", 64), stagedRows: 2, stagedBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	n.exec("UPDATE queues_v1 SET custody_generation=2 WHERE id='queue'")
	if out, e := applyMPStartPrepAccept(t.Context(), n.purpose(MPStartPrepAccept), mpNativeStartFence(r)); e == nil || out != (mpStartPrepEffect{}) {
		t.Fatal("queue drift", out, e)
	}
	n.exec("UPDATE queues_v1 SET custody_generation=1 WHERE id='queue'")
	r, err = applyMPStartPrepAccept(t.Context(), n.purpose(MPStartPrepAccept), mpNativeStartFence(r))
	if err != nil || r.state != "accepted" || r.ids != mpDraftIDs() {
		t.Fatal(r, err)
	}
}
func TestMPStartPrepNativeCancelPhysicalExit(t *testing.T) {
	n := mpNativeFixture(t)
	r, err := applyMPStartPrepBegin(t.Context(), n.purpose(MPStartPrepBegin), mpNativeStart())
	if err != nil {
		t.Fatal(err)
	}
	r, err = applyMPStartPrepClaim(t.Context(), n.purpose(MPStartPrepClaim), mpNativeStartFence(r), "execution")
	if err != nil {
		t.Fatal(err)
	}
	n.exec("UPDATE inventory_objects SET revision='changed'; UPDATE authorization_session_families SET revoked=1")
	f := mpNativeStartFence(r)
	f.nowMS = 11000 // cleanup is allowed after the original horizon
	r, err = applyMPStartPrepRetire(t.Context(), n.purpose(MPStartPrepRetire), f)
	if err != nil || r.stopExecution != "execution" || !r.cleanupPending {
		t.Fatal(r, err)
	}
	var exited, cleaned int64
	if err = n.conn.QueryRowContext(t.Context(), "SELECT physical_exited,cleaned FROM mp_start_execution WHERE operation_id='prep'").Scan(&exited, &cleaned); err != nil || exited != 0 || cleaned != 0 {
		t.Fatal("cancel released custody", exited, cleaned, err)
	}
	f = mpNativeStartFence(r)
	f.nowMS = 11000
	r, err = applyMPStartPrepCleanup(t.Context(), n.purpose(MPStartPrepRetire), f, mpStartPrepCleanup{execution: "execution", exitDigest: strings.Repeat("f", 64), nextCursor: 2, finished: true})
	if err != nil || r.cleanupPending {
		t.Fatal(r, err)
	}
}
