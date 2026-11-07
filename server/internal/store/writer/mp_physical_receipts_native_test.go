package writer

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Runner-only synthetic storage evidence. Payload bytes stand in for W output;
// this is no codec/schema/authority/ACK/COMMIT or current-disclosure acceptance.
func TestMPSessionDomainNativeWorkerOriginalPayload(t *testing.T) {
	for _, physical := range []bool{false, true} {
		t.Run(map[bool]string{false: "session", true: "physical"}[physical], func(t *testing.T) {
			f := mpHLSCanonicalSQL(t)
			table, receipts, kind := "playback_v1_sessions", "mp_session_command_receipts", MPSessionReconcile
			defs := mpSessionWorkerPayloadDefinitions()
			if physical {
				table, receipts, kind = "playback_sessions", "mp_physical_command_receipts", MPHLSReconcile
				defs = mpPhysicalWorkerPayloadDefinitions()
				// Existing fixture has a canonical playback_sessions FK parent. Add only
				// explicit worker projection columns rather than invent a second MP root.
				table = "playback_sessions"
				for _, column := range []string{"mp_command_incarnation BLOB", "mp_command_last_sequence INTEGER DEFAULT 0", "mp_command_acknowledged_sequence INTEGER DEFAULT 0", "mp_owner_revision INTEGER DEFAULT 1", "mp_receipt_rows INTEGER DEFAULT 0", "mp_receipt_variable_bytes INTEGER DEFAULT 0", "mp_receipt_reclaim_due_ms INTEGER", "mp_retirement_state TEXT DEFAULT 'live'"} {
					f.exec("ALTER TABLE playback_sessions ADD COLUMN " + column)
				}
			} else {
				f.exec(`CREATE TABLE playback_v1_sessions(id TEXT PRIMARY KEY,mp_command_incarnation BLOB,mp_command_last_sequence INTEGER DEFAULT 0,mp_command_acknowledged_sequence INTEGER DEFAULT 0,mp_owner_revision INTEGER DEFAULT 1,mp_receipt_rows INTEGER DEFAULT 0,mp_receipt_variable_bytes INTEGER DEFAULT 0,mp_receipt_reclaim_due_ms INTEGER,mp_retirement_state TEXT DEFAULT 'live')`)
			}
			f.exec("CREATE UNIQUE INDEX test_worker_owner ON " + table + "(id,mp_command_incarnation)")
			f.exec("CREATE TABLE " + receipts + `(owner_id TEXT,command_incarnation BLOB,command_sequence INTEGER,operation_id BLOB,command_kind INTEGER,request_digest BLOB,result_version INTEGER,result_count INTEGER,result_digest BLOB,result_payload BLOB CHECK(length(result_payload) BETWEEN 1 AND 65536),applied_ms INTEGER,variable_bytes INTEGER GENERATED ALWAYS AS(length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(operation_id)+length(request_digest)+length(result_digest)+length(result_payload)) VIRTUAL,PRIMARY KEY(owner_id,command_incarnation,command_sequence),UNIQUE(owner_id,command_incarnation,operation_id),FOREIGN KEY(owner_id,command_incarnation) REFERENCES ` + table + `(id,mp_command_incarnation) ON DELETE NO ACTION ON UPDATE NO ACTION) WITHOUT ROWID`)
			incarnation := [16]byte{1}
			owner := "original-owner"
			f.exec("INSERT INTO "+table+"(id,mp_command_incarnation) VALUES(?,?)", owner, incarnation[:])
			registry, err := stmttest.New(t, defs)
			if err != nil {
				t.Fatal(err)
			}
			f.registry = registry
			f.steps = nil
			for _, d := range defs {
				f.steps = append(f.steps, statementStep{id: d.ID, work: Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Nanosecond}})
			}
			// Keep the catalog's statement kinds explicit in this runner fixture.
			for _, d := range defs {
				if d.Kind != stmt.Read && d.Kind != stmt.Write {
					t.Fatal(d.ID)
				}
			}
			h := mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: incarnation, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}, kind: kind, appliedMS: 100, resultDigest: ResultDigest{4}}
			v := mpWMediaPayload{header: h, version: 1, count: 1, payload: []byte("immutable original W bytes " + strings.Repeat("x", 32))}
			o := mpMediaWorkerBefore{incarnation: incarnation, revision: 1}
			a := mpMediaWorkerAdmission{maxRows: 1, maxBytes: 10000, reclaimDueMS: 200}
			var n int64
			if physical {
				n, err = storeMPPhysicalWorkerPayload(t.Context(), f.turn(kind), owner, o, a, v)
			} else {
				n, err = storeMPSessionWorkerPayload(t.Context(), f.turn(kind), owner, o, a, v)
			}
			if err != nil {
				t.Fatal(err)
			}
			var last, ack, rows, debt, due int64
			if err = f.conn.QueryRowContext(t.Context(), "SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_due_ms FROM "+table+" WHERE id=?", owner).Scan(&last, &ack, &rows, &debt, &due); err != nil {
				t.Fatal(err)
			}
			if last != 1 || ack != 0 || rows != 1 || debt != n || due != 200 {
				t.Fatal(last, ack, rows, debt, n, due)
			}
			// Mutable root drift does not rewrite the saved result. No current-row replay.
			f.exec("UPDATE "+table+" SET mp_owner_revision=9,mp_retirement_state='draining' WHERE id=?", owner)
			var actual int64
			if physical {
				actual, err = mpPhysicalWorkerPayloadBytes(t.Context(), f.turn(kind), owner, h)
			} else {
				actual, err = mpSessionWorkerPayloadBytes(t.Context(), f.turn(kind), owner, h)
			}
			if err != nil || actual != n {
				t.Fatal(actual, n, err)
			}
			var got mpWMediaPayload
			if physical {
				got, err = loadMPPhysicalWorkerPayload(t.Context(), f.turn(kind), owner, h, n)
			} else {
				got, err = loadMPSessionWorkerPayload(t.Context(), f.turn(kind), owner, h, n)
			}
			if err != nil || got.header != h || got.count != v.count || !bytes.Equal(got.payload, v.payload) {
				t.Fatal(got, err)
			}
			if physical {
				got, err = loadMPPhysicalWorkerPayload(t.Context(), f.turn(kind), owner, h, n-1)
			} else {
				got, err = loadMPSessionWorkerPayload(t.Context(), f.turn(kind), owner, h, n-1)
			}
			if err == nil || len(got.payload) != 0 {
				t.Fatal("copied without actual byte admission", got, err)
			}
		})
	}
}
