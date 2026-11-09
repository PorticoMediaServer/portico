package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgRecordingIntentRead                stmt.ID = "writer.mg.recording.intent.read"
	mgRecordingIntentScope               stmt.ID = "writer.mg.recording.intent.scope"
	mgRecordingIntentReservationRead     stmt.ID = "writer.mg.recording.intent.reservation"
	mgRecordingIntentOwnerRevisionRead   stmt.ID = "writer.mg.recording.intent.owner-revision"
	mgRecordingIntentFloorRead           stmt.ID = "writer.mg.recording.intent.floor"
	mgRecordingIntentInsert              stmt.ID = "writer.mg.recording.intent.insert"
	mgRecordingIntentUpdate              stmt.ID = "writer.mg.recording.intent.update"
	mgRecordingIntentPromote             stmt.ID = "writer.mg.recording.intent.promote"
	mgRecordingIntentCancel              stmt.ID = "writer.mg.recording.intent.cancel"
	mgRecordingIntentKeep                stmt.ID = "writer.mg.recording.intent.keep"
	mgRecordingIntentReserveInsert       stmt.ID = "writer.mg.recording.intent.reserve-insert"
	mgRecordingIntentReserveUpdate       stmt.ID = "writer.mg.recording.intent.reserve-update"
	mgRecordingIntentReserveDisable      stmt.ID = "writer.mg.recording.intent.reserve-disable"
	mgRecordingIntentDependencyInsert    stmt.ID = "writer.mg.recording.intent.dependency-insert"
	mgRecordingIntentDependencyDelete    stmt.ID = "writer.mg.recording.intent.dependency-delete"
	mgRecordingIntentOwnerRevisionInsert stmt.ID = "writer.mg.recording.intent.owner-revision-insert"
	mgRecordingIntentOwnerRevisionUpdate stmt.ID = "writer.mg.recording.intent.owner-revision-update"
)

// The owner/member/state fence every intent mutation re-checks. Each element is
// an exact native value read in this same turn; a concurrent worker advance, a
// stale revision or a foreign owner therefore refuses instead of overwriting.
const mgRecordingIntentFence = `id=? AND owner_key=? AND authority='local' AND account_id=? AND profile_id=? AND revision=? AND command_incarnation=? AND command_member_incarnation=? AND command_profile_lifetime=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND state=? AND claim_token=? AND claim_generation=? AND cancel_generation=? AND owner_instance=? AND lease_until_ms=?`

// Window edits additionally require that no launch, share or guide-pin drift
// exists. A launched or shared occurrence is capture custody, not intent.
const mgRecordingIntentEditFence = ` AND capture_launch_id IS NULL AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL AND guide_pin_generation=guide_generation`

