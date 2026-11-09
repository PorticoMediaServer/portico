package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mgTunerRead        stmt.ID = "writer.mg.tuner.read"
	mgTunerResource    stmt.ID = "writer.mg.tuner.resource"
	mgTunerChannel     stmt.ID = "writer.mg.tuner.channel"
	mgTunerShared      stmt.ID = "writer.mg.tuner.shared"
	mgTunerNext        stmt.ID = "writer.mg.tuner.next"
	mgTunerReservation stmt.ID = "writer.mg.tuner.reservation"
	mgTunerInsert      stmt.ID = "writer.mg.tuner.insert"
	mgTunerRenew       stmt.ID = "writer.mg.tuner.renew"
	mgTunerColumns             = `id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms,preempt_at_ms`
)

// Point and bounded-stream access only: allocation PK, the kind/resource
// UNIQUE key, the current head channel PK, the source/state/channel stream in
// both held states, the enabled reservation window, and the exact tuple CAS.
// No capacity scan, discovery metadata read, payload, reservation selection or
// physical release appears here.
func mgTunerDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	cols := []stmt.Param{text("id"), text("source"), text("channel"), text("resource"), text("kind"), text("authority"), text("account"), text("profile"), text("token"), integer("generation"), text("state"), integer("lease"), integer("created"), integer("preempt")}
	pk := func(table, column string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
	}
	defs := []stmt.Definition{
		{ID: mgTunerRead, Kind: stmt.Read, SQL: `SELECT ` + mgTunerColumns + ` FROM live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 WHERE id=?`, Params: []stmt.Param{text("id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_allocations_1"}, AutoIndexes: []stmt.AutoIndexBinding{pk("live_allocations", "id")}}},
		{ID: mgTunerResource, Kind: stmt.Read, SQL: `SELECT ` + mgTunerColumns + ` FROM live_allocations INDEXED BY sqlite_autoindex_live_allocations_2 WHERE kind=? AND resource_id=?`, Params: []stmt.Param{text("kind"), text("resource")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_allocations_2"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "live_allocations", Name: "sqlite_autoindex_live_allocations_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "kind", Collation: "BINARY"}, {Name: "resource_id", Collation: "BINARY"}}}}}},
		{ID: mgTunerChannel, Kind: stmt.Read, SQL: `SELECT channel_id FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?`, Params: []stmt.Param{text("guide"), text("channel")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_channel_versions_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "live_channel_versions", Name: "sqlite_autoindex_live_channel_versions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: "channel_id", Collation: "BINARY"}}}}}},
		{ID: mgTunerShared, Kind: stmt.Read, SQL: `SELECT lease_until_ms,preempt_at_ms FROM live_allocations INDEXED BY live_allocations_source_state_channel WHERE source_id=? AND state=? AND channel_id=? ORDER BY id LIMIT 1`, Params: []stmt.Param{text("source"), text("state"), text("channel")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"live_allocations_source_state_channel"}}},
		{ID: mgTunerNext, Kind: stmt.Read, SQL: `SELECT channel_id FROM live_allocations INDEXED BY live_allocations_source_state_channel WHERE source_id=? AND state=? AND channel_id>? ORDER BY channel_id,id LIMIT 1`, Params: []stmt.Param{text("source"), text("state"), text("after")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"live_allocations_source_state_channel"}}},
		{ID: mgTunerReservation, Kind: stmt.Read, SQL: `SELECT start_ms FROM live_reservations INDEXED BY live_reservation_window WHERE source_id=? AND enabled=1 AND start_ms=? AND end_ms>? LIMIT 1`, Params: []stmt.Param{text("source"), integer("start"), integer("now")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"live_reservation_window"}}},
		{ID: mgTunerInsert, Kind: stmt.Write, SQL: `INSERT INTO live_allocations(` + mgTunerColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING ` + mgTunerColumns, Params: cols},
		{ID: mgTunerRenew, Kind: stmt.Write, SQL: `UPDATE live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 SET lease_until_ms=? WHERE id=? AND source_id=? AND channel_id=? AND resource_id=? AND kind=? AND authority=? AND account_id=? AND profile_id=? AND token=? AND generation=? AND state=? AND lease_until_ms=? AND created_ms=? AND preempt_at_ms=? RETURNING ` + mgTunerColumns, Params: append([]stmt.Param{integer("new_lease")}, cols...), Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_allocations_1"}, AutoIndexes: []stmt.AutoIndexBinding{pk("live_allocations", "id")}}},
	}
	for i := range defs {
		d := &defs[i]
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
		d.Cohorts = []stmt.Cohort{{Name: "held-tuple", Bindings: args}}
	}
	return defs
}
