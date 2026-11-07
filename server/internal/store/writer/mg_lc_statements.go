package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgLCHead              stmt.ID = "writer.mg.lc.head"
	mgLCLibrary           stmt.ID = "writer.mg.lc.library-witness"
	mgLCHistory           stmt.ID = "writer.mg.lc.history-witness"
	mgLCRead              stmt.ID = "writer.mg.lc.generation"
	mgLCInsert            stmt.ID = "writer.mg.lc.begin"
	mgLCTail              stmt.ID = "writer.mg.lc.candidate-tail"
	mgLCCandidateInsert   stmt.ID = "writer.mg.lc.candidate-insert"
	mgLCCandidateRead     stmt.ID = "writer.mg.lc.candidate"
	mgLCEntryInsert       stmt.ID = "writer.mg.lc.entry-insert"
	mgLCAdvance           stmt.ID = "writer.mg.lc.advance"
	mgLCHeadPublish       stmt.ID = "writer.mg.lc.publish-head"
	mgLCGenerationPublish stmt.ID = "writer.mg.lc.publish-generation"
)

func mgLCDefinitions() []stmt.Definition {
	t := stmt.Text
	i := stmt.Integer
	r := stmt.Real
	p := func(name string, typ stmt.Type) stmt.Param { return stmt.Param{Name: name, Type: typ} }
	header := []stmt.Param{p("generation", t), p("channel", t), p("revision", i), p("config", t), p("catalogue", t), p("seed", t), p("base", t), p("start", i), p("end", i), p("lease", t), p("lease_until", i), p("history", i)}
	predicate := `id=? AND channel_id=? AND config_revision=? AND config_json=? AND catalog_fence=? AND seed=? AND base_generation=? AND start_ms=? AND end_ms=? AND lease_token=? AND lease_until_ms=? AND play_history_fence=? AND status='pending'`
	defs := []stmt.Definition{
		{ID: mgLCHead, Kind: stmt.Read, SQL: `SELECT revision,active_generation FROM lc_channels WHERE id=? AND revision=? AND config_json=? AND active_generation=? AND enabled=1 AND removed=0`, Params: []stmt.Param{p("channel", t), p("revision", i), p("config", t), p("base", t)}},
		{ID: mgLCLibrary, Kind: stmt.Read, SQL: `SELECT l.data_revision,l.data_order_revision,l.data_metadata_revision,l.playback_origin_revision,l.playback_origin_incarnation FROM libraries AS l CROSS JOIN catalog_libraries AS c ON c.library_id=l.id WHERE l.id=? AND c.id=? AND c.retired=0`, Params: []stmt.Param{p("library", t), p("catalogue_library", i)}},
		{ID: mgLCHistory, Kind: stmt.Read, SQL: `SELECT event_id FROM lc_play_events WHERE event_id>0 ORDER BY event_id DESC LIMIT 1`},
		{ID: mgLCRead, Kind: stmt.Read, SQL: `SELECT phase,cursor_ms,candidate_count,unresolved_count,scan_after FROM lc_generations WHERE ` + predicate, Params: header},
		{ID: mgLCInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,base_generation,start_ms,end_ms,lease_token,lease_until_ms,play_history_fence,status,phase,boundary_ms,cursor_ms,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'pending','mg-lc-candidates',?,?,?)`, Params: append(append([]stmt.Param{}, header...), p("boundary", i), p("cursor", i), p("created", i))},
		{ID: mgLCTail, Kind: stmt.Read, SQL: `SELECT ordinal FROM lc_candidates WHERE generation_id=? AND rule_id=? ORDER BY ordinal DESC LIMIT 1`, Params: []stmt.Param{p("generation", t), p("rule", t)}},
		{ID: mgLCCandidateInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_candidates(generation_id,rule_id,item_id,ordinal,library_id,asset_id,source_fence,duration_ms,title,show_id,season,episode,weight,sort_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{p("generation", t), p("rule", t), p("item", i), p("ordinal", i), p("library", t), p("asset", t), p("source", t), p("duration", i), p("title", t), p("show", i), p("season", i), p("episode", i), p("weight", r), p("sort", t)}},
		// Point lookup decodes only the duration. INSERT SELECT below copies the
		// immutable staged media fields without decoding a wide row in Go.
		{ID: mgLCCandidateRead, Kind: stmt.Read, SQL: `SELECT duration_ms FROM lc_candidates WHERE generation_id=? AND rule_id=? AND item_id=?`, Params: []stmt.Param{p("generation", t), p("rule", t), p("item", i)}},
		{ID: mgLCEntryInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_entries(generation_id,occurrence_id,channel_id,start_ms,end_ms,item_id,asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason) SELECT ?,?,?,?, ?,item_id,asset_id,library_id,source_fence,title,rule_id,?,0,'' FROM lc_candidates WHERE generation_id=? AND rule_id=? AND item_id=? UNION ALL SELECT ?,?,?,?, ?,0,'','','','Unavailable program',?,?,0,'no-eligible-media' WHERE ?=0`, Params: []stmt.Param{p("generation", t), p("occurrence", t), p("channel", t), p("start", i), p("end", i), p("block", t), p("candidate_generation", t), p("rule", t), p("item", i), p("slate_generation", t), p("slate_occurrence", t), p("slate_channel", t), p("slate_start", i), p("slate_end", i), p("slate_rule", t), p("slate_block", t), p("slate_item", i)}},
		{ID: mgLCAdvance, Kind: stmt.Write, SQL: `UPDATE lc_generations SET phase=?,cursor_ms=?,candidate_count=?,unresolved_count=?,scan_after=? WHERE ` + predicate + ` AND phase=? AND cursor_ms=? AND candidate_count=? AND unresolved_count=? AND scan_after=? RETURNING phase,cursor_ms,candidate_count,unresolved_count,scan_after`, Params: append(append([]stmt.Param{p("phase", t), p("cursor", i), p("candidates", i), p("unresolved", i), p("after", t)}, header...), p("old_phase", t), p("old_cursor", i), p("old_candidates", i), p("old_unresolved", i), p("old_after", t))},
		{ID: mgLCHeadPublish, Kind: stmt.Write, SQL: `UPDATE lc_channels SET active_generation=?,generated_through_ms=?,state=?,health_code=? WHERE id=? AND revision=? AND config_json=? AND active_generation=? AND enabled=1 AND removed=0 RETURNING active_generation,generated_through_ms`, Params: []stmt.Param{p("generation", t), p("through", i), p("state", t), p("health", t), p("channel", t), p("revision", i), p("config", t), p("base", t)}},
		{ID: mgLCGenerationPublish, Kind: stmt.Write, SQL: `UPDATE lc_generations SET status='published',phase='published',published_ms=?,lease_token='',lease_until_ms=0 WHERE ` + predicate + ` AND phase='mg-lc-validating' AND cursor_ms=end_ms AND candidate_count=? AND unresolved_count=? RETURNING cursor_ms,candidate_count,unresolved_count,published_ms`, Params: append(append([]stmt.Param{p("published", i)}, header...), p("candidates", i), p("unresolved", i))},
	}
	for k := range defs {
		defs[k].Owner = "writer.mg.lc"
		defs[k].CallSites = []string{"writer/mg_lc_effects.go"}
		// Missing-key plan cohort; behavior fixtures exercise successful bindings.
		// Six reads have native Capture goldens. Full DML command Capture and
		// authority/receipt recipe registration remain parent integration gates.
		args := make([]any, len(defs[k].Params))
		for j, param := range defs[k].Params {
			switch param.Type {
			case stmt.Text:
				args[j] = "missing"
			case stmt.Integer:
				args[j] = int64(1)
			case stmt.Real:
				args[j] = float64(1)
			}
		}
		defs[k].Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return mgLCPlanHints(defs)
}

