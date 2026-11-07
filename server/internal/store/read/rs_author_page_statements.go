package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsAuthorBookSQL = `SELECT e.author,e.title,e.series_position FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=8 AND e.retired=0`
const rsAuthorBookOctetsSQL = `SELECT octet_length(e.author),octet_length(e.title),octet_length(e.series_position) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=8 AND e.retired=0`

func init() {
	registerRSBytePoint("rs.author.book", rsBytePoint{lengths: "rs.author.book_octets", columns: []int{0, 1, 2}})
}
func RSAuthorBookDefinitions() []stmt.Definition {
	out := rsAuthorBooksDefinitions()
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	for _, v := range []struct{ name, sql string }{{"book", rsAuthorBookSQL}, {"book_octets", rsAuthorBookOctetsSQL}} {
		out = append(out, stmt.Definition{ID: readStatementID("rs.author." + v.name), SQL: v.sql, Params: []stmt.Param{i("book"), i("library")}, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_author_books_reader.go:enumerate"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(2002), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}})
	}
	return append(out, stmt.Definition{ID: readStatementID("rs.author.files"), SQL: `SELECT id,book_id,retired FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=? AND kind=9 AND id>? ORDER BY id LIMIT 1`, Params: []stmt.Param{i("library"), i("after")}, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_author_books_reader.go:enumerate"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(0)}}, {Name: "empty", Bindings: []any{int64(999999), int64(0)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, Seeks: []string{"id>?"}}})
}
