package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	mgLCHead              stmt.ID = "writer.mg.lc.head"
	mgLCLibrary           stmt.ID = "writer.mg.lc.library-witness"
	mgLCHistory           stmt.ID = "writer.mg.lc.history-witness"
	mgLCRead              stmt.ID = "writer.mg.lc.generation"
	mgLCInsert            stmt.ID = "writer.mg.lc.begin"
	mgLCTail              stmt.ID = "writer.mg.lc.candidate-tail"
	mgLCCandidateInsert   stmt.ID = "writer.mg.lc.candidate-insert"
	mgLCCandidateRead     stmt.ID = "writer.mg.lc.candidate-lengths"
	mgLCEntrySource       stmt.ID = "writer.mg.lc.candidate-source"
	mgLCEntryInsert       stmt.ID = "writer.mg.lc.entry-insert"
	mgLCEntryLengthsFirst stmt.ID = "writer.mg.lc.entry-lengths-first"
	mgLCEntryLengths      stmt.ID = "writer.mg.lc.entry-lengths"
	mgLCEntryPage         stmt.ID = "writer.mg.lc.entry-page"
	mgLCAdvance           stmt.ID = "writer.mg.lc.advance"
	mgLCHeadPublish       stmt.ID = "writer.mg.lc.publish-head"
	mgLCBirthOwner        stmt.ID = "writer.mg.lc.birth-owner"
	mgLCReceiptOwner      stmt.ID = "writer.mg.lc.receipt-owner"
	mgLCReceiptInsert     stmt.ID = "writer.mg.lc.receipt-save"
	mgLCReceiptRead       stmt.ID = "writer.mg.lc.receipt-read"
	mgLCReceiptOperation  stmt.ID = "writer.mg.lc.receipt-operation"
	mgLCReceiptAny        stmt.ID = "writer.mg.lc.receipt-any"
	mgLCRetirement        stmt.ID = "writer.mg.lc.retirement"
	mgLCAckOwner          stmt.ID = "writer.mg.lc.ack"
)

// Closed compiled field lists; requests cannot supply SQL names or fragments.
const mgLCProgressColumns = `phase,scan_after,status,lease_token,reclaim_family,reclaim_key,cursor_ms,candidate_count,unresolved_count,last_command_sequence,acknowledged_command_sequence,stage_revision,staged_bytes,entry_count,verified_entry_count,verify_after_ms,verify_end_ms,lease_until_ms,published_ms,reclaim_subkey,entry_digest,verified_entry_digest,schedule_sealed`
const mgLCEntryColumns = `generation_id,occurrence_id,channel_id,start_ms,end_ms,item_id,asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason`