func mgLCPlanHints(defs []stmt.Definition) []stmt.Definition {
	for k := range defs {
		d := &defs[k]
		table, index, origin := "", "", "pk"
		columns := []string{"id"}
		switch d.ID {
		case mgLCHistory:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"lc_play_events"}, Seeks: []string{"rowid>?"}}
			continue
		case mgLCHead, mgLCHeadPublish:
			table = "lc_channels"
		case mgLCLibrary:
			table = "libraries"
		case mgLCRead, mgLCAdvance, mgLCGenerationPublish:
			table = "lc_generations"
		case mgLCTail:
			table = "lc_candidates"
			index = "sqlite_autoindex_lc_candidates_2"
			origin = "u"
			columns = []string{"generation_id", "rule_id", "ordinal"}
		case mgLCCandidateRead:
			table = "lc_candidates"
			columns = []string{"generation_id", "rule_id", "item_id"}
		default:
			continue
		}
		if index == "" {
			index = "sqlite_autoindex_" + table + "_1"
		}
		pin := stmt.AutoIndexBinding{Table: table, Name: index, Origin: origin}
		for _, c := range columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		d.Plan = stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{pin}}
		if d.ID == mgLCLibrary {
			d.Plan.PrimaryKeys = []string{"c"}
		}
		needle := "FROM " + table
		if d.ID == mgLCLibrary {
			needle += " AS l"
		}
		if strings.HasPrefix(d.SQL, "UPDATE ") {
			needle = "UPDATE " + table
		}
		d.SQL = strings.Replace(d.SQL, needle, needle+" INDEXED BY "+index, 1)
	}
	return defs
}

func (b mgLCBuild) values() []any {
	return []any{b.generation, b.channel, b.revision, b.config, b.fence(), b.seed, b.base, b.start, b.end, b.lease, b.leaseUntil, b.history}
}
