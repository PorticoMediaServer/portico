package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed native statements for the LC channel control family (868-871). They
// reuse the adopted G/013 lc_channels/lc_generations/lc_playback_refs/lc_receipts
// tables and add no DDL. The channel keeps its stock non-null active_generation
// text; the generated active_generation_ref remains the only nullable head.
const (
	mgLCControlRead          stmt.ID = "writer.mg.lc.control.read"
	mgLCControlDirectory     stmt.ID = "writer.mg.lc.control.directory"
	mgLCControlInsert        stmt.ID = "writer.mg.lc.control.insert"
	mgLCControlUpdate        stmt.ID = "writer.mg.lc.control.update"
	mgLCControlPending       stmt.ID = "writer.mg.lc.control.pending"
	mgLCControlSupersede     stmt.ID = "writer.mg.lc.control.supersede"
	mgLCControlGeneration    stmt.ID = "writer.mg.lc.control.generation"
	mgLCControlRefs          stmt.ID = "writer.mg.lc.control.refs"
	mgLCControlPendingAlign  stmt.ID = "writer.mg.lc.control.pending-align"
	mgLCControlReceiptRead   stmt.ID = "writer.mg.lc.control.receipt-read"
	mgLCControlReceiptInsert stmt.ID = "writer.mg.lc.control.receipt-save"
)

const (
	mgLCControlChannelColumns       = `id,revision,config_json,enabled,position,name,active_generation,state,health_code,generated_through_ms,removed`
	mgLCControlPendingColumns       = `id,config_revision,config_json,status,error_code,lease_token,lease_until_ms,last_command_sequence,acknowledged_command_sequence,stage_revision,entry_count,staged_bytes,entry_digest,receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms`
	mgLCControlDirectoryLimit       = 65 // one more than the 64-channel bound
	mgLCControlMaxChannels    int64 = 64
	mgLCControlConfigBytes          = 1 << 16
)

