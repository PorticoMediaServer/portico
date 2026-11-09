package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// MG source commands consume MG-07's ONE public root adapter for the owner,
// receipt and payload statements. This file adds only the source-family ACK
// floor statement the adapter does not need for preferences/reminders.
const mgSourceCommandOwnerAck stmt.ID = "mg.source.command.ack"

func mgSourceCommandDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: mgSourceCommandOwnerAck, Kind: stmt.Write, SQL: `UPDATE user_mutation_command_owners INDEXED BY sqlite_autoindex_user_mutation_command_owners_1
SET acknowledged_command_sequence=?
WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
RETURNING acknowledged_command_sequence`,
			Params: []stmt.Param{i("next"), s("root"), b("incarnation"), i("last"), i("ack")}},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "writer.mg.source.commands"
		d.CallSites = []string{"writer/mg_source_commands.go"}
		d.Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_user_mutation_command_owners_1"}, Seeks: []string{"root_id=?"},
			AutoIndexes: []stmt.AutoIndexBinding{{Table: "user_mutation_command_owners", Name: "sqlite_autoindex_user_mutation_command_owners_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "root_id", Collation: "BINARY"}}}}}
		d.Cohorts = []stmt.Cohort{{Name: "missing_receipt_owner", Bindings: []any{int64(1), "missing", make([]byte, 16), int64(1), int64(0)}}}
	}
	return defs
}
