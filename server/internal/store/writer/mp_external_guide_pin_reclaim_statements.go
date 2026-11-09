package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Reclaim SQL is frozen F018 source, unchanged. Include the complete union in
// IA/MP's outer Capture/plan; registration grants no current source authority.
const mpGuidePinReclaimSQL = `DELETE FROM mp_external_guide_pins
WHERE media_session_id=? AND occurrence_generation=? AND execution_id=?
AND source_request_id=? AND released_ms>0
AND current_occurrence_generation IS NULL AND current_execution_id IS NULL
AND current_source_id IS NULL AND current_generation_id IS NULL
AND current_channel_id IS NULL AND current_programme_id IS NULL
RETURNING variable_bytes`

func mpGuidePinReclaimDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: mpGuidePinReclaim, Kind: stmt.Write, SQL: mpGuidePinReclaimSQL, Params: []stmt.Param{text("media"), integer("generation"), text("execution"), text("source_request")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_external_guide_pins"}}},
		{ID: mpGuidePinFirst, Kind: stmt.Read, SQL: `SELECT occurrence_generation,execution_id,source_request_id FROM mp_external_guide_pins WHERE media_session_id=? AND occurrence_generation>? ORDER BY occurrence_generation,execution_id LIMIT 1`, Params: []stmt.Param{text("media"), integer("generation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_external_guide_pins"}, Seeks: []string{"media_session_id=? AND occurrence_generation>?"}}},
		{ID: mpGuidePinTie, Kind: stmt.Read, SQL: `SELECT occurrence_generation,execution_id,source_request_id FROM mp_external_guide_pins WHERE media_session_id=? AND occurrence_generation=? AND execution_id>? ORDER BY execution_id LIMIT 1`, Params: []stmt.Param{text("media"), integer("generation"), text("after_execution")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_external_guide_pins"}, Seeks: []string{"media_session_id=? AND occurrence_generation=? AND execution_id>?"}}},
	}
	for i := range defs {
		defs[i].Owner = "store/writer MP guide"
		defs[i].CallSites = []string{"store/writer/mp_external_guide_pin_reclaim.go"}
		args := []any{}
		for _, p := range defs[i].Params {
			if p.Type == stmt.Text {
				args = append(args, "missing")
			} else {
				args = append(args, int64(1))
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "empty", Bindings: args}}
	}
	return defs
}
