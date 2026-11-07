package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Normalization is deliberately performed by SQLite at the exact author13
// point. Go TrimSpace/ToLower, NOCASE and Reading20/21 are different contracts.
const rsAudioAuthorKeySQL = `SELECT lower(e.title) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=13 AND e.retired=0`
const rsAudioAuthorKeyOctetsSQL = `SELECT octet_length(lower(trim(e.title))) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=13 AND e.retired=0`

func init() {
	registerRSBytePoint("rs.audio.author_key", rsBytePoint{lengths: "rs.audio.author_key_octets", columns: []int{0}})
}
func rsAuthorBooksDefinitions() []stmt.Definition {
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	for _, pair := range []struct{ name, sql string }{{"author_key", rsAudioAuthorKeySQL}, {"author_key_octets", rsAudioAuthorKeyOctetsSQL}} {
		out = append(out, stmt.Definition{ID: readStatementID("rs.audio." + pair.name), SQL: pair.sql, Params: []stmt.Param{integer("author"), integer("library")}, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_author_books_statements.go:rsAuthorBooksDefinitions"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(13001), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}})
	}
	// A numeric SEARCH stream avoids claiming that the accepted Reading author
	// facet or the raw NOCASE author index implements trim/lower normalization.
	out = append(out, stmt.Definition{ID: readStatementID("rs.audio.books"), SQL: `SELECT id FROM catalog_entities INDEXED BY catalog_entities_library WHERE library_id=? AND kind=8 AND id>? ORDER BY id LIMIT 1`, Params: []stmt.Param{integer("library"), integer("after")}, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_author_books_statements.go:rsAuthorBooksDefinitions"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(0)}}, {Name: "empty", Bindings: []any{int64(999999), int64(0)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, Seeks: []string{"id>?"}}})
	return out
}
