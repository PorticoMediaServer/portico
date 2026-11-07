package writer

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Canonical controls use native SQL, not a fabricated accepting MP command.
// No borrowed purpose, fake authority, registry change, new writer or provider
// is used. The opt-in F fixtures below apply only the exact parent packet to a
// disposable clone; none of these controls claims an Applied command result.
func TestMPNativeCanonicalSQLPrepare(t *testing.T) {
	w, _, _ := testExecutor(t)
	defs := append(mpQueueDefinitions(), mpReadingDefinitions()...)
	defs = append(defs, mpQueueSaveDefinitions()...)
	for _, d := range mpNextDefinitions() {
		switch string(d.ID) {
		case "mp.next.item_kind", "mp.next.item", "mp.next.session", "mp.next.outstanding", "mp.next.media", "mp.next.cancel":
			defs = append(defs, d)
		}
	}
	for _, d := range mpQueueCleanupDefinitions() {
		if !strings.Contains(d.SQL, "mp_queue_stop_obligations") && !strings.Contains(d.SQL, "mp_retired_generation") {
			defs = append(defs, d)
		}
	}
	for _, d := range defs {
		t.Run(string(d.ID), func(t *testing.T) {
			args := make([]any, len(d.Params))
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, args...)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMPNativeQueueImmutableStageCASAndCleanup(t *testing.T) {
	w, _, _ := testExecutor(t)
	defs := map[string]stmt.Definition{}
	for _, d := range mpQueueDefinitions() {
		defs[string(d.ID)] = d
	}
	exec := func(id string, want int64, args ...any) {
		t.Helper()
		result, err := w.db.ExecContext(t.Context(), defs[id].SQL, args...)
		if err != nil {
			t.Fatal(id, err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != want {
			t.Fatalf("%s rows %d, want %d: %v", id, n, want, err)
		}
	}
	exec("mp.queue.begin", 1, "queue", "~device", "account", "profile", int64(1), int64(1))
	exec("mp.queue.snapshot_begin", 1, "snap", "queue", "items", "", "test", int64(2), "source-revision")
	b, err := mpEncodeChunk([]string{"item", "item"})
	if err != nil {
		t.Fatal(err)
	}
	exec("mp.queue.chunk_stage", 1, "snap", int64(0), b)
	if _, err := w.db.ExecContext(t.Context(), defs["mp.queue.chunk_stage"].SQL, "snap", int64(0), b); err == nil {
		t.Fatal("immutable chunk overwritten")
	}
	exec("mp.queue.stage_cursor", 0, int64(2), int64(b[0]), "snap", "queue", int64(1), int64(2))
	exec("mp.queue.snapshot_seal", 0, "snap", "queue")
	exec("mp.queue.stage_cursor", 1, int64(2), int64(b[0]), "snap", "queue", int64(0), int64(2))
	exec("mp.queue.snapshot_seal", 1, "snap", "queue")
	exec("mp.queue.stage_cursor", 0, int64(2), int64(b[0]), "snap", "queue", int64(0), int64(2))
	exec("mp.queue.segment_insert", 1, "segment", "queue", "h", "snap", int64(0), int64(2), int64(0))
	exec("mp.queue.removal_insert", 1, "snap", int64(1))
	exec("mp.queue.cleanup_snapshot", 0, "snap", "queue", "snap", "snap", "snap")
	exec("mp.queue.segment_delete", 0, "segment", "queue", "snap", int64(0), int64(3), int64(0))
	exec("mp.queue.segment_delete", 1, "segment", "queue", "snap", int64(0), int64(2), int64(0))
	exec("mp.queue.cleanup_snapshot", 0, "snap", "queue", "snap", "snap", "snap")
	exec("mp.queue.cleanup_chunk", 1, "snap", int64(0))
	exec("mp.queue.cleanup_removal", 1, "snap", int64(1))
	exec("mp.queue.cleanup_snapshot", 1, "snap", "queue", "snap", "snap", "snap")
	var count int64
	if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM queues_v1 WHERE id='queue'").Scan(&count); err != nil || count != 1 {
		t.Fatal("cleanup erased retained queue root", count, err)
	}
}
func TestMPNativeQueueOwnerRevisionCustodyCAS(t *testing.T) {
	w, _, _ := testExecutor(t)
	defs := map[string]stmt.Definition{}
	for _, d := range mpQueueDefinitions() {
		defs[string(d.ID)] = d
	}
	if _, err := w.db.ExecContext(t.Context(), defs["mp.queue.begin"].SQL, "queue", "device", "account", "profile", int64(1), int64(1)); err != nil {
		t.Fatal(err)
	}
	args := []any{"device", int64(2), int64(0), int64(0), "", "", int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(2), "queue", "device", "account", "profile", int64(1), int64(0)}
	for _, position := range []int{14, 15, 16, 17, 18} {
		bad := append([]any(nil), args...)
		if _, ok := bad[position].(string); ok {
			bad[position] = "other"
		} else {
			bad[position] = int64(55)
		}
		r, err := w.db.ExecContext(t.Context(), defs["mp.queue.publish"].SQL, bad...)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := r.RowsAffected()
		if n != 0 {
			t.Fatal("stale/foreign CAS changed queue", position)
		}
	}
	r, err := w.db.ExecContext(t.Context(), defs["mp.queue.publish"].SQL, args...)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		t.Fatal("fresh CAS failed")
	}
	// A retry cannot reapply current effects; the parent must serve the saved
	// original typed result, independently of this now-stale source CAS.
	r, err = w.db.ExecContext(t.Context(), defs["mp.queue.publish"].SQL, args...)
	if err != nil {
		t.Fatal(err)
	}
	n, _ = r.RowsAffected()
	if n != 0 {
		t.Fatal("fresh path reapplied")
	}
	var foreignKeys int64
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("native FK guard absent", err)
	}
}

// Runner-only opt-in while F is a draft; after canonical adoption the tables
// already exist and no packet input is needed. This fixture applies parent F
// only to the disposable clone. It creates no accepting MP dispatcher/purpose.
func mpNativeFExecutor(t *testing.T) *executor {
	t.Helper()
	w, _, _ := testExecutor(t)
	var exists int64
	if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='mp_queue_command_receipts'").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists == 0 {
		packet := os.Getenv("PORTICO_MP_F_PACKET")
		if packet == "" {
			t.Skip("F not embedded: supply exact parent packet via PORTICO_MP_F_PACKET on the runner")
		}
		body, err := os.ReadFile(packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(body)); err != nil {
			t.Fatal("apply F to disposable native fixture", err)
		}
	}
	return w
}
func TestMPNativeFReceiptSQLAndScalarRoundTrip(t *testing.T) {
	w := mpNativeFExecutor(t)
	defs := append(mpQueueResultDefinitions(), mpNextResultDefinitions()...)
	defs = append(defs, mpQueueCleanupDefinitions()...)
	defs = append(defs, mpNextDefinitions()...)
	byID := map[string]stmt.Definition{}
	for _, d := range defs {
		byID[string(d.ID)] = d
		args := make([]any, len(d.Params))
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, args...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		for rows.Next() {
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	q := mpTestQueueSavedReceipt()
	q.digest, _ = mpQueueSavedCommitment(q)
	n := mpTestNextSavedReceipt()
	n.digest, _ = mpNextSavedCommitment(n)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms,mp_command_incarnation) VALUES('queue','device','device','local','account','profile',4,1,19,?)`, q.identity.Incarnation[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO playback_v1_prepared(token,queue_id,device_id,create_key,digest,session_id,session_generation,session_state,queue_revision,next_position,entry_id,item_id,media_session_id,request,presentation,expires_ms,state,created_ms,mp_command_incarnation) VALUES('next','queue','device','native-key','native-digest','logical-next',1,'playing',5,1,'snap-1',1,'media-next','','',1000,'committed',19,?)`, n.identity.Incarnation[:]); err != nil {
		t.Fatal(err)
	}
	check := func(insert, read string, args []any, owner string, identity CommandIdentity) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), byID[insert].SQL, args...); err != nil {
			t.Fatal(insert, err)
		}
		expected := mpReceiptScalarFields(t, byID[insert].SQL, args)
		rows, err := w.db.QueryContext(t.Context(), byID[read].SQL, owner, identity.Incarnation[:], identity.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("missing original scalar result")
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range dest {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, col := range columns {
			if !reflect.DeepEqual(values[i], expected[col]) {
				t.Fatalf("%s field %s: got %T/%v want %T/%v", read, col, values[i], values[i], expected[col], expected[col])
			}
		}
		if rows.Next() {
			t.Fatal("duplicate scalar receipt")
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	check("mp.queue.result_insert", "mp.queue.result_read", mpQueueScalarResultArgs(q), q.queue, q.identity)
	check("mp.next.result_insert", "mp.next.result_read", mpNextScalarResultArgs(n), n.token, n.identity)
}

// Prepared runner fixture: ACKed immutable result children can be reclaimed
// while mutable exact-stop obligations still retain their queue/next/physical
// owners. This is native SQL/FK evidence, never accepting command execution.
func TestMPNativeFStopDebtSurvivesReceiptReclaim(t *testing.T) {
	w := mpNativeFExecutor(t)
	defs := append(mpQueueResultDefinitions(), mpNextResultDefinitions()...)
	defs = append(defs, mpQueueCleanupDefinitions()...)
	defs = append(defs, mpNextDefinitions()...)
	byID := map[string]stmt.Definition{}
	for _, d := range defs {
		byID[string(d.ID)] = d
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(query, err)
		}
	}
	q := mpTestQueueSavedReceipt()
	q.digest, _ = mpQueueSavedCommitment(q)
	n := mpTestNextSavedReceipt()
	n.digest, _ = mpNextSavedCommitment(n)
	exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms,mp_command_incarnation,mp_command_last_sequence,mp_command_acknowledged_sequence) VALUES('queue','device','device','local','account','profile',4,1,19,?,1,1)`, q.identity.Incarnation[:])
	exec(`INSERT INTO playback_v1_prepared(token,queue_id,device_id,create_key,digest,session_id,session_generation,session_state,queue_revision,next_position,entry_id,item_id,media_session_id,request,presentation,expires_ms,state,created_ms,mp_command_incarnation,mp_command_last_sequence,mp_command_acknowledged_sequence) VALUES('next','queue','device','native-key','native-digest','logical-next',1,'playing',5,1,'snap-1',1,'media-next','','',1000,'committed',19,?,1,1)`, n.identity.Incarnation[:])
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'library','/fixture')`)
	exec(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1,?,1,1)`, make([]byte, 16))
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(1,'asset','/fixture/item',1,1,'mp4','','',0,0,0,1)`)
	for i, stop := range []mpReceiptRetireMedia{q.retireMedia[0], n.retireMedia[0]} {
		exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_retired_generation,mp_retire_request_id) VALUES(?,'unused','account','profile',1,'asset',?,'stopped',?,'unused','2099-01-01T00:00:00Z',?,0,?,?)`, stop.media, stop.generation, stop.media, []string{"source-request-one", "source-request-two"}[i], stop.generation, stop.requestID)
	}
	exec(byID["mp.queue.result_insert"].SQL, mpQueueScalarResultArgs(q)...)
	for i, s := range q.segments {
		exec(byID["mp.queue.result_segment_insert"].SQL, q.queue, q.identity.Incarnation[:], q.identity.Sequence, int64(i), s.id, s.snapshot, s.kind, s.label, s.sourceRevision, s.count, s.state)
	}
	for i, s := range q.retireMedia {
		exec(byID["mp.queue.result_retire_insert"].SQL, q.queue, q.identity.Incarnation[:], q.identity.Sequence, int64(i), s.media, s.generation, s.requestID, mpBool(s.pending))
	}
	exec(byID["mp.next.result_insert"].SQL, mpNextScalarResultArgs(n)...)
	for i, s := range n.retireMedia {
		exec(byID["mp.next.result_retire_insert"].SQL, n.token, n.identity.Incarnation[:], n.identity.Sequence, int64(i), s.media, s.generation, s.requestID, mpBool(s.pending))
	}
	a, b := q.retireMedia[0], n.retireMedia[0]
	exec(byID["mp.queue.stop"].SQL, q.queue, a.generation, "retired", int64(20), "logical", int64(1), a.media, a.generation)
	exec(byID["mp.next.stop"].SQL, n.token, b.generation, "canceled", int64(20), b.media, b.generation)
	for _, table := range []string{"mp_queue_receipt_segments", "mp_queue_receipt_retire_media", "mp_queue_command_receipts", "mp_next_receipt_retire_media", "mp_next_command_receipts"} {
		exec("DELETE FROM " + table)
	} // closed fixture table list
	var count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM mp_queue_stop_obligations)+(SELECT count(*) FROM mp_next_stop_obligations)`).Scan(&count); err != nil || count != 2 {
		t.Fatal("ACK/reclaim erased physical debt", count, err)
	}
	for _, stop := range []mpReceiptRetireMedia{a, b} {
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM playback_sessions WHERE id=?`, stop.media); err == nil {
			t.Fatal("physical owner escaped outstanding stop FK")
		}
	}
	supervisor := [16]byte{9}
	exec(`UPDATE playback_sessions SET mp_exited_generation=generation,mp_exited_ms=50,mp_exit_request_id=mp_retire_request_id,mp_exit_supervisor_incarnation=?`, supervisor[:])
	r, err := w.db.ExecContext(t.Context(), byID["mp.next.exit"].SQL, int64(50), n.token, b.media, b.generation, "changed request", int64(50), supervisor[:], int64(50))
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := r.RowsAffected()
	if changed != 0 {
		t.Fatal("wrong request released physical debt")
	}
	exec(byID["mp.next.exit"].SQL, int64(50), n.token, b.media, b.generation, b.requestID, int64(50), supervisor[:], int64(50))
	exec(byID["mp.queue.stop_exit"].SQL, int64(50), q.queue, a.media, a.generation, a.requestID, int64(50), supervisor[:], int64(50))
	for _, scope := range []struct{ table, key, owner, request string }{{"mp_queue_stop_obligations", "queue_id", q.queue, a.requestID}, {"mp_next_stop_obligations", "next_id", n.token, b.requestID}} {
		var request string
		var exited int64
		if err := w.db.QueryRowContext(t.Context(), "SELECT request_id,exited_ms FROM "+scope.table+" WHERE "+scope.key+"=?", scope.owner).Scan(&request, &exited); err != nil || request != scope.request || exited != 50 {
			t.Fatal("lost original request/exit", request, exited, err)
		}
	}
}
