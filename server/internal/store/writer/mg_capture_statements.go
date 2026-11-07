package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgCaptureRead           stmt.ID = "mg.capture.recording"
	mgCaptureAllocationRead stmt.ID = "mg.capture.allocation"
	mgCaptureFollowersRead  stmt.ID = "mg.capture.followers"
	mgCaptureClaim          stmt.ID = "mg.capture.claim"
	mgCaptureRenew          stmt.ID = "mg.capture.renew"
	mgCaptureCancel         stmt.ID = "mg.capture.cancel"
	mgCaptureAttach         stmt.ID = "mg.capture.attach"
)

const mgCaptureColumns = `id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,rule_id,rule_revision,start_ms,end_ms,revision,state,reason,claim_generation,cancel_generation,claim_token,owner_instance,lease_until_ms,allocation_id,artifact_id,item_id,next_attempt_ms,recovery_attempt,has_dvr_recording_shares,share_capture_recording_id,updated_ms,finished_ms`

func mgCaptureDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	// All six worker fences plus revision/state/owner are carried even for cancel.
	fence := `id=? AND owner_key=? AND revision=? AND state=? AND claim_token=? AND claim_generation=? AND cancel_generation=? AND owner_instance=? AND lease_until_ms=?`
	fp := []stmt.Param{text("id"), text("owner"), integer("revision"), text("state"), text("token"), integer("claim"), integer("cancel"), text("instance"), integer("lease")}
	defs := []stmt.Definition{
		{ID: mgCaptureRead, Kind: stmt.Read, SQL: `SELECT ` + mgCaptureColumns + ` FROM dvr_recordings WHERE id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgCaptureAllocationRead, Kind: stmt.Read, SQL: `SELECT source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,preempt_at_ms FROM live_allocations WHERE id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgCaptureFollowersRead, Kind: stmt.Read, SQL: `SELECT id FROM dvr_recordings INDEXED BY dvr_recordings_capture WHERE share_capture_recording_id=? ORDER BY id LIMIT 1`, Params: []stmt.Param{text("leader")}},
		{ID: mgCaptureClaim, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET state='preparing',reason='',claim_token=?,claim_generation=claim_generation+1,owner_instance=?,lease_until_ms=?,allocation_id=?,artifact_id=?,next_attempt_ms=0,revision=revision+1,updated_ms=? WHERE ` + fence + ` AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL`, Params: append([]stmt.Param{text("new_token"), text("new_instance"), integer("new_lease"), text("allocation"), text("artifact"), integer("now")}, fp...)},
		{ID: mgCaptureRenew, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET lease_until_ms=?,revision=revision+1,updated_ms=? WHERE ` + fence, Params: append([]stmt.Param{integer("new_lease"), integer("now")}, fp...)},
		{ID: mgCaptureCancel, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET state='cancelled',reason=?,cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=?,finished_ms=? WHERE ` + fence, Params: append([]stmt.Param{text("reason"), integer("now"), integer("finished")}, fp...)},
		{ID: mgCaptureAttach, Kind: stmt.Write, SQL: `UPDATE dvr_recordings SET has_dvr_recording_shares=1,share_capture_recording_id=?,revision=revision+1,updated_ms=? WHERE ` + fence + ` AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL`, Params: append([]stmt.Param{text("leader"), integer("now")}, fp...)},
	}
	for i := range defs {
		d := &defs[i]
		d.Owner = "writer.mg.capture"
		d.CallSites = []string{"writer/mg_capture_effects.go"}
		if d.ID == mgCaptureFollowersRead {
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"dvr_recordings_capture"}}
		} else {
			table := "dvr_recordings"
			if d.ID == mgCaptureAllocationRead {
				table = "live_allocations"
			}
			index := "sqlite_autoindex_" + table + "_1"
			d.SQL = strings.Replace(d.SQL, table+" WHERE", table+" INDEXED BY "+index+" WHERE", 1)
			d.SQL = strings.Replace(d.SQL, "UPDATE "+table+" SET", "UPDATE "+table+" INDEXED BY "+index+" SET", 1)
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
		}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Type == stmt.Text {
				args[j] = "missing"
			} else {
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing_native_key", Bindings: args}}
	}
	return defs
}
