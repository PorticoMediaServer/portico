package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	mgLCControlRead       stmt.ID = "mg.lc.control.read"
	mgLCControlKeys       stmt.ID = "mg.lc.control.keys"
	mgLCControlInsert     stmt.ID = "mg.lc.control.insert"
	mgLCControlUpdate     stmt.ID = "mg.lc.control.update"
	mgLCControlPending    stmt.ID = "mg.lc.control.pending"
	mgLCControlInvalidate stmt.ID = "mg.lc.control.invalidate"
	mgLCControlGeneration stmt.ID = "mg.lc.control.generation"
	mgLCControlRefs       stmt.ID = "mg.lc.control.refs"
)
const mgLCControlColumns = `id,revision,config_json,enabled,position,name,active_generation,state,health_code,generated_through_ms,removed`
const mgLCControlGenerationColumns = `id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,copy_after_ms,scan_rule,scan_after,cursor_ms,candidate_count,unresolved_count,lease_token,lease_until_ms,created_ms,published_ms,error_code,play_history_fence`

func mgLCControlDefinitions() []stmt.Definition {
	text := stmt.Text
	integer := stmt.Integer
	makeDef := func(id stmt.ID, kind stmt.Kind, q string, types ...stmt.Type) stmt.Definition {
		d := stmt.Definition{ID: id, Kind: kind, SQL: q, Owner: "writer.mg.lc.control", CallSites: []string{"writer/mg_lc_control.go"}}
		args := []any{}
		for j, typ := range types {
			d.Params = append(d.Params, stmt.Param{Name: strings.Repeat("p", j+1), Type: typ})
			if typ == text {
				args = append(args, "missing")
			} else {
				args = append(args, int64(1))
			}
		}
		if id == mgLCControlUpdate {
			d.Params[len(d.Params)-1].Nullable = true
		}
		table, index := "lc_channels", "sqlite_autoindex_lc_channels_1"
		switch id {
		case mgLCControlKeys:
			index = "lc_directory"
		case mgLCControlPending, mgLCControlInvalidate:
			table, index = "lc_generations", "lc_one_job"
		case mgLCControlGeneration:
			table, index = "lc_generations", "sqlite_autoindex_lc_generations_1"
		case mgLCControlRefs:
			table, index = "lc_playback_refs", "lc_ref_channel"
		}
		d.Plan = stmt.Plan{Hot: true, Indexes: []string{index}}
		if strings.HasPrefix(index, "sqlite_autoindex_") {
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}
		}
		if id == mgLCControlInsert {
			d.Plan = stmt.Plan{}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-key", Bindings: args}}
		return d
	}
	return []stmt.Definition{
		makeDef(mgLCControlRead, stmt.Read, `SELECT `+mgLCControlColumns+` FROM lc_channels INDEXED BY sqlite_autoindex_lc_channels_1 WHERE id=?`, text),
		makeDef(mgLCControlKeys, stmt.Read, `SELECT 1 FROM lc_channels INDEXED BY lc_directory WHERE enabled=? AND removed=0 ORDER BY position,id LIMIT 64`, integer),
		makeDef(mgLCControlInsert, stmt.Write, `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES(?,?,?,?,?,?,?)`, text, integer, text, integer, integer, text, text),
		makeDef(mgLCControlUpdate, stmt.Write, `UPDATE lc_channels INDEXED BY sqlite_autoindex_lc_channels_1 SET revision=?,config_json=?,enabled=?,position=?,name=?,state=?,health_code=?,removed=? WHERE id=? AND revision=? AND active_generation=? AND removed=0`, integer, text, integer, integer, text, text, text, integer, text, integer, text),
		makeDef(mgLCControlPending, stmt.Read, `SELECT `+mgLCControlGenerationColumns+` FROM lc_generations INDEXED BY lc_one_job WHERE channel_id=? AND status='pending'`, text),
		makeDef(mgLCControlInvalidate, stmt.Write, `UPDATE lc_generations INDEXED BY lc_one_job SET status='superseded',error_code='configuration-changed' WHERE id=? AND channel_id=? AND config_revision=? AND status='pending'`, text, text, integer),
		makeDef(mgLCControlGeneration, stmt.Read, `SELECT `+mgLCControlGenerationColumns+` FROM lc_generations INDEXED BY sqlite_autoindex_lc_generations_1 WHERE id=?`, text),
		makeDef(mgLCControlRefs, stmt.Read, `SELECT playback_id FROM lc_playback_refs INDEXED BY lc_ref_channel WHERE channel_id=? LIMIT 1`, text),
	}
}
