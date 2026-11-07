package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mgPersonalLiveChannel      stmt.ID = "writer.mg.personal.live-channel"
	mgPersonalLibraryChannel   stmt.ID = "writer.mg.personal.library-channel"
	mgPersonalProgramme        stmt.ID = "writer.mg.personal.programme"
	mgPersonalOccurrence       stmt.ID = "writer.mg.personal.occurrence"
	mgPersonalPreferenceRead   stmt.ID = "writer.mg.personal.preference-read"
	mgPersonalPreferenceInsert stmt.ID = "writer.mg.personal.preference-insert"
	mgPersonalPreferenceUpdate stmt.ID = "writer.mg.personal.preference-update"
	mgPersonalReminderRead     stmt.ID = "writer.mg.personal.reminder-read"
	mgPersonalReminderInsert   stmt.ID = "writer.mg.personal.reminder-insert"
	mgPersonalReminderUpdate   stmt.ID = "writer.mg.personal.reminder-update"
	mgPersonalReminderDelete   stmt.ID = "writer.mg.personal.reminder-delete"
)
const mgPersonalReminderColumns = `id,owner_key,kind,source_id,channel_id,generation_id,programme_id,revision,start_utc,external_generation_ref,library_generation_ref`

// Only fixed point shapes. Registration/Capture, full recipe forecasting and
// shared descriptor aggregation are parent integration; these declare no API.
func mgPersonalDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	owner := []stmt.Param{s("authority"), s("account"), s("profile"), s("source"), s("channel")}
	pk := `authority=? AND account_id=? AND profile_id=? AND source_id=? AND channel_id=?`
	defs := []stmt.Definition{
		{ID: mgPersonalLiveChannel, Kind: stmt.Read, SQL: `SELECT v.revision,v.active_generation FROM live_sources AS v INDEXED BY sqlite_autoindex_live_sources_1 CROSS JOIN live_source_identities AS o INDEXED BY sqlite_autoindex_live_source_identities_1 ON o.id=v.id CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=v.active_generation AND g.source_id=v.id CROSS JOIN live_channel_versions AS c INDEXED BY sqlite_autoindex_live_channel_versions_1 ON c.generation_id=g.id WHERE v.id=? AND v.revision=? AND v.active_generation=? AND c.channel_id=? AND v.state='active' AND o.has_live_source_removals=0`, Params: []stmt.Param{s("source"), i("revision"), s("generation"), s("channel")}},
		{ID: mgPersonalLibraryChannel, Kind: stmt.Read, SQL: `SELECT c.revision,c.active_generation FROM lc_channels AS c INDEXED BY sqlite_autoindex_lc_channels_1 CROSS JOIN lc_generations AS g INDEXED BY sqlite_autoindex_lc_generations_1 ON g.id=c.active_generation AND g.channel_id=c.id WHERE c.id=? AND c.revision=? AND c.active_generation=? AND c.enabled=1 AND c.removed=0 AND g.status='published'`, Params: []stmt.Param{s("channel"), i("revision"), s("generation")}},
		{ID: mgPersonalProgramme, Kind: stmt.Read, SQL: `SELECT start_utc,end_utc FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id=? AND channel_id=?`, Params: []stmt.Param{s("generation"), s("programme"), s("channel")}},
		{ID: mgPersonalOccurrence, Kind: stmt.Read, SQL: `SELECT start_ms,end_ms,source_offset_ms FROM lc_entries INDEXED BY sqlite_autoindex_lc_entries_1 WHERE generation_id=? AND occurrence_id=? AND channel_id=? AND item_id=? AND library_id=? AND asset_id=? AND source_fence=? AND slate_reason=''`, Params: []stmt.Param{s("generation"), s("occurrence"), s("channel"), i("item"), s("library"), s("asset"), s("source")}},
		{ID: mgPersonalPreferenceRead, Kind: stmt.Read, SQL: `SELECT favorite,hidden,revision FROM live_channel_preferences INDEXED BY sqlite_autoindex_live_channel_preferences_1 WHERE ` + pk, Params: owner},
		{ID: mgPersonalPreferenceInsert, Kind: stmt.Write, SQL: `INSERT INTO live_channel_preferences(authority,account_id,profile_id,source_id,channel_id,favorite,hidden,revision) VALUES(?,?,?,?,?,?,?,?) RETURNING favorite,hidden,revision`, Params: append(append([]stmt.Param{}, owner...), i("favorite"), i("hidden"), i("initial_revision"))},
		{ID: mgPersonalPreferenceUpdate, Kind: stmt.Write, SQL: `UPDATE live_channel_preferences INDEXED BY sqlite_autoindex_live_channel_preferences_1 SET favorite=?,hidden=?,revision=revision+1 WHERE ` + pk + ` AND revision=? RETURNING favorite,hidden,revision`, Params: append(append([]stmt.Param{i("favorite"), i("hidden")}, owner...), i("revision"))},
		{ID: mgPersonalReminderRead, Kind: stmt.Read, SQL: `SELECT ` + mgPersonalReminderColumns + ` FROM live_reminders INDEXED BY sqlite_autoindex_live_reminders_1 WHERE id=? AND owner_key=?`, Params: []stmt.Param{s("id"), s("owner")}},
		{ID: mgPersonalReminderInsert, Kind: stmt.Write, SQL: `INSERT INTO live_reminders(id,owner_key,kind,source_id,channel_id,generation_id,programme_id,revision,start_utc) VALUES(?,?,?,?,?,?,?,?,?) RETURNING ` + mgPersonalReminderColumns, Params: []stmt.Param{s("id"), s("owner"), s("kind"), s("source"), s("channel"), s("generation"), s("programme"), i("initial_revision"), s("start")}},
		{ID: mgPersonalReminderUpdate, Kind: stmt.Write, SQL: `UPDATE live_reminders INDEXED BY sqlite_autoindex_live_reminders_1 SET revision=revision+1 WHERE id=? AND owner_key=? AND revision=? AND kind=? AND source_id=? AND channel_id=? AND generation_id=? AND programme_id=? AND start_utc=? RETURNING ` + mgPersonalReminderColumns, Params: []stmt.Param{s("id"), s("owner"), i("revision"), s("kind"), s("source"), s("channel"), s("generation"), s("programme"), i("initial_revision"), s("start")}},
		{ID: mgPersonalReminderDelete, Kind: stmt.Write, SQL: `DELETE FROM live_reminders INDEXED BY sqlite_autoindex_live_reminders_1 WHERE id=? AND owner_key=? AND revision=? RETURNING ` + mgPersonalReminderColumns, Params: []stmt.Param{s("id"), s("owner"), i("revision")}},
	}
	// Each generated index is pinned to its complete actual PK, including joined
	// scope keys; the plan harness must independently verify these declarations.
	pin := func(table string, columns ...string) stmt.AutoIndexBinding {
		p := stmt.AutoIndexBinding{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk"}
		for _, c := range columns {
			p.Columns = append(p.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return p
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "writer.mg.personal"
		d.CallSites = []string{"writer/mg_personal_effects.go"}
		switch d.ID {
		case mgPersonalLiveChannel:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("live_sources", "id"), pin("live_source_identities", "id"), pin("live_generations", "id"), pin("live_channel_versions", "generation_id", "channel_id")}
		case mgPersonalLibraryChannel:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("lc_channels", "id"), pin("lc_generations", "id")}
		case mgPersonalProgramme:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("live_programmes", "generation_id", "id")}
		case mgPersonalOccurrence:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("lc_entries", "generation_id", "occurrence_id")}
		case mgPersonalPreferenceRead, mgPersonalPreferenceUpdate:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("live_channel_preferences", "authority", "account_id", "profile_id", "source_id", "channel_id")}
		case mgPersonalReminderRead, mgPersonalReminderUpdate, mgPersonalReminderDelete:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("live_reminders", "id")}
		}
		for _, p := range d.Plan.AutoIndexes {
			d.Plan.Indexes = append(d.Plan.Indexes, p.Name)
		}
		d.Plan.Hot = true
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Type == stmt.Text {
				args[j] = "missing"
			} else {
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return defs
}
func (r mgPersonalPreferenceRequest) ownerValues() []any {
	return []any{r.owner.authority, r.owner.account, r.owner.profile, r.target.preferenceSource(), r.target.channel}
}
