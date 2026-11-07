package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mgTunerRead     stmt.ID = "mg.tuner.read"
	mgTunerResource stmt.ID = "mg.tuner.resource"
	mgTunerChannel  stmt.ID = "mg.tuner.channel"
	mgTunerShared   stmt.ID = "mg.tuner.shared"
	mgTunerNext     stmt.ID = "mg.tuner.next"
	mgTunerInsert   stmt.ID = "mg.tuner.insert"
	mgTunerRenew    stmt.ID = "mg.tuner.renew"
	mgTunerColumns          = `id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms,preempt_at_ms`
)

func mgTunerDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	cols := []stmt.Param{text("id"), text("source"), text("channel"), text("resource"), text("kind"), text("authority"), text("account"), text("profile"), text("token"), integer("generation"), text("state"), integer("lease"), integer("created"), integer("preempt")}
	defs := []stmt.Definition{
		{ID: mgTunerRead, Kind: stmt.Read, SQL: `SELECT ` + mgTunerColumns + ` FROM live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 WHERE id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgTunerResource, Kind: stmt.Read, SQL: `SELECT ` + mgTunerColumns + ` FROM live_allocations INDEXED BY sqlite_autoindex_live_allocations_2 WHERE kind=? AND resource_id=?`, Params: []stmt.Param{text("kind"), text("resource")}},
		{ID: mgTunerChannel, Kind: stmt.Read, SQL: `SELECT channel_id FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?`, Params: []stmt.Param{text("guide"), text("channel")}},
		{ID: mgTunerShared, Kind: stmt.Read, SQL: `SELECT lease_until_ms,preempt_at_ms FROM live_allocations INDEXED BY live_allocations_source_state_channel WHERE source_id=? AND state=? AND channel_id=? ORDER BY id LIMIT 1`, Params: []stmt.Param{text("source"), text("state"), text("channel")}},
		{ID: mgTunerNext, Kind: stmt.Read, SQL: `SELECT channel_id FROM live_allocations INDEXED BY live_allocations_source_state_channel WHERE source_id=? AND state=? AND channel_id>? ORDER BY channel_id,id LIMIT 1`, Params: []stmt.Param{text("source"), text("state"), text("after")}},
		{ID: mgTunerInsert, Kind: stmt.Write, SQL: `INSERT INTO live_allocations(` + mgTunerColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING ` + mgTunerColumns, Params: cols},
		{ID: mgTunerRenew, Kind: stmt.Write, SQL: `UPDATE live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 SET lease_until_ms=? WHERE id=? AND source_id=? AND channel_id=? AND resource_id=? AND kind=? AND authority=? AND account_id=? AND profile_id=? AND token=? AND generation=? AND state=? AND lease_until_ms=? AND created_ms=? AND preempt_at_ms=? RETURNING ` + mgTunerColumns, Params: append([]stmt.Param{integer("new_lease")}, cols...)},
	}
	for i := range defs {
		d := &defs[i]

		d.Plan.Hot = true
		table, index, origin := "live_allocations", "sqlite_autoindex_live_allocations_1", "pk"
		names := []string{"id"}
		switch d.ID {
		case mgTunerResource:
			index = "sqlite_autoindex_live_allocations_2"
			origin = "u"
			names = []string{"kind", "resource_id"}
		case mgTunerChannel:
			table = "live_channel_versions"
			index = "sqlite_autoindex_live_channel_versions_1"
			names = []string{"generation_id", "channel_id"}
		case mgTunerNext, mgTunerShared:
			index = "live_allocations_source_state_channel"
			origin = "c"
		case mgTunerInsert:
			index = ""
		}
		if index != "" {
			d.Plan.Indexes = []string{index}
			if origin != "c" {
				var columns []stmt.IndexColumn
				for _, n := range names {
					columns = append(columns, stmt.IndexColumn{Name: n, Collation: "BINARY"})
				}
				d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: origin, Columns: columns}}
			}
		}
		d.Owner = "writer.mg.tuner"
		d.CallSites = []string{"writer/mg_tuner_effects.go"}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Type == stmt.Text {
				args[j] = "missing"
			} else {
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing", Bindings: args}}
	}
	return defs
}
