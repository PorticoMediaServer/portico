package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	mgCaptureRead           stmt.ID = "mg.capture.recording"
	mgCaptureAllocationRead stmt.ID = "mg.capture.allocation"
	mgCaptureSourceRead     stmt.ID = "mg.capture.source"
	mgCaptureFollowersRead  stmt.ID = "mg.capture.followers"
	mgCaptureClaim          stmt.ID = "mg.capture.claim"
	mgCaptureRenew          stmt.ID = "mg.capture.renew"
	mgCaptureCancel         stmt.ID = "mg.capture.cancel"
	mgCaptureAttach         stmt.ID = "mg.capture.attach"
)

func mgCaptureDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string, nullable bool) stmt.Param {
		return stmt.Param{Name: n, Type: stmt.Blob, Nullable: nullable}
	}
	// Namespace and every physical scope component join the original worker CAS.
	fence := `id=? AND owner_key=? AND revision=? AND state=? AND claim_token=? AND claim_generation=? AND cancel_generation=? AND owner_instance=? AND lease_until_ms=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND coalesce(capture_launch_id,'')=coalesce(?,'') AND coalesce(length(capture_launch_id),-1)=? AND allocation_id=? AND capture_allocation_generation=? AND capture_allocation_token=? AND capture_source_revision=? AND capture_source_fence=? AND capture_writer_retired_sequence=? AND capture_physical_exit_sequence=? AND capture_shared_ready_sequence=? AND share_capture_claim_generation=? AND coalesce(share_capture_launch_id,'')=coalesce(?,'') AND coalesce(length(share_capture_launch_id),-1)=? AND coalesce(guide_pin_generation,'')=coalesce(?,'') AND coalesce(length(guide_pin_generation),-1)=?`
	fp := []stmt.Param{text("id"), text("owner"), integer("revision"), text("state"), text("token"), integer("claim"), integer("cancel"), text("instance"), integer("lease"), blob("incarnation", false), integer("last"), integer("ack"), blob("launch", true), integer("launch_length"), text("old_allocation"), integer("allocation_generation"), text("allocation_token"), integer("source_revision"), text("source_fence"), integer("writer_retired"), integer("physical_exit"), integer("shared_ready"), integer("share_claim"), blob("share_launch", true), integer("share_launch_length"), {Name: "guide_pin", Type: stmt.Text, Nullable: true}, integer("guide_pin_length")}
	defs := []stmt.Definition{
		{ID: mgCaptureRead, Kind: stmt.Read, SQL: `SELECT ` + mgCaptureColumns + ` FROM dvr_recordings WHERE id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgCaptureAllocationRead, Kind: stmt.Read, SQL: `SELECT source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,preempt_at_ms FROM live_allocations WHERE id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgCaptureSourceRead, Kind: stmt.Read, SQL: `SELECT revision,state,active_generation FROM live_sources WHERE id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgCaptureFollowersRead, Kind: stmt.Read, SQL: `SELECT id FROM dvr_recordings INDEXED BY dvr_recordings_capture WHERE share_capture_recording_id=? ORDER BY id LIMIT 1`, Params: []stmt.Param{text("leader")}},
		{ID: mgCaptureClaim, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET state='preparing',reason='',claim_token=?,claim_generation=claim_generation+1,owner_instance=?,lease_until_ms=?,allocation_id=?,artifact_id=?,next_attempt_ms=0,revision=revision+1,updated_ms=?,capture_launch_id=?,capture_allocation_generation=?,capture_allocation_token=?,capture_source_revision=?,capture_source_fence=?,last_command_sequence=last_command_sequence+1 WHERE ` + fence + ` AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL`, Params: append([]stmt.Param{text("new_token"), text("new_instance"), integer("new_lease"), text("allocation"), text("artifact"), integer("now"), blob("new_launch", false), integer("new_allocation_generation"), text("new_allocation_token"), integer("new_source_revision"), text("new_source_fence")}, fp...)},
		{ID: mgCaptureRenew, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET lease_until_ms=?,revision=revision+1,updated_ms=?,last_command_sequence=last_command_sequence+1 WHERE ` + fence, Params: append([]stmt.Param{integer("new_lease"), integer("now")}, fp...)},
		{ID: mgCaptureCancel, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET state='cancelled',reason=?,cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=?,finished_ms=?,last_command_sequence=last_command_sequence+1 WHERE ` + fence, Params: append([]stmt.Param{text("reason"), integer("now"), integer("finished")}, fp...)},
		{ID: mgCaptureAttach, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET has_dvr_recording_shares=1,share_capture_recording_id=?,share_capture_claim_generation=?,share_capture_launch_id=?,revision=revision+1,updated_ms=?,last_command_sequence=last_command_sequence+1 WHERE ` + fence + ` AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL`, Params: append([]stmt.Param{text("leader"), integer("leader_claim"), blob("leader_launch", false), integer("now")}, fp...)},
	}
	defs = append(defs, mgCaptureReceiptDefinitions()...)
	for i := range defs {
		d := &defs[i]
		d.Owner = "writer.mg.capture"
		d.CallSites = []string{"writer/mg_capture_effects.go", "writer/mg_capture_receipts.go"}
		switch d.ID {
		case mgCaptureFollowersRead:
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"dvr_recordings_capture"}}
		case mgCaptureReceiptOperationRead:
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dvr_capture_command_receipts_2"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "dvr_capture_command_receipts", Name: "sqlite_autoindex_dvr_capture_command_receipts_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "recording_id", Collation: "BINARY"}, {Name: "command_incarnation", Collation: "BINARY"}, {Name: "operation_id", Collation: "BINARY"}}}}}
		case mgCaptureReceiptRead:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_capture_command_receipts"}}
		case mgCaptureReceiptInsert:
			d.Plan = stmt.Plan{}
		default:
			table := "dvr_recordings"
			if d.ID == mgCaptureAllocationRead {
				table = "live_allocations"
			}
			if d.ID == mgCaptureSourceRead {
				table = "live_sources"
			}
			index := "sqlite_autoindex_" + table + "_1"
			d.SQL = strings.Replace(d.SQL, table+" WHERE", table+" INDEXED BY "+index+" WHERE", 1)
			d.SQL = strings.Replace(d.SQL, "UPDATE "+table+" SET", "UPDATE "+table+" INDEXED BY "+index+" SET", 1)
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
		}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			switch p.Type {
			case stmt.Text:
				args[j] = "missing"
			case stmt.Blob:
				size := 16
				if strings.Contains(p.Name, "digest") {
					size = 32
				}
				args[j] = make([]byte, size)
			default:
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing_native_key", Bindings: args}}
	}
	return defs
}
