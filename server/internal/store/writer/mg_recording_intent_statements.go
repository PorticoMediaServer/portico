package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	mgRecordingIntentRead                stmt.ID = "mg.recording.intent.read"
	mgRecordingIntentScope               stmt.ID = "mg.recording.intent.scope"
	mgRecordingIntentOwner               stmt.ID = "mg.recording.intent.owner"
	mgRecordingIntentReservationRead     stmt.ID = "mg.recording.intent.reservation"
	mgRecordingIntentOwnerRevisionRead   stmt.ID = "mg.recording.intent.owner_revision"
	mgRecordingIntentFloorRead           stmt.ID = "mg.recording.intent.floor"
	mgRecordingIntentInsert              stmt.ID = "mg.recording.intent.insert"
	mgRecordingIntentUpdate              stmt.ID = "mg.recording.intent.update"
	mgRecordingIntentPromote             stmt.ID = "mg.recording.intent.promote"
	mgRecordingIntentKeep                stmt.ID = "mg.recording.intent.keep"
	mgRecordingIntentReserveInsert       stmt.ID = "mg.recording.intent.reserve_insert"
	mgRecordingIntentReserveUpdate       stmt.ID = "mg.recording.intent.reserve_update"
	mgRecordingIntentDependencyInsert    stmt.ID = "mg.recording.intent.dependency"
	mgRecordingIntentOwnerRevisionInsert stmt.ID = "mg.recording.intent.owner_revision_insert"
	mgRecordingIntentOwnerRevisionUpdate stmt.ID = "mg.recording.intent.owner_revision_update"
)

func mgRecordingIntentDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	fence := `id=? AND owner_key=? AND authority='local' AND account_id=? AND profile_id=? AND revision=? AND command_incarnation=? AND command_member_incarnation=? AND command_profile_lifetime=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND state=? AND claim_token=? AND claim_generation=? AND cancel_generation=? AND owner_instance=? AND lease_until_ms=?`
	fp := []stmt.Param{s("id"), s("owner"), s("account"), s("profile"), i("revision"), b("incarnation"), s("member"), s("lifetime"), i("last"), i("ack"), s("state"), s("token"), i("claim"), i("cancel"), s("instance"), i("lease")}
	editFence := ` AND capture_launch_id IS NULL AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL AND guide_pin_generation=guide_generation`
	insertParams := []stmt.Param{s("id"), s("owner"), s("authority"), s("account"), s("profile"), s("source"), s("channel"), s("programme"), s("generation"), s("programme_json"), i("manual"), s("options_json"), i("start"), i("end"), i("priority"), i("revision"), s("state"), i("created"), i("updated"), b("incarnation"), b("operation"), b("birth_digest"), s("member"), s("lifetime"), s("guide_pin")}
	defs := []stmt.Definition{
		{ID: mgRecordingIntentRead, Kind: stmt.Read, SQL: `SELECT ` + mgCaptureColumns + `,programme_json,options_json,manual,priority,keep,created_ms FROM dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 WHERE id=?`, Params: []stmt.Param{s("id")}},
		{ID: mgRecordingIntentScope, Kind: stmt.Read, SQL: `SELECT s.revision,s.state,s.active_generation,i.has_live_source_removals,p.id,p.channel_id,p.provider_key,p.title,p.start_utc,p.end_utc,p.lineage,p.has_live_programme_metadata,p.metadata_series_id,p.metadata_episode_id,p.metadata_new_evidence,p.metadata_description,p.metadata_facts,p.has_live_programme_icons,p.icon_url
FROM live_source_identities AS i INDEXED BY sqlite_autoindex_live_source_identities_1
CROSS JOIN live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 ON s.id=i.id
CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=? AND g.source_id=s.id
CROSS JOIN live_channel_versions AS c INDEXED BY sqlite_autoindex_live_channel_versions_1 ON c.generation_id=g.id AND c.channel_id=?
CROSS JOIN live_programmes AS p INDEXED BY sqlite_autoindex_live_programmes_1 ON p.generation_id=g.id AND p.id=? AND p.channel_id=c.channel_id
WHERE i.id=?`, Params: []stmt.Param{s("generation"), s("channel"), s("programme"), s("source")}},
		{ID: mgRecordingIntentOwner, Kind: stmt.Read, SQL: `SELECT a.epoch,m.incarnation,m.revision,m.disabled,p.lifetime,p.revision,p.deleted FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1 CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_1 ON p.id=? AND p.account_id=a.id WHERE a.id=?`, Params: []stmt.Param{s("profile"), s("account")}},
		{ID: mgRecordingIntentReservationRead, Kind: stmt.Read, SQL: `SELECT source_id,start_ms,end_ms,priority,enabled FROM live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 WHERE id=?`, Params: []stmt.Param{s("id")}},
		{ID: mgRecordingIntentOwnerRevisionRead, Kind: stmt.Read, SQL: `SELECT revision FROM dvr_owner_revisions INDEXED BY sqlite_autoindex_dvr_owner_revisions_1 WHERE owner_key=?`, Params: []stmt.Param{s("owner")}},
		{ID: mgRecordingIntentFloorRead, Kind: stmt.Read, SQL: `SELECT recording_id FROM dvr_capture_retirements WHERE recording_id=?`, Params: []stmt.Param{s("id")}},
		{ID: mgRecordingIntentInsert, Kind: stmt.Write, SQL: `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,manual,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(insertParams)), ",") + `)`, Params: insertParams},
		{ID: mgRecordingIntentUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET manual=1,rule_id='',rule_revision=0,options_json=?,start_ms=?,end_ms=?,priority=?,state='scheduled',reason='',revision=revision+1,updated_ms=? WHERE ` + fence + editFence, Params: append([]stmt.Param{s("options"), i("start"), i("end"), i("priority"), i("updated")}, fp...)},
		{ID: mgRecordingIntentPromote, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET manual=1,rule_id='',rule_revision=0,revision=revision+1,updated_ms=? WHERE ` + fence, Params: append([]stmt.Param{i("updated")}, fp...)},
		{ID: mgRecordingIntentKeep, Kind: stmt.Write, SQL: `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET keep=?,revision=revision+1,updated_ms=? WHERE ` + fence, Params: append([]stmt.Param{i("keep"), i("updated")}, fp...)},
		{ID: mgRecordingIntentReserveInsert, Kind: stmt.Write, SQL: `INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES(?,?,?,?,?,?)`, Params: []stmt.Param{s("id"), s("source"), i("start"), i("end"), i("priority"), i("enabled")}},
		{ID: mgRecordingIntentReserveUpdate, Kind: stmt.Write, SQL: `UPDATE live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 SET start_ms=?,end_ms=?,priority=?,enabled=1 WHERE id=? AND source_id=? AND start_ms=? AND end_ms=? AND priority=? AND enabled=?`, Params: []stmt.Param{i("new_start"), i("new_end"), i("new_priority"), s("id"), s("source"), i("start"), i("end"), i("priority"), i("enabled")}},
		{ID: mgRecordingIntentDependencyInsert, Kind: stmt.Write, SQL: `INSERT INTO live_source_dependencies(source_id,kind,id) VALUES(?,?,?)`, Params: []stmt.Param{s("source"), s("kind"), s("id")}},
		{ID: mgRecordingIntentOwnerRevisionInsert, Kind: stmt.Write, SQL: `INSERT INTO dvr_owner_revisions(owner_key,revision) VALUES(?,?)`, Params: []stmt.Param{s("owner"), i("revision")}},
		{ID: mgRecordingIntentOwnerRevisionUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_owner_revisions INDEXED BY sqlite_autoindex_dvr_owner_revisions_1 SET revision=revision+1 WHERE owner_key=? AND revision=?`, Params: []stmt.Param{s("owner"), i("revision")}},
	}
	pins := map[string]stmt.AutoIndexBinding{}
	for _, v := range []struct {
		table   string
		columns []string
	}{{"dvr_recordings", []string{"id"}}, {"live_source_identities", []string{"id"}}, {"live_sources", []string{"id"}}, {"live_generations", []string{"id"}}, {"live_channel_versions", []string{"generation_id", "channel_id"}}, {"live_programmes", []string{"generation_id", "id"}}, {"accounts", []string{"id"}}, {"direct_memberships", []string{"account_id"}}, {"direct_profiles", []string{"id"}}, {"live_reservations", []string{"id"}}, {"dvr_owner_revisions", []string{"owner_key"}}} {
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
		if d.ID == mgRecordingIntentFloorRead {
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_capture_retirements"}}
		} else if !strings.HasPrefix(d.SQL, "INSERT ") {
			d.Plan.Hot = true
			// Read only the explicitly selected indexes in this finite compiled SQL.
			for _, part := range strings.Split(d.SQL, "INDEXED BY ")[1:] {
				index := strings.Fields(part)[0]
				if _, ok := pins[index]; ok {
					d.Plan.Indexes = append(d.Plan.Indexes, index)
					d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, pins[index])
				}
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
