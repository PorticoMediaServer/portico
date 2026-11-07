package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoPersonalFlagsSubject        stmt.ID = "uo.personal.flags.subject"
	uoPersonalFlagsRead           stmt.ID = "uo.personal.flags.read"
	uoPersonalFlagsInsert         stmt.ID = "uo.personal.flags.insert"
	uoPersonalFlagsUpdate         stmt.ID = "uo.personal.flags.update"
	uoPersonalFlagsRevisionRead   stmt.ID = "uo.personal.flags.revision_read"
	uoPersonalFlagsRevisionInsert stmt.ID = "uo.personal.flags.revision_insert"
	uoPersonalFlagsRevisionUpdate stmt.ID = "uo.personal.flags.revision_update"
)

// Inert finite domain statements, not a registered production command.
func uoPersonalFlagsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	viewerKey := stmt.AutoIndexBinding{Table: "viewer_revisions", Name: "sqlite_autoindex_viewer_revisions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}, {Name: "library_id", Collation: "BINARY"}}}
	viewerPlan := stmt.Plan{Hot: true, Indexes: []string{viewerKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{viewerKey}}
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO personal", Kind: kind, SQL: sql, Params: params, CallSites: []string{"store/writer/uo_personal_flags.go:applyUOPersonalFlags"}, Plan: p}
	}
	definitions := []stmt.Definition{
		def(uoPersonalFlagsSubject, stmt.Read, `SELECT e.id FROM catalog_entities AS e
CROSS JOIN catalog_libraries AS l ON l.id=e.library_id
WHERE e.id=? AND e.library_id=? AND e.public_id=? AND e.revision=? AND e.admission_revision=? AND l.library_id=? AND e.retired=0 AND l.retired=0`,
			[]stmt.Param{i("subject"), i("library"), b("public"), i("entity_revision"), i("admission_revision"), s("library_key")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "l"}}),
		def(uoPersonalFlagsRead, stmt.Read, `SELECT revision,watchlisted,favorite,not_interested,rating FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=? AND item_public_id=?`,
			[]stmt.Param{s("profile"), i("subject"), b("public")}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}}),
		def(uoPersonalFlagsInsert, stmt.Write, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,watchlisted,favorite,not_interested,rating) VALUES(?,?,?,?,?,?,?,?) RETURNING revision`,
			[]stmt.Param{s("profile"), b("public"), i("subject"), i("library"), i("watchlisted"), i("favorite"), i("not_interested"), ni("rating")}, stmt.Plan{Hot: true}),
		def(uoPersonalFlagsUpdate, stmt.Write, `UPDATE personal_items INDEXED BY personal_current_live SET revision=?,watchlisted=?,favorite=?,not_interested=?,rating=? WHERE profile_id=? AND item_id=? AND item_public_id=? AND revision=? RETURNING revision`,
			[]stmt.Param{i("next_revision"), i("watchlisted"), i("favorite"), i("not_interested"), ni("rating"), s("profile"), i("subject"), b("public"), i("old_revision")}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}}),
		def(uoPersonalFlagsRevisionRead, stmt.Read, `SELECT revision,selection_revision FROM viewer_revisions INDEXED BY sqlite_autoindex_viewer_revisions_1 WHERE profile_id=? AND library_id=?`,
			[]stmt.Param{s("profile"), s("library")}, viewerPlan),
		def(uoPersonalFlagsRevisionInsert, stmt.Write, `INSERT INTO viewer_revisions(profile_id,library_id,revision,selection_revision) VALUES(?,?,?,?) RETURNING revision,selection_revision`,
			[]stmt.Param{s("profile"), s("library"), i("initial_revision"), i("initial_selection")}, stmt.Plan{Hot: true}),
		def(uoPersonalFlagsRevisionUpdate, stmt.Write, `UPDATE viewer_revisions INDEXED BY sqlite_autoindex_viewer_revisions_1 SET revision=?,selection_revision=? WHERE profile_id=? AND library_id=? AND revision=? AND selection_revision=? RETURNING revision,selection_revision`,
			[]stmt.Param{i("next_revision"), i("next_selection"), s("profile"), s("library"), i("old_revision"), i("old_selection")}, viewerPlan),
	}
	public := []byte{3, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	bindings := [][]any{
		{int64(2111), int64(7), public, int64(1), int64(1), "library-one"},
		{"profile-one", int64(2111), public},
		{"profile-two", public, int64(2111), int64(7), int64(1), int64(0), int64(0), nil},
		{int64(2), int64(1), int64(1), int64(0), int64(0), "profile-one", int64(2111), public, int64(1)},
		{"profile-one", "library-one"},
		{"profile-one", "library-one", int64(1), int64(1)},
		{int64(2), int64(2), "profile-one", "library-one", int64(1), int64(1)},
	}
	for n := range definitions {
		definitions[n].Cohorts = []stmt.Cohort{{Name: "personal_flags_point", Bindings: bindings[n]}}
	}
	return definitions
}
