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
	base := `SELECT id,COALESCE(disc_number,1) AS d,COALESCE(part_number,0) AS p,library_id,kind,retired FROM catalog_entities INDEXED BY catalog_entities_book WHERE book_id IS NOT NULL AND book_id=?`
	for _, v := range []struct {
		name, tail string
		params     []stmt.Param
		bindings   []any
		seeks      []string
	}{
		{"first", ` ORDER BY d,p,id LIMIT 1`, []stmt.Param{i("book")}, []any{int64(2002)}, nil},
		{"tail", ` AND COALESCE(disc_number,1)=? AND COALESCE(part_number,0)=? AND id>? ORDER BY id LIMIT 1`, []stmt.Param{i("book"), i("disc"), i("part"), i("after")}, []any{int64(2002), int64(1), int64(0), int64(0)}, []string{"id>?"}},
		{"part", ` AND COALESCE(disc_number,1)=? AND COALESCE(part_number,0)>? ORDER BY p,id LIMIT 1`, []stmt.Param{i("book"), i("disc"), i("part")}, []any{int64(2002), int64(1), int64(0)}, []string{"<expr>>?"}},
		{"disc", ` AND COALESCE(disc_number,1)>? ORDER BY d,p,id LIMIT 1`, []stmt.Param{i("book"), i("disc")}, []any{int64(2002), int64(1)}, []string{"<expr>>?"}},
	} {
		empty := append([]any(nil), v.bindings...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: readStatementID("rs.author.files." + v.name), SQL: base + v.tail, Params: v.params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_author_books_reader.go:enumerate"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: v.bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_book"}, Seeks: v.seeks}})
	}
	return out
}
