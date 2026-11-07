package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

// MPByteDefinitions contributes exact scalar preflights for MP's selected
// durable heads. A length row is not a permission/source proof or a payload.
// Consumers still pair it with the exact payload in one ReadForecasted window.
func MPByteDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, p := range []struct{ name, table, columns, id string }{
		{"queue", "queues_v1", "id,current_entry_id,session_id,repeat,owner_kind,owner_id,authority,account_id,profile_id", "mp-queue"},
		{"session", "playback_v1_sessions", "id,kind,role,state,version_id,queue_id,entry_id,media_session_id,end_reason,device_id,account_id,profile_id,authority,group_id,transfer_id,channel_id", "mp-session"},
		{"prepared", "prepared_media_jobs", "id,state,phase,error_code,version_id,asset_id,target_id,profile_id,source_revision,library_id", "mp-job"},
	} {
		columns := strings.Split(p.columns, ",")
		for n, c := range columns {
			columns[n] = "octet_length(" + c + ")"
		}
		index := "sqlite_autoindex_" + p.table + "_1"
		out = append(out, stmt.Definition{ID: readStatementID("foreground.mp." + p.name + "_octets"), SQL: "SELECT " + strings.Join(columns, ",") + " FROM " + p.table + " INDEXED BY " + index + " WHERE id=?", Owner: "store/read MP byte preflight", CallSites: []string{"store/read/mp_octets.go"}, Kind: stmt.Read,
			Params: []stmt.Param{{Name: "id", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{p.id}}, {Name: "empty", Bindings: []any{"missing"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: p.table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}},
		})
	}
	return out
}
