package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Runner target: native canonical SQL, not a fabricated accepting MP command.
// No borrowed registered purpose, fake authority, altered registry, new writer,
// draft DDL, provider, large fixture or Applied/COMMIT claim is used here.
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
		if !strings.Contains(d.SQL, "queue_v1_stop_obligations") {
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
