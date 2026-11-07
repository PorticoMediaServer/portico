package writer

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Standard-runner target only. This is synthetic SQL fence evidence, not a
// qualified F fixture, public command, real supervisor exit or Applied receipt.
// No existing command ID is borrowed and no descriptor is mutated.
type mpHLSCanonicalSQLFixture struct {
	t        *testing.T
	conn     *sql.Conn
	registry *stmt.Registry
	steps    []statementStep
	now      time.Time
}

func mpHLSCanonicalSQL(t *testing.T) *mpHLSCanonicalSQLFixture {
	t.Helper()
	db, _, err := sqliteconn.Open(t.Context(), filepath.Join(t.TempDir(), "hls.sqlite"), sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 128, Synchronous: sqliteconn.Normal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	f := &mpHLSCanonicalSQLFixture{t: t, conn: conn, now: time.Now()}
	f.exec(`CREATE TABLE playback_sessions(id TEXT PRIMARY KEY); INSERT INTO playback_sessions VALUES('media')`)
	ddl := schema.SQL()
	start := strings.Index(ddl, "CREATE TABLE playback_hls_windows(")
	if start < 0 {
		t.Fatal("native table missing")
	}
	ddl = ddl[start:]
	end := strings.Index(ddl, ";\n")
	f.exec(ddl[:end+1])
	// Explicit queryable F projection only; no owner/header/birth graph fixture.
	for _, column := range []string{"mp_physical_generation INTEGER NOT NULL DEFAULT 0", "mp_execution_id TEXT NOT NULL DEFAULT ''", "mp_source_request_id TEXT NOT NULL DEFAULT ''", "mp_writes_retired INTEGER NOT NULL DEFAULT 0", "mp_exit_confirmed INTEGER NOT NULL DEFAULT 0 CHECK(mp_exit_confirmed IN(0,1) AND mp_exit_confirmed<=mp_writes_retired)", "mp_execution_generation INTEGER NOT NULL DEFAULT 0", "mp_revision INTEGER NOT NULL DEFAULT 0", "mp_end_index INTEGER NOT NULL DEFAULT 0", "mp_plan_digest TEXT NOT NULL DEFAULT ''", "mp_cleaned INTEGER NOT NULL DEFAULT 0", "mp_exit_digest TEXT NOT NULL DEFAULT ''", "mp_cleanup_cursor INTEGER NOT NULL DEFAULT 0"} {
		f.exec("ALTER TABLE playback_hls_windows ADD COLUMN " + column)
	}
	f.exec(`CREATE UNIQUE INDEX mp_hls_window_generation_test ON playback_hls_windows(session_id,start_index,mp_execution_generation);
 CREATE TABLE mp_hls_plans(session_id TEXT PRIMARY KEY,plan_digest TEXT,backend TEXT);
 CREATE TABLE mp_hls_renditions(session_id TEXT,rendition_id TEXT,plan_digest TEXT,init_name TEXT,PRIMARY KEY(session_id,rendition_id));
 CREATE TABLE mp_hls_window_renditions(session_id TEXT,window_start INTEGER,window_generation INTEGER,rendition_id TEXT,highest_produced INTEGER,PRIMARY KEY(session_id,window_start,window_generation,rendition_id),FOREIGN KEY(session_id,window_start,window_generation) REFERENCES playback_hls_windows(session_id,start_index,mp_execution_generation) ON DELETE NO ACTION ON UPDATE NO ACTION);
 CREATE TABLE mp_hls_artifacts(session_id TEXT,rendition_id TEXT,segment_index INTEGER,kind INTEGER,name TEXT,digest TEXT,size INTEGER,start_us INTEGER,end_us INTEGER,decode_from_us INTEGER,window_start INTEGER,window_generation INTEGER,execution_id TEXT,occurrence_generation INTEGER,plan_digest TEXT,status TEXT,PRIMARY KEY(session_id,rendition_id,segment_index,kind),FOREIGN KEY(session_id,window_start,window_generation) REFERENCES playback_hls_windows(session_id,start_index,mp_execution_generation) ON DELETE NO ACTION ON UPDATE NO ACTION);`)
	digest := strings.Repeat("a", 64)
	f.exec(`INSERT INTO mp_hls_plans VALUES('media',?,'software')`, digest)
	f.exec(`INSERT INTO mp_hls_renditions VALUES('media','main',?,'')`, digest)
	defs := mpHLSDomainDefinitions()
	f.registry, err = stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		f.steps = append(f.steps, statementStep{id: d.ID, work: Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Nanosecond}})
	}
	f.exec("BEGIN IMMEDIATE")
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") })
	return f
}
func (f *mpHLSCanonicalSQLFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *mpHLSCanonicalSQLFixture) turn(id CommandID) *commandTurn {
	f.t.Helper()
	q, err := NewQuantum(Work{Rows: 1000, VMOps: 1000, Pages: 1000, WALBytes: 1000, Hold: 20 * time.Millisecond}, Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Nanosecond}, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	frame := &transaction{writer: &executor{now: func() time.Time { return f.now }, driverPanics: &driverPanicLog{}}, conn: f.conn, ctx: f.t.Context(), custody: &testCustody{}, quantum: q, start: f.now, begun: true, descriptor: Descriptor{ID: id, Durability: Normal}}
	turn, err := newCommandTurn(frame, f.registry, f.steps)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func TestMPHLSDomainNativeCanonicalWindowExecutionFence(t *testing.T) {
	f := mpHLSCanonicalSQL(t)
	ctx := t.Context()
	digest := strings.Repeat("a", 64)
	exit := strings.Repeat("b", 64)
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowClaim), "mp.hls.window_insert", "media", int64(10), int64(9), int64(100), int64(4), "old", "source-request", int64(11), digest); err != nil {
		t.Fatal(err)
	}
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowCancel), "mp.hls.window_cancel", int64(1), "media", int64(10), int64(1), "old", int64(1), "source-request"); err != nil {
		t.Fatal(err)
	}
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowExit), "mp.hls.window_exit", exit, int64(0), int64(1), "media", int64(10), int64(1), "old", int64(2), int64(0), exit, "source-request", int64(4)); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO mp_hls_window_renditions VALUES('media',10,1,'main',9)`)
	reclaim := []any{int64(200), "new", int64(4), int64(11), digest, "source-request", "media", int64(10), int64(3), int64(1), "old", int64(4), "source-request"}
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowClaim), "mp.hls.window_reclaim", reclaim...); !errors.Is(err, ErrConflict) {
		t.Fatal("reclaimed before old children were removed", err)
	}
	if err := mpPrepOne(ctx, f.turn(MPHLSReconcile), "mp.hls.window_progress_delete", "media", int64(10), int64(1), "main", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowClaim), "mp.hls.window_reclaim", reclaim...); err != nil {
		t.Fatal(err)
	}
	// Delayed producer and delayed exit retain the OLD execution tuple. Neither
	// may borrow a current tuple from the row after a same-key restart.
	oldArtifact := []any{"media", "main", int64(10), int64(mpMediaSegment), "segment-10.ts", strings.Repeat("c", 64), int64(100), int64(0), int64(4000000), int64(0), int64(10), int64(1), "old", int64(4), digest, "media", "main", int64(mpMediaSegment), "segment-10.ts", int64(mpMediaSegment), int64(10), int64(1), "old", int64(3), "source-request", int64(4)}
	if err := mpPrepOne(ctx, f.turn(MPHLSArtifactPublish), "mp.hls.artifact_insert", oldArtifact...); !errors.Is(err, ErrConflict) {
		t.Fatal("old producer published into successor", err)
	}
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowExit), "mp.hls.window_exit", exit, int64(0), int64(1), "media", int64(10), int64(1), "old", int64(3), int64(0), exit, "source-request", int64(4)); !errors.Is(err, ErrConflict) {
		t.Fatal("old exit settled successor", err)
	}
	var count, generation, revision, exited, cleaned, artifacts int64
	var execution, status, request string
	if err := f.conn.QueryRowContext(ctx, `SELECT count(*),mp_execution_generation,mp_revision,mp_exit_confirmed,mp_cleaned,mp_execution_id,status,mp_source_request_id FROM playback_hls_windows WHERE session_id='media' AND start_index=10`).Scan(&count, &generation, &revision, &exited, &cleaned, &execution, &status, &request); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.QueryRowContext(ctx, `SELECT count(*) FROM mp_hls_artifacts`).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if count != 1 || generation != 2 || revision != 4 || exited != 0 || cleaned != 0 || execution != "new" || status != "running" || request != "source-request" || artifacts != 0 {
		t.Fatal(count, generation, revision, exited, cleaned, execution, status, request, artifacts)
	}
}

// Completion is not actual exit. The confirmed-exit write must retire writes
// atomically to satisfy F, even when no cancellation preceded normal completion.
func TestMPHLSDomainNativeCompletedWindowExitRetiresWrites(t *testing.T) {
	f := mpHLSCanonicalSQL(t)
	ctx := t.Context()
	digest := strings.Repeat("a", 64)
	exit := strings.Repeat("b", 64)
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowClaim), "mp.hls.window_insert", "media", int64(20), int64(19), int64(100), int64(4), "completed-execution", "source-request", int64(21), digest); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE playback_hls_windows SET status='complete',highest_produced=21 WHERE session_id='media' AND start_index=20`)
	var retired, exited int64
	if err := f.conn.QueryRowContext(ctx, `SELECT mp_writes_retired,mp_exit_confirmed FROM playback_hls_windows WHERE session_id='media' AND start_index=20`).Scan(&retired, &exited); err != nil || retired != 0 || exited != 0 {
		t.Fatal(retired, exited, err)
	}
	// Synthetic accepted exact-execution exit proof, not status-derived exit.
	if err := mpPrepOne(ctx, f.turn(MPHLSWindowExit), "mp.hls.window_exit", exit, int64(0), int64(1), "media", int64(20), int64(1), "completed-execution", int64(1), int64(0), exit, "source-request", int64(4)); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.QueryRowContext(ctx, `SELECT mp_writes_retired,mp_exit_confirmed FROM playback_hls_windows WHERE session_id='media' AND start_index=20`).Scan(&retired, &exited); err != nil || retired != 1 || exited != 1 {
		t.Fatal(retired, exited, err)
	}
}
