package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

func mpSessionStopFixture(t *testing.T) *executor {
	t.Helper()
	w, _ := uoFlagsFixture(t)
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(900,'retire-asset','retire.mkv',100,1,'matroska','h264','aac',1920,1080,1,7)`)
	wc008NativeExec(t, w, `INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id) VALUES('retire-media','hash','account','profile',2111,'retire-asset',2,'playing','grant','opaque','2099-01-01T00:00:00Z','source-request',1,'execution')`)
	return w
}

// Seed exit fields only for the native guard's negative/graph controls. This is
// not an accepting exit recipe; MP15 separately proves the real supervisor seam.
func mpSessionSeedExited(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE playback_sessions SET state='stopped',mp_grant_retired=1,mp_retired_generation=generation,mp_retire_request_id='stop',mp_exited_generation=generation,mp_exit_request_id='stop',mp_exit_supervisor_incarnation=?,mp_exited_ms=101,mp_exit_digest=?,mp_cleanup_complete=1`, make([]byte, 16), strings.Repeat("a", 64))
}
func TestMPSessionChildlessExitAndCustodyGuard(t *testing.T) {
	for _, cell := range []string{"live", "exited", "cleanup", "grant", "receipt-debt", "read-pin", "hls", "queue-stop", "replacement"} {
		t.Run(cell, func(t *testing.T) {
			w := mpSessionStopFixture(t)
			if cell != "live" {
				mpSessionSeedExited(t, w)
			}
			switch cell {
			case "cleanup":
				wc008NativeExec(t, w, `UPDATE playback_sessions SET mp_cleanup_complete=0`)
			case "grant":
				wc008NativeExec(t, w, `UPDATE playback_sessions SET mp_grant_retired=0`)
			case "receipt-debt":
				wc008NativeExec(t, w, `UPDATE playback_sessions SET mp_receipt_rows=1,mp_receipt_variable_bytes=1`)
			case "read-pin":
				wc008NativeExec(t, w, `INSERT INTO mp_media_read_pins(pin_id,media_session_id,execution_id,occurrence_generation,source_version,from_byte,size) VALUES('pin','retire-media','execution',2,'source',0,1)`)
			case "hls":
				wc008NativeExec(t, w, `INSERT INTO playback_hls_windows(session_id,start_index,status,started_at_ms) VALUES('retire-media',0,'running',1)`)
			case "queue-stop":
				wc008NativeExec(t, w, `INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,custody_generation,created_ms,updated_ms) VALUES('queue','device','dev','local','account','profile',1,0,1,1)`)
				wc008NativeExec(t, w, `INSERT INTO mp_queue_stop_obligations(queue_id,media_session_id,media_generation,request_id,reason,requested_ms,logical_session_id,logical_generation) VALUES('queue','retire-media',2,'stop','stop',1,'logical',1)`)
			case "replacement":
				wc008NativeExec(t, w, `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES('logical','dev','account','profile','local','key','digest','vod','player','stopped','',1,1,100,1,1)`)
				wc008NativeExec(t, w, `INSERT INTO mp_session_replacements(session_id,old_media_id,old_media_generation,old_logical_generation,old_execution_id,new_media_id,new_execution_id,new_media_incarnation,input_digest,phase) VALUES('logical','retire-media',2,1,'execution','new','newexec',?,?, 'requested')`, make([]byte, 16), make([]byte, 32))
			}
			turn := wc008NativeTurn(t, w, IARetireStep, mpSessionParentDrainDefinitions())
			err := requireMPSessionParentDrained(t.Context(), turn, "retire-media")
			if cell == "exited" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal("held parent accepted", cell, err)
			}
		})
	}
}
func TestMPSessionGuardDefinitionsCoverActualFKGraph(t *testing.T) {
	w := mpSessionStopFixture(t)
	rows, err := w.db.Query(`SELECT m.name,f."from" FROM sqlite_schema m JOIN pragma_foreign_key_list(m.name) f WHERE m.type='table' AND f."table"='playback_sessions'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, key string
		if err = rows.Scan(&table, &key); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, p := range mpSessionCustodyPoints {
			found = found || p.table == table && p.key == key
		}
		if !found && !(table == "mp_external_guide_pins" && (key == "current_occurrence_generation" || key == "current_execution_id")) && !(table == "mp_physical_command_receipts" && key == "command_incarnation") {
			t.Fatal("uncovered FK", table, key)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	wc008NativePrepare(t, w, mpSessionParentDrainDefinitions())
}
func mpSessionGuardTestDefinitions() []stmt.Definition { return mpSessionParentDrainDefinitions() }