// Closed MG recording-intent statements: one full row point, one current
// guide/source/channel/programme point, one reservation window, the owner
// revision, the birth-retirement floor, the intent mutations and their exact
// reservation/dependency/owner-revision children. No provider, tuner,
// catalogue, filesystem or capture namespace statement appears here.
func mgRecordingIntentDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	fenceParams := []stmt.Param{s("id"), s("owner"), s("account"), s("profile"), i("revision"), b("incarnation"), s("member"), s("lifetime"), i("last"), i("ack"), s("state"), s("token"), i("claim"), i("cancel"), s("instance"), i("lease")}
	insertParams := []stmt.Param{s("id"), s("owner"), s("authority"), s("account"), s("profile"), s("source"), s("channel"), s("programme"), s("generation"), s("programme_json"), i("manual"), s("options_json"), i("start"), i("end"), i("priority"), i("revision"), s("state"), i("created"), i("updated"), b("incarnation"), b("operation"), b("birth_digest"), s("member"), s("lifetime"), s("guide_pin")}
	defs := []stmt.Definition{
		{ID: mgRecordingIntentRead, Kind: stmt.Read, SQL: `SELECT id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,rule_id,rule_revision,start_ms,end_ms,revision,state,reason,keep,cancel_generation,claim_generation,claim_token,next_attempt_ms,recovery_attempt,lease_until_ms,owner_instance,allocation_id,item_id,artifact_id,artifact_digest,has_dvr_recording_shares,share_capture_recording_id,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,last_command_sequence,acknowledged_command_sequence,guide_pin_generation,capture_launch_id,capture_allocation_generation,capture_allocation_token,capture_source_revision,capture_source_fence,capture_writer_retired_sequence,capture_physical_exit_sequence,capture_shared_ready_sequence,created_ms,updated_ms,finished_ms,programme_json,options_json,manual,priority,has_dvr_programme_images,programme_image_url FROM dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 WHERE id=?`, Params: []stmt.Param{s("id")}},
		{ID: mgRecordingIntentScope, Kind: stmt.Read, SQL: `SELECT s.revision,s.state,s.active_generation,i.has_live_source_removals,p.id,p.channel_id,p.provider_key,p.title,p.start_utc,p.end_utc,p.lineage,p.has_live_programme_metadata,p.metadata_series_id,p.metadata_episode_id,p.metadata_new_evidence,p.metadata_description,p.metadata_facts,p.has_live_programme_icons,p.icon_url
FROM live_source_identities AS i INDEXED BY sqlite_autoindex_live_source_identities_1
CROSS JOIN live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 ON s.id=i.id
CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=? AND g.source_id=s.id
CROSS JOIN live_channel_versions AS c INDEXED BY sqlite_autoindex_live_channel_versions_1 ON c.generation_id=g.id AND c.channel_id=?
CROSS JOIN live_programmes AS p INDEXED BY sqlite_autoindex_live_programmes_1 ON p.generation_id=g.id AND p.id=? AND p.channel_id=c.channel_id
WHERE i.id=?`, Params: []stmt.Param{s("generation"), s("channel"), s("programme"), s("source")}},
		{ID: mgRecordingIntentReservationRead, Kind: stmt.Read, SQL: `SELECT source_id,start_ms,end_ms,priority,enabled FROM live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 WHERE id=?`, Params: []stmt.Param{s("id")}},
		{ID: mgRecordingIntentOwnerRevisionRead, Kind: stmt.Read, SQL: `SELECT revision FROM dvr_owner_revisions INDEXED BY sqlite_autoindex_dvr_owner_revisions_1 WHERE owner_key=?`, Params: []stmt.Param{s("owner")}},
		{ID: mgRecordingIntentFloorRead, Kind: stmt.Read, SQL: `SELECT recording_id FROM dvr_capture_retirements WHERE recording_id=?`, Params: []stmt.Param{s("id")}},
		{ID: mgRecordingIntentInsert, Kind: stmt.Write, SQL: `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,manual,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(insertParams)), ",") + `)`, Params: insertParams},
		{ID: mgRecordingIntentUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET manual=1,rule_id='',rule_revision=0,options_json=?,start_ms=?,end_ms=?,priority=?,state='scheduled',reason='',revision=revision+1,updated_ms=? WHERE ` + mgRecordingIntentFence + mgRecordingIntentEditFence, Params: append([]stmt.Param{s("options"), i("start"), i("end"), i("priority"), i("updated")}, fenceParams...)},
		{ID: mgRecordingIntentPromote, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET manual=1,rule_id='',rule_revision=0,revision=revision+1,updated_ms=? WHERE ` + mgRecordingIntentFence, Params: append([]stmt.Param{i("updated")}, fenceParams...)},
		{ID: mgRecordingIntentCancel, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET state='cancelled',reason='owner-cancelled',cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=?,finished_ms=? WHERE ` + mgRecordingIntentFence, Params: append([]stmt.Param{i("updated"), i("finished")}, fenceParams...)},
		{ID: mgRecordingIntentKeep, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET keep=?,revision=revision+1,updated_ms=? WHERE ` + mgRecordingIntentFence, Params: append([]stmt.Param{i("keep"), i("updated")}, fenceParams...)},
		{ID: mgRecordingIntentReserveInsert, Kind: stmt.Write, SQL: `INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES(?,?,?,?,?,?)`, Params: []stmt.Param{s("id"), s("source"), i("start"), i("end"), i("priority"), i("enabled")}},
		{ID: mgRecordingIntentReserveUpdate, Kind: stmt.Write, SQL: `UPDATE live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 SET start_ms=?,end_ms=?,priority=?,enabled=1 WHERE id=? AND source_id=? AND start_ms=? AND end_ms=? AND priority=? AND enabled=?`, Params: []stmt.Param{i("new_start"), i("new_end"), i("new_priority"), s("id"), s("source"), i("start"), i("end"), i("priority"), i("enabled")}},
		{ID: mgRecordingIntentReserveDisable, Kind: stmt.Write, SQL: `UPDATE live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 SET enabled=0 WHERE id=? AND source_id=? AND start_ms=? AND end_ms=? AND priority=? AND enabled=?`, Params: []stmt.Param{s("id"), s("source"), i("start"), i("end"), i("priority"), i("enabled")}},
		{ID: mgRecordingIntentDependencyInsert, Kind: stmt.Write, SQL: `INSERT INTO live_source_dependencies(source_id,kind,id) VALUES(?,?,?)`, Params: []stmt.Param{s("source"), s("kind"), s("id")}},
		{ID: mgRecordingIntentDependencyDelete, Kind: stmt.Write, SQL: `DELETE FROM live_source_dependencies INDEXED BY sqlite_autoindex_live_source_dependencies_1 WHERE source_id=? AND kind='recording' AND id=?`, Params: []stmt.Param{s("source"), s("id")}},
		{ID: mgRecordingIntentOwnerRevisionInsert, Kind: stmt.Write, SQL: `INSERT INTO dvr_owner_revisions(owner_key,revision) VALUES(?,?)`, Params: []stmt.Param{s("owner"), i("revision")}},
		{ID: mgRecordingIntentOwnerRevisionUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_owner_revisions INDEXED BY sqlite_autoindex_dvr_owner_revisions_1 SET revision=revision+1 WHERE owner_key=? AND revision=?`, Params: []stmt.Param{s("owner"), i("revision")}},
	}
	pins := map[string]stmt.AutoIndexBinding{}
	for _, v := range []struct {
		table   string
		columns []string
	}{
		{"dvr_recordings", []string{"id"}},
		{"live_source_identities", []string{"id"}},
		{"live_sources", []string{"id"}},
		{"live_generations", []string{"id"}},
		{"live_channel_versions", []string{"generation_id", "channel_id"}},
		{"live_programmes", []string{"generation_id", "id"}},
		{"live_reservations", []string{"id"}},
		{"live_source_dependencies", []string{"source_id", "kind", "id"}},
		{"dvr_owner_revisions", []string{"owner_key"}},
	} {
		name := "sqlite_autoindex_" + v.table + "_1"
		binding := stmt.AutoIndexBinding{Table: v.table, Name: name, Origin: "pk"}
		for _, c := range v.columns {
			binding.Columns = append(binding.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		pins[name] = binding
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "writer.mg.recording.intent"
		d.CallSites = []string{"writer/mg_recording_intent_effects.go"}
		switch d.ID {
		case mgRecordingIntentFloorRead:
			// WITHOUT ROWID primary-key table: the PK IS the access path.
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_capture_retirements"}}
		case mgRecordingIntentDependencyDelete:
			// One exact (source,kind,id) key; the caller has already proved the
			// cancelled row carries no artifact/item debt.
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_dependencies_1"}, AutoIndexes: []stmt.AutoIndexBinding{pins["sqlite_autoindex_live_source_dependencies_1"]}, Seeks: []string{"source_id=? AND kind=? AND id=?"}}
		case mgRecordingIntentInsert, mgRecordingIntentReserveInsert, mgRecordingIntentDependencyInsert, mgRecordingIntentOwnerRevisionInsert:
			// Plain INSERT destinations are audited by Capture's index/FK
			// inventory, not by a destination SEARCH.
		default:
			d.Plan.Hot = true
			for _, part := range strings.Split(d.SQL, "INDEXED BY ")[1:] {
				index := strings.Fields(part)[0]
				if _, ok := pins[index]; ok {
					d.Plan.Indexes = append(d.Plan.Indexes, index)
					d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, pins[index])
				}
			}
			switch d.ID {
			case mgRecordingIntentRead:
				d.Plan.Seeks = []string{"id=?"}
			case mgRecordingIntentScope:
				d.Plan.Seeks = []string{"id=?", "id=?", "id=?", "generation_id=? AND channel_id=?", "generation_id=? AND id=?"}
			case mgRecordingIntentReservationRead, mgRecordingIntentReserveUpdate, mgRecordingIntentReserveDisable:
				d.Plan.Seeks = []string{"id=?"}
			case mgRecordingIntentOwnerRevisionRead, mgRecordingIntentOwnerRevisionUpdate:
				d.Plan.Seeks = []string{"owner_key=?"}
			case mgRecordingIntentUpdate, mgRecordingIntentPromote, mgRecordingIntentCancel, mgRecordingIntentKeep:
				d.Plan.Seeks = []string{"id=?"}
			}
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