func mgLCControlDefinitions() []stmt.Definition {
	p := func(n string, typ stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: typ} }
	t, i, x := stmt.Text, stmt.Integer, stmt.Blob
	defs := []stmt.Definition{
		{ID: mgLCControlRead, Kind: stmt.Read, SQL: `SELECT ` + mgLCControlChannelColumns + ` FROM lc_channels INDEXED BY sqlite_autoindex_lc_channels_1 WHERE id=?`,
			Params: []stmt.Param{p("channel", t)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_channels_1"}, Seeks: []string{"id=?"}}},
		{ID: mgLCControlDirectory, Kind: stmt.Read, SQL: `SELECT ` + mgLCControlChannelColumns + ` FROM lc_channels INDEXED BY lc_directory WHERE enabled=? AND removed=0 ORDER BY position,id LIMIT 65`,
			Params: []stmt.Param{p("enabled", i)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"lc_directory"}, Seeks: []string{"enabled=?", "removed=0"}}},
		{ID: mgLCControlInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state) VALUES(?,?,?,?,?,?,?,?) RETURNING ` + mgLCControlChannelColumns,
			Params: []stmt.Param{p("id", t), p("revision", i), p("config", t), p("enabled", i), p("position", i), p("name", t), p("head", t), p("state", t)}},
		{ID: mgLCControlUpdate, Kind: stmt.Write, SQL: `UPDATE lc_channels INDEXED BY sqlite_autoindex_lc_channels_1 SET revision=?,config_json=?,enabled=?,position=?,name=?,state=?,health_code=?,removed=? WHERE id=? AND revision=? AND config_json=? AND enabled=? AND position=? AND name=? AND state=? AND health_code=? AND removed=? AND active_generation=? RETURNING ` + mgLCControlChannelColumns,
			Params: []stmt.Param{
				p("new_revision", i), p("new_config", t), p("new_enabled", i), p("new_position", i), p("new_name", t),
				p("new_state", t), p("new_health", t), p("new_removed", i),
				p("old_id", t), p("old_revision", i), p("old_config", t), p("old_enabled", i), p("old_position", i),
				p("old_name", t), p("old_state", t), p("old_health", t), p("old_removed", i), p("old_head", t),
			},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_channels_1"}, Seeks: []string{"id=?"}}},
		{ID: mgLCControlPending, Kind: stmt.Read, SQL: `SELECT ` + mgLCControlPendingColumns + ` FROM lc_generations INDEXED BY lc_one_job WHERE channel_id=? AND status='pending'`,
			Params: []stmt.Param{p("channel", t)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"lc_one_job"}, Seeks: []string{"channel_id=?", "status='pending'"}}},
		{ID: mgLCControlSupersede, Kind: stmt.Write, SQL: `UPDATE lc_generations INDEXED BY sqlite_autoindex_lc_generations_1 SET status='superseded',error_code=? WHERE id=? AND channel_id=? AND config_revision=? AND status='pending' AND lease_token=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND entry_count=? AND staged_bytes=? AND entry_digest=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? RETURNING ` + mgLCControlPendingColumns,
			Params: []stmt.Param{
				p("error", t), p("id", t), p("channel", t), p("config_revision", i), p("lease", t), p("lease_until", i),
				p("last", i), p("ack", i), p("stage_revision", i), p("entries", i), p("bytes", i), p("entry_digest", x),
				p("receipt_rows", i), p("receipt_bytes", i), p("reclaim_after", i),
			},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_generations_1"}, Seeks: []string{"id=?"}}},
		{ID: mgLCControlGeneration, Kind: stmt.Read, SQL: `SELECT ` + mgLCControlPendingColumns + ` FROM lc_generations INDEXED BY sqlite_autoindex_lc_generations_1 WHERE id=?`,
			Params: []stmt.Param{p("id", t)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_generations_1"}, Seeks: []string{"id=?"}}},
		{ID: mgLCControlRefs, Kind: stmt.Read, SQL: `SELECT playback_id FROM lc_playback_refs INDEXED BY lc_ref_channel WHERE channel_id=? LIMIT 1`,
			Params: []stmt.Param{p("channel", t)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"lc_ref_channel"}, Seeks: []string{"channel_id=?"}}},
		{ID: mgLCControlPendingAlign, Kind: stmt.Write, SQL: `UPDATE lc_generations INDEXED BY lc_one_job SET config_revision=?,config_json=? WHERE channel_id=? AND status='pending' AND config_revision=? RETURNING ` + mgLCControlPendingColumns,
			Params: []stmt.Param{p("revision", i), p("config", t), p("channel", t), p("old_revision", i)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"lc_one_job"}, Seeks: []string{"channel_id=?", "status='pending'"}}},
		{ID: mgLCControlReceiptRead, Kind: stmt.Read, SQL: `SELECT digest,result_json FROM lc_receipts INDEXED BY sqlite_autoindex_lc_receipts_1 WHERE request_id=?`,
			Params: []stmt.Param{p("request", t)},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_receipts_1"}, Seeks: []string{"request_id=?"}}},
		{ID: mgLCControlReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_receipts(request_id,digest,result_json) VALUES(?,?,?)`,
			Params: []stmt.Param{p("request", t), p("digest", t), p("result", t)}},
	}
	binary := func(names ...string) []stmt.IndexColumn {
		out := make([]stmt.IndexColumn, 0, len(names))
		for _, name := range names {
			out = append(out, stmt.IndexColumn{Name: name, Collation: "BINARY"})
		}
		return out
	}
	for k := range defs {
		d := &defs[k]
		d.Owner = "writer.mg.lc.control"
		d.CallSites = []string{"writer/mg_lc_control.go", "writer/mg_lc_control_dispatch.go"}
		switch d.ID {
		case mgLCControlRead, mgLCControlUpdate:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "lc_channels", Name: "sqlite_autoindex_lc_channels_1", Origin: "pk", Columns: binary("id")}}
		case mgLCControlSupersede, mgLCControlGeneration:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "lc_generations", Name: "sqlite_autoindex_lc_generations_1", Origin: "pk", Columns: binary("id")}}
		case mgLCControlReceiptRead:
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "lc_receipts", Name: "sqlite_autoindex_lc_receipts_1", Origin: "pk", Columns: binary("request_id")}}
		}
		args := make([]any, len(d.Params))
		for j, param := range d.Params {
			switch param.Type {
			case stmt.Text:
				args[j] = "missing"
			case stmt.Integer:
				args[j] = int64(1)
			case stmt.Blob:
				args[j] = make([]byte, 32)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return defs
}
