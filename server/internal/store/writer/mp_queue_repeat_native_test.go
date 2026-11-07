package writer

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Runner-only effect evidence against a synthetic root projection and exact F
// stop table. This does not qualify authority, codec, commit or actual exit.
func TestMPQueueRepeatNativeRetainsExactStop(t *testing.T) {
	path := os.Getenv("PORTICO_MP_F_PACKET")
	if path == "" {
		t.Skip("F absent: supply pinned F on standard runner")
	}
	packet, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(packet), "CREATE TABLE mp_next_stop_obligations(")
	if start < 0 {
		t.Fatal("F missing independent stop owner")
	}
	ddl := string(packet)[start:]
	end := strings.Index(ddl, ";")
	if end < 0 {
		t.Fatal("F stop DDL incomplete")
	}
	ddl = ddl[:end+1]
	for _, wrongGeneration := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "wrong-generation"}[wrongGeneration], func(t *testing.T) {
			f := mpHLSCanonicalSQL(t)
			for _, col := range []string{"mp_retired_generation INTEGER", "mp_exited_generation INTEGER", "mp_retire_request_id TEXT", "mp_exit_request_id TEXT", "mp_exit_supervisor_incarnation BLOB", "mp_exited_ms INTEGER"} {
				f.exec("ALTER TABLE playback_sessions ADD COLUMN " + col)
			}
			f.exec(`UPDATE playback_sessions SET mp_retired_generation=2,mp_exited_generation=0,mp_retire_request_id='physical-stop',mp_exit_request_id='',mp_exit_supervisor_incarnation=NULL,mp_exited_ms=0`)
			f.exec(`CREATE TABLE queues_v1(id TEXT PRIMARY KEY,authority TEXT,account_id TEXT,profile_id TEXT,owner_kind TEXT,owner_id TEXT,revision INTEGER,custody_generation INTEGER,repeat TEXT,updated_ms INTEGER);
            INSERT INTO queues_v1 VALUES('queue','local','account','profile','device','device',3,7,'off',10);
            CREATE TABLE playback_v1_prepared(token TEXT PRIMARY KEY,queue_id TEXT,media_session_id TEXT,mp_media_generation INTEGER,state TEXT,cancel_reason TEXT);
            CREATE INDEX playback_v1_prepared_queue ON playback_v1_prepared(queue_id);
            INSERT INTO playback_v1_prepared VALUES('next','queue','media',2,'prepared','')`)
			f.exec(ddl)
			if wrongGeneration {
				f.exec(`UPDATE playback_v1_prepared SET mp_media_generation=3`)
			}
			defs := mpQueueRepeatDefinitions()
			f.registry, err = stmttest.New(t, defs)
			if err != nil {
				t.Fatal(err)
			}
			f.steps = nil
			for _, d := range defs {
				f.steps = append(f.steps, statementStep{id: d.ID, work: Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Nanosecond}})
			}
			got, e := applyMPQueueRepeat(t.Context(), f.turn(MPQueueRepeat), mpQueueRepeatInvocation{request: mpQueueRepeatRequest{queue: "queue", account: "account", profile: "profile", device: "device", expectedRevision: 3, expectedCustody: 7, repeat: "all"}, appliedMS: 100})
			if wrongGeneration {
				if !errors.Is(e, ErrConflict) || got != (mpQueueRepeatEffect{}) {
					t.Fatal(got, e)
				}
				var state string
				if e = f.conn.QueryRowContext(t.Context(), `SELECT state FROM playback_v1_prepared WHERE token='next'`).Scan(&state); e != nil || state != "prepared" {
					t.Fatal(state, e)
				}
				return
			}
			if e != nil || got.canceledRequest != "physical-stop" || got.canceledGeneration != 2 || !got.physicalPending || !got.changed {
				t.Fatal(got, e)
			}
			var request string
			var generation int64
			if e = f.conn.QueryRowContext(t.Context(), `SELECT request_id,media_generation FROM mp_next_stop_obligations WHERE next_id='next' AND media_session_id='media'`).Scan(&request, &generation); e != nil || request != got.canceledRequest || generation != 2 {
				t.Fatal(request, generation, e)
			}
		})
	}
}
