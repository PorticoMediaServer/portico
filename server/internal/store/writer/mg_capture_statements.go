package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgCaptureRead           stmt.ID = "writer.mg.capture.recording"
	mgCaptureSourceRead     stmt.ID = "writer.mg.capture.source"
	mgCaptureAllocationRead stmt.ID = "writer.mg.capture.allocation"
	mgCaptureLaunchRead     stmt.ID = "writer.mg.capture.launch"
	mgCaptureClaim          stmt.ID = "writer.mg.capture.claim"
	mgCaptureRenew          stmt.ID = "writer.mg.capture.renew"
	mgCaptureCheckpoint     stmt.ID = "writer.mg.capture.checkpoint"
)

// The exact pre-effect capture tuple. Launch, share launch and guide pin are
// nullable comparisons (IS NULL), so an absent value matches only an absent value.
const mgCaptureFenceSQL = `id=? AND owner_key=? AND revision=? AND state=? AND claim_token=? AND claim_generation=? AND cancel_generation=? AND owner_instance=? AND lease_until_ms=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND coalesce(capture_launch_id,'')=coalesce(?,'') AND allocation_id=? AND capture_allocation_generation=? AND capture_allocation_token=? AND capture_source_revision=? AND capture_source_fence=? AND capture_writer_retired_sequence=? AND capture_physical_exit_sequence=? AND capture_shared_ready_sequence=? AND share_capture_claim_generation=? AND coalesce(share_capture_launch_id,'')=coalesce(?,'') AND coalesce(guide_pin_generation,'')=coalesce(?,'')`

// Closed MG capture statements: one full recording point, the current source
// head point, the exact allocation point, the launch-uniqueness point and the
// three capture custody mutations. No provider, tuner, catalogue, filesystem,
// artifact or physical-exit statement appears here.
func mgCaptureDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	sn := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	bn := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	fence := []stmt.Param{s("id"), s("owner"), i("revision"), s("state"), s("token"), i("claim"), i("cancel"),
		s("instance"), i("lease"), b("incarnation"), i("last"), i("ack"), bn("launch"), s("allocation"),
		i("allocation_generation"), s("allocation_token"), i("source_revision"), s("source_fence"),
		i("writer_retired"), i("physical_exit"), i("shared_ready"), i("share_claim"), bn("share_launch"), sn("guide_pin")}
	claimParams := []stmt.Param{s("new_token"), s("new_instance"), i("new_lease"), s("claim_allocation"), s("artifact"),
		i("now"), b("new_launch"), i("new_allocation_generation"), s("new_allocation_token"),
		i("new_source_revision"), s("new_source_fence")}
	pk := func(table, column string) stmt.Plan {
		name := "sqlite_autoindex_" + table + "_1"
		return stmt.Plan{Hot: true, Indexes: []string{name}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}}}
	}
	defs := []stmt.Definition{
		{ID: mgCaptureRead, Kind: stmt.Read, SQL: `SELECT ` + mgCaptureColumns + ` FROM dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 WHERE id=?`, Params: []stmt.Param{s("id")}, Plan: pk("dvr_recordings", "id")},
		{ID: mgCaptureSourceRead, Kind: stmt.Read, SQL: `SELECT s.revision,s.state,s.active_generation,i.has_live_source_removals
FROM live_source_identities AS i INDEXED BY sqlite_autoindex_live_source_identities_1
CROSS JOIN live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 ON s.id=i.id
WHERE i.id=?`, Params: []stmt.Param{s("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_identities_1", "sqlite_autoindex_live_sources_1"}, AutoIndexes: []stmt.AutoIndexBinding{
			{Table: "live_source_identities", Name: "sqlite_autoindex_live_source_identities_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
			{Table: "live_sources", Name: "sqlite_autoindex_live_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
		}, Seeks: []string{"id=?"}}},
		{ID: mgCaptureAllocationRead, Kind: stmt.Read, SQL: `SELECT source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,preempt_at_ms FROM live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 WHERE id=?`, Params: []stmt.Param{s("allocation")}, Plan: pk("live_allocations", "id")},
		{ID: mgCaptureLaunchRead, Kind: stmt.Read, SQL: `SELECT id FROM dvr_recordings INDEXED BY dvr_recordings_capture_launch WHERE capture_launch_id=? AND capture_launch_id IS NOT NULL LIMIT 1`, Params: []stmt.Param{b("launch")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"dvr_recordings_capture_launch"}}},
		{ID: mgCaptureClaim, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET state='preparing',reason='',claim_token=?,claim_generation=claim_generation+1,owner_instance=?,lease_until_ms=?,allocation_id=?,artifact_id=?,next_attempt_ms=0,revision=revision+1,updated_ms=?,capture_launch_id=?,capture_allocation_generation=?,capture_allocation_token=?,capture_source_revision=?,capture_source_fence=?,last_command_sequence=last_command_sequence+1 WHERE ` + mgCaptureFenceSQL + ` AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL`, Params: append(claimParams, fence...), Plan: pk("dvr_recordings", "id")},
		{ID: mgCaptureRenew, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET lease_until_ms=?,revision=revision+1,updated_ms=?,last_command_sequence=last_command_sequence+1 WHERE ` + mgCaptureFenceSQL, Params: append([]stmt.Param{i("new_lease"), i("now")}, fence...), Plan: pk("dvr_recordings", "id")},
		{ID: mgCaptureCheckpoint, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET captured_start_ms=?,captured_end_ms=?,capture_gap_count=?,capture_producer_succeeded=?,capture_boundary_reached=?,revision=revision+1,updated_ms=?,last_command_sequence=last_command_sequence+1 WHERE ` + mgCaptureFenceSQL, Params: append([]stmt.Param{i("captured_start"), i("captured_end"), i("gap_count"), i("producer_succeeded"), i("boundary_reached"), i("now")}, fence...), Plan: pk("dvr_recordings", "id")},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "writer.mg.capture"
		d.CallSites = []string{"writer/mg_capture_effects.go"}
		for _, id := range []stmt.ID{mgCaptureRead, mgCaptureLaunchRead, mgCaptureClaim, mgCaptureRenew, mgCaptureCheckpoint} {
			if d.ID == id {
				d.Plan.Seeks = append(d.Plan.Seeks, "id=?")
			}
		}
		if d.ID == mgCaptureLaunchRead {
			d.Plan.Seeks = []string{"capture_launch_id=?"}
		}
		if d.ID == mgCaptureAllocationRead {
			d.Plan.Seeks = []string{"id=?"}
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
		d.Cohorts = []stmt.Cohort{{Name: "missing_native_capture", Bindings: args}}
	}
	return defs
}
