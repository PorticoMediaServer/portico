package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mgGuideRead             stmt.ID = "mg.guide.operation"
	mgGuideSource           stmt.ID = "mg.guide.source"
	mgGuideIdentity         stmt.ID = "mg.guide.identity"
	mgGuideSealRow          stmt.ID = "mg.guide.seal"
	mgGuidePublishSource    stmt.ID = "mg.guide.publish_source"
	mgGuidePublishNewSource stmt.ID = "mg.guide.publish_new_source"
	mgGuidePublishOperation stmt.ID = "mg.guide.publish_operation"
)

func mgGuidePublicationDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: mgGuideRead, Kind: stmt.Read, SQL: `SELECT o.request_id,o.input_hash,o.source_id,o.generation_id,o.expected_revision,o.expected_generation,o.expected_choice_count,o.command_incarnation,o.create_operation_id,o.birth_request_digest,o.last_command_sequence,o.acknowledged_command_sequence,o.stage_revision,o.staged_channel_count,o.staged_programme_count,o.staged_choice_count,o.staged_bytes,o.manifest_digest,o.stage_digest,o.sealed,o.status,o.result_revision,o.result_state,o.published_at,g.name,g.tuner_count,g.channel_count,g.programme_count,g.created_at
FROM live_operations AS o INDEXED BY sqlite_autoindex_live_operations_1
CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=o.generation_id AND g.source_id=o.source_id
WHERE o.request_id=?`, Params: []stmt.Param{s("request")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1", "sqlite_autoindex_live_generations_1"}, Seeks: []string{"request_id=?", "id=?"}}, Cohorts: []stmt.Cohort{{Name: "staged", Bindings: []any{"guide-request"}}}},
		{ID: mgGuideSource, Kind: stmt.Read, SQL: `SELECT revision,state,active_generation FROM live_sources INDEXED BY sqlite_autoindex_live_sources_1 WHERE id=?`, Params: []stmt.Param{s("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"guide-source"}}, {Name: "new", Bindings: []any{"new-guide-source"}}}},
		{ID: mgGuideIdentity, Kind: stmt.Read, SQL: `SELECT has_live_source_removals FROM live_source_identities INDEXED BY sqlite_autoindex_live_source_identities_1 WHERE id=?`, Params: []stmt.Param{s("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_identities_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "retained", Bindings: []any{"guide-source"}}}},
		{ID: mgGuideSealRow, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET sealed=1,last_command_sequence=?,stage_revision=? WHERE request_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND status='staging' AND sealed=0 AND staged_channel_count=? AND staged_programme_count=? AND staged_choice_count=? AND staged_bytes=? AND manifest_digest=? AND stage_digest=?`, Params: []stmt.Param{i("next_sequence"), i("next_revision"), s("request"), b("incarnation"), i("sequence"), i("acknowledged"), i("revision"), i("channels"), i("programmes"), i("choices"), i("bytes"), b("manifest"), b("chain")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete", Bindings: []any{int64(2), int64(2), "guide-request", make([]byte, 16), int64(1), int64(1), int64(1), int64(1), int64(0), int64(1), int64(100), make([]byte, 32), make([]byte, 32)}}}},
		{ID: mgGuidePublishSource, Kind: stmt.Write, SQL: `UPDATE live_sources INDEXED BY sqlite_autoindex_live_sources_1 SET name=?,revision=?,tuner_count=?,active_generation=?,published_at=? WHERE id=? AND revision=? AND active_generation=? AND state=?`, Params: []stmt.Param{s("name"), i("revision"), i("tuners"), s("generation"), s("published"), s("source"), i("expected_revision"), s("expected_generation"), s("expected_state")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "refresh", Bindings: []any{"Guide", int64(7), int64(1), "guide-new", "2026-11-07T00:00:00Z", "guide-source", int64(7), "guide-old", "active"}}}},
		{ID: mgGuidePublishNewSource, Kind: stmt.Write, SQL: `INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,?,?,?,?,?,?)`, Params: []stmt.Param{s("source"), s("name"), i("revision"), s("state"), i("tuners"), s("generation"), s("published")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "first", Bindings: []any{"new-guide-source", "Guide", int64(1), "active", int64(1), "guide-new", "2026-11-07T00:00:00Z"}}}},
		{ID: mgGuidePublishOperation, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET status='published',last_command_sequence=?,stage_revision=?,result_revision=?,result_state=?,published_at=? WHERE request_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND status='staging' AND sealed=1 AND source_id=? AND generation_id=? AND expected_revision=? AND expected_generation=?`, Params: []stmt.Param{i("next_sequence"), i("next_revision"), i("result_revision"), s("result_state"), s("published"), s("request"), b("incarnation"), i("sequence"), i("acknowledged"), i("revision"), s("source"), s("generation"), i("expected_revision"), s("expected_generation")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}}, Cohorts: []stmt.Cohort{{Name: "head_flip", Bindings: []any{int64(3), int64(3), int64(7), "active", "2026-11-07T00:00:00Z", "guide-request", make([]byte, 16), int64(2), int64(2), int64(2), "guide-source", "guide-new", int64(7), "guide-old"}}}},
	}
	pins := map[string]stmt.AutoIndexBinding{}
	for _, pair := range [][2]string{{"live_operations", "request_id"}, {"live_generations", "id"}, {"live_sources", "id"}, {"live_source_identities", "id"}} {
		name := "sqlite_autoindex_" + pair[0] + "_1"
		pins[name] = stmt.AutoIndexBinding{Table: pair[0], Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: pair[1], Collation: "BINARY"}}}
	}
	for j := range defs {
		for _, index := range defs[j].Plan.Indexes {
			if pin, ok := pins[index]; ok {
				defs[j].Plan.AutoIndexes = append(defs[j].Plan.AutoIndexes, pin)
			}
		}
	}

	for j := range defs {
		defs[j].Owner = "store/writer MG guide publication effects"
		defs[j].CallSites = []string{"store/writer/mg_guide_publication_effects.go"}
	}
	return defs
}