func mgLCProgressParams(prefix string) []stmt.Param {
	var out []stmt.Param
	for k, n := range strings.Split(mgLCProgressColumns, ",") {
		typ := stmt.Integer
		if k < 6 {
			typ = stmt.Text
		}
		if k == 20 || k == 21 {
			typ = stmt.Blob
		}
		out = append(out, stmt.Param{Name: prefix + n, Type: typ})
	}
	return out
}
func mgLCDefinitions() []stmt.Definition {
	t, i, r, x := stmt.Text, stmt.Integer, stmt.Real, stmt.Blob
	p := func(n string, typ stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: typ} }
	header := []stmt.Param{p("generation", t), p("channel", t), p("revision", i), p("config", t), p("catalogue", t), p("seed", t), p("base", t), p("start", i), p("end", i), p("lease", t), p("lease_until", i), p("history", i), p("incarnation", x), p("create", x), p("birth", x)}
	predicate := `id=? AND channel_id=? AND config_revision=? AND config_json=? AND catalog_fence=? AND seed=? AND base_generation=? AND start_ms=? AND end_ms=? AND lease_token=? AND lease_until_ms=? AND play_history_fence=? AND command_incarnation=? AND create_operation_id=? AND birth_request_digest=? AND status='pending'`
	progressFields := strings.Split(mgLCProgressColumns, ",")
	var assigns, equals []string
	for _, f := range progressFields {
		assigns = append(assigns, f+"=?")
		equals = append(equals, f+"=?")
	}
	entryParams := []stmt.Param{p("generation", t), p("occurrence", t), p("channel", t), p("start", i), p("end", i), p("item", i), p("asset", t), p("library", t), p("source", t), p("title", t), p("rule", t), p("block", t), p("offset", i), p("slate", t)}
	defs := []stmt.Definition{
		{ID: mgLCHead, Kind: stmt.Read, SQL: `SELECT revision,active_generation FROM lc_channels WHERE id=? AND revision=? AND config_json=? AND active_generation=? AND enabled=1 AND removed=0`, Params: []stmt.Param{p("channel", t), p("revision", i), p("config", t), p("base", t)}},
		{ID: mgLCLibrary, Kind: stmt.Read, SQL: `SELECT l.data_revision,l.data_order_revision,l.data_metadata_revision,l.playback_origin_revision,l.playback_origin_incarnation FROM libraries AS l CROSS JOIN catalog_libraries AS c ON c.library_id=l.id WHERE l.id=? AND c.id=? AND c.retired=0`, Params: []stmt.Param{p("library", t), p("catalogue_library", i)}},
		{ID: mgLCHistory, Kind: stmt.Read, SQL: `SELECT event_id FROM lc_play_events WHERE event_id>0 ORDER BY event_id DESC LIMIT 1`, Params: nil},
		{ID: mgLCRead, Kind: stmt.Read, SQL: `SELECT ` + mgLCProgressColumns + ` FROM lc_generations WHERE ` + predicate, Params: header},
		{ID: mgLCInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,base_generation,start_ms,end_ms,lease_token,lease_until_ms,play_history_fence,command_incarnation,create_operation_id,birth_request_digest,status,phase,boundary_ms,cursor_ms,created_ms,entry_digest,verify_end_ms,verified_entry_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'pending','mg-lc-candidates',?,?,?,?,?,?) RETURNING ` + mgLCProgressColumns, Params: append(append([]stmt.Param{}, header...), p("boundary", i), p("cursor", i), p("created", i), p("initial_digest", x), p("verify_end", i), p("verify_digest", x))},
		{ID: mgLCTail, Kind: stmt.Read, SQL: `SELECT ordinal FROM lc_candidates WHERE generation_id=? AND rule_id=? ORDER BY ordinal DESC LIMIT 1`, Params: []stmt.Param{p("generation", t), p("rule", t)}},
		{ID: mgLCCandidateInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_candidates(generation_id,rule_id,item_id,ordinal,library_id,asset_id,source_fence,duration_ms,title,show_id,season,episode,weight,sort_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING generation_id,rule_id,item_id,ordinal,library_id,asset_id,source_fence,duration_ms,title,show_id,season,episode,weight,sort_key`, Params: []stmt.Param{p("generation", t), p("rule", t), p("item", i), p("ordinal", i), p("library", t), p("asset", t), p("source", t), p("duration", i), p("title", t), p("show", i), p("season", i), p("episode", i), p("weight", r), p("sort", t)}},
		{ID: mgLCCandidateRead, Kind: stmt.Read, SQL: `SELECT duration_ms,octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title) FROM lc_candidates WHERE generation_id=? AND rule_id=? AND item_id=?`, Params: []stmt.Param{p("generation", t), p("rule", t), p("item", i)}},
		{ID: mgLCEntrySource, Kind: stmt.Read, SQL: `SELECT asset_id,library_id,source_fence,title FROM lc_candidates WHERE generation_id=? AND rule_id=? AND item_id=?`, Params: []stmt.Param{p("generation", t), p("rule", t), p("item", i)}},
		{ID: mgLCEntryInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_entries(` + mgLCEntryColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING ` + mgLCEntryColumns, Params: entryParams},
		{ID: mgLCEntryLengthsFirst, Kind: stmt.Read, SQL: `SELECT start_ms,113+octet_length(generation_id)+octet_length(occurrence_id)+octet_length(channel_id)+octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title)+octet_length(rule_id)+octet_length(block_id)+octet_length(slate_reason) FROM lc_entries INDEXED BY lc_schedule_window WHERE generation_id=? ORDER BY start_ms LIMIT 8`, Params: []stmt.Param{p("generation", t)}},
		{ID: mgLCEntryLengths, Kind: stmt.Read, SQL: `SELECT start_ms,113+octet_length(generation_id)+octet_length(occurrence_id)+octet_length(channel_id)+octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title)+octet_length(rule_id)+octet_length(block_id)+octet_length(slate_reason) FROM lc_entries INDEXED BY lc_schedule_window WHERE generation_id=? AND start_ms>? ORDER BY start_ms LIMIT 8`, Params: []stmt.Param{p("generation", t), p("after", i)}},
		{ID: mgLCEntryPage, Kind: stmt.Read, SQL: `SELECT ` + mgLCEntryColumns + ` FROM lc_entries INDEXED BY lc_schedule_window WHERE generation_id=? AND start_ms>? AND start_ms<=? ORDER BY start_ms LIMIT ?`, Params: []stmt.Param{p("generation", t), p("after", i), p("through", i), p("limit", i)}},
		{ID: mgLCAdvance, Kind: stmt.Write, SQL: `UPDATE lc_generations SET ` + strings.Join(assigns, ",") + ` WHERE ` + predicate + ` AND ` + strings.Join(equals, " AND ") + ` RETURNING ` + mgLCProgressColumns, Params: append(append(mgLCProgressParams("new_"), header...), mgLCProgressParams("old_")...)},
		{ID: mgLCHeadPublish, Kind: stmt.Write, SQL: `UPDATE lc_channels SET active_generation=?,generated_through_ms=?,state=?,health_code=? WHERE id=? AND revision=? AND config_json=? AND active_generation=? AND enabled=1 AND removed=0 RETURNING active_generation,generated_through_ms`, Params: []stmt.Param{p("generation", t), p("through", i), p("state", t), p("health", t), p("channel", t), p("revision", i), p("config", t), p("base", t)}},
	}
	defs = append(defs, mgLCReceiptDefinitions()...)
	for k := range defs {
		defs[k].Owner = "writer.mg.lc"
		defs[k].CallSites = []string{"writer/mg_lc_effects.go", "writer/mg_lc_receipts.go"}
		args := make([]any, len(defs[k].Params))
		for j, param := range defs[k].Params {
			switch param.Type {
			case stmt.Text:
				args[j] = "missing"
			case stmt.Integer:
				args[j] = int64(1)
			case stmt.Real:
				args[j] = float64(1)
			case stmt.Blob:
				args[j] = make([]byte, 32)
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
		case mgLCRead, mgLCAdvance, mgLCReceiptOwner, mgLCAckOwner, mgLCBirthOwner:
			table = "lc_generations"
		case mgLCTail:
			table = "lc_candidates"
			index = "sqlite_autoindex_lc_candidates_2"
			origin = "u"
			columns = []string{"generation_id", "rule_id", "ordinal"}
		case mgLCCandidateRead, mgLCEntrySource:
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
	return []any{b.generation, b.channel, b.revision, b.config, b.fence(), b.seed, b.base, b.start, b.end, b.lease, b.leaseUntil, b.history, b.incarnation[:], b.create[:], b.birth[:]}
}
func (p mgLCProgress) values() []any {
	sealed := int64(0)
	if p.sealed {
		sealed = 1
	}
	return []any{p.phase, p.after, p.status, p.lease, p.reclaimFamily, p.reclaimKey, p.cursor, p.candidates, p.unresolved, p.last, p.ack, p.revision, p.bytes, p.entries, p.verified, p.verifyAfter, p.verifyEnd, p.leaseUntil, p.published, p.reclaimSubkey, p.entryDigest[:], p.verifyDigest[:], sealed}
}
