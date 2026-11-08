package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBDirectoryDefinitions is an inert own inventory. Composition must obtain its
// actual Reader Capture before use; these definitions register no runtime.
func RBDirectoryDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbDirectoryDefinitions()...)
}

func rbDirectoryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name, origin string, cols ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return b
	}
	lib := key("libraries", "sqlite_autoindex_libraries_1", "pk", "id")
	viewer := key("viewer_revisions", "sqlite_autoindex_viewer_revisions_1", "pk", "profile_id", "library_id")
	proof := stmt.Plan{Hot: true, PrimaryKeys: []string{"cl"}, Indexes: []string{lib.Name, viewer.Name}, AutoIndexes: []stmt.AutoIndexBinding{lib, viewer}}
	// The integer mapping key is the only order; roots, kind and per-library
	// revisions travel with it so the principal share check and the shared fence
	// see one actual mapping, never a caller's selector or allowed list.
	const from = ` FROM catalog_libraries AS cl
 INNER JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=cl.library_id
 LEFT JOIN viewer_revisions AS v INDEXED BY sqlite_autoindex_viewer_revisions_1 ON v.library_id=l.id AND v.profile_id=?
 WHERE cl.retired=0 AND cl.id>?
 ORDER BY cl.id LIMIT ?`
	sql := `SELECT cl.id,cl.library_id,l.name,l.kind,l.position,cl.root,l.root,l.data_revision,l.data_order_revision,l.data_metadata_revision,COALESCE(v.revision,0),l.age_input_revision,l.age_published_revision,l.age_pending_operations,l.playback_origin_incarnation,l.playback_origin_revision` + from
	params := []stmt.Param{s("profile"), i("after"), i("limit")}
	return []stmt.Definition{{
		ID: statementID("rb.directory.scope"), Owner: "store/read", Kind: stmt.Read,
		CallSites: []string{"store/read/rb_directory.go:readRBDirectoryScope"},
		SQL:       sql, Params: params,
		Cohorts: []stmt.Cohort{
			{Name: "present", Bindings: []any{"p", int64(0), int64(rbDirectoryScopePage)}},
			{Name: "empty", Bindings: []any{"p", int64(1 << 62), int64(rbDirectoryScopePage)}},
		},
		Plan: proof,
	}}
}
