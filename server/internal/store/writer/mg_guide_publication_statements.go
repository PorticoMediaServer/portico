package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The MG guide seal (804) and publication (805) effects are point operations on
// the operation PK, the source PK and one generation FK. They never re-count the
// staged guide, never fan out to rules or recordings and never sweep unreferenced
// generations. Totals, digests and the seal are the exact counters the bounded
// staging commands already accepted; publication repeats them in its CAS instead
// of trusting caller-supplied success facts.
const (
	mgGuideSealRow          stmt.ID = "mg.guide.seal"
	mgGuideSourceRow        stmt.ID = "mg.guide.published_source"
	mgGuidePublishSource    stmt.ID = "mg.guide.publish_source"
	mgGuidePublishNewSource stmt.ID = "mg.guide.publish_new_source"
	mgGuidePublishOperation stmt.ID = "mg.guide.publish_operation"
)

func mgGuidePublicationDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: mgGuideSealRow, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET sealed=1,last_command_sequence=?,stage_revision=? WHERE request_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND status='staging' AND sealed=0 AND staged_channel_count=? AND staged_programme_count=? AND staged_choice_count=? AND staged_bytes=? AND manifest_digest=? AND stage_digest=?`, Params: []stmt.Param{i("next_sequence"), i("next_revision"), s("request"), b("incarnation"), i("sequence"), i("acknowledged"), i("revision"), i("channels"), i("programmes"), i("choices"), i("bytes"), b("manifest"), b("chain")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete", Bindings: []any{int64(2), int64(2), "guide-request", make([]byte, 16), int64(1), int64(1), int64(1), int64(1), int64(1), int64(1), int64(100), make([]byte, 32), make([]byte, 32)}}}},
		{ID: mgGuideSourceRow, Kind: stmt.Read, SQL: `SELECT name,revision,state,tuner_count,active_generation,published_at FROM live_sources INDEXED BY sqlite_autoindex_live_sources_1 WHERE id=?`, Params: []stmt.Param{s("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "published", Bindings: []any{"guide-source"}}}},
		{ID: mgGuidePublishSource, Kind: stmt.Write, SQL: `UPDATE live_sources INDEXED BY sqlite_autoindex_live_sources_1 SET name=?,revision=?,tuner_count=?,active_generation=?,published_at=? WHERE id=? AND revision=? AND active_generation=? AND state=?`, Params: []stmt.Param{s("name"), i("revision"), i("tuners"), s("generation"), s("published"), s("source"), i("expected_revision"), s("expected_generation"), s("expected_state")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "refresh", Bindings: []any{"Guide", int64(7), int64(1), "guide-new", "2026-11-07T00:00:00.000Z", "guide-source", int64(7), "guide-old", "active"}}}},
		{ID: mgGuidePublishNewSource, Kind: stmt.Write, SQL: `INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,?,?,?,?,?,?) RETURNING id,name,revision,state,tuner_count,active_generation,published_at`, Params: []stmt.Param{s("source"), s("name"), i("revision"), s("state"), i("tuners"), s("generation"), s("published")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "first", Bindings: []any{"new-guide-source", "Guide", int64(1), "active", int64(1), "guide-new", "2026-11-07T00:00:00.000Z"}}}},
		{ID: mgGuidePublishOperation, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET status='published',last_command_sequence=?,stage_revision=?,result_revision=?,result_state=?,published_at=? WHERE request_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND status='staging' AND sealed=1 AND source_id=? AND generation_id=? AND expected_revision=? AND expected_generation=?`, Params: []stmt.Param{i("next_sequence"), i("next_revision"), i("result_revision"), s("result_state"), s("published"), s("request"), b("incarnation"), i("sequence"), i("acknowledged"), i("stage_revision"), s("source"), s("generation"), i("expected_revision"), s("expected_generation")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}}, Cohorts: []stmt.Cohort{{Name: "head_flip", Bindings: []any{int64(3), int64(3), int64(7), "active", "2026-11-07T00:00:00.000Z", "guide-request", make([]byte, 16), int64(2), int64(2), int64(2), "guide-source", "guide-new", int64(7), "guide-old"}}}},
	}
	pins := map[string]stmt.AutoIndexBinding{
		"sqlite_autoindex_live_operations_1": {Table: "live_operations", Name: "sqlite_autoindex_live_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}}},
		"sqlite_autoindex_live_sources_1":    {Table: "live_sources", Name: "sqlite_autoindex_live_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
	}
	for j := range defs {
		d := &defs[j]
		d.Owner = "writer.mg.guide.publication"
		d.CallSites = []string{"writer/mg_guide_publication.go"}
		for _, index := range d.Plan.Indexes {
			if pin, ok := pins[index]; ok {
				d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, pin)
			}
		}
	}
	return defs
}
