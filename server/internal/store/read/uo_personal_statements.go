package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// UOPersonalDefinitions returns inert UO definitions for explicit harness
// composition alongside HarnessDefinitions. Capture them on the actual reader
// before registration. The product does not select this layer before cutover.
func UOPersonalDefinitions() []stmt.Definition {
	key := func(table, name, column string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
	}
	token := key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	return []stmt.Definition{{
		ID: readStatementID("foreground.uo.personal.point"), Owner: "store/read UO personal",
		CallSites: []string{"store/read/uo_personal.go:ForegroundReader.PersonalState"}, Kind: stmt.Read,
		// Only fixed-size public IDs and native scalar state are copied. The
		// common item shape uses empty title/sort placeholders: no title/path or
		// other variable-length subject data is fetched by this point recipe.
		SQL: `SELECT e.id,'',e.public_id,'',e.retired,e.revision,e.admission_own_age,
p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed,p.not_interested,p.rating,p.item_public_id
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN catalog_entities AS e ON e.id=?
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=f.profile_id AND p.item_id=e.id
WHERE t.token_hash=? AND e.library_id=? AND e.kind=?`,
		Params: []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "token", Type: stmt.Text}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{
			{Name: "present", Bindings: []any{int64(1), "token", int64(1), int64(1)}},
			{Name: "missing", Bindings: []any{int64(999999), "token", int64(1), int64(1)}},
		},
		Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{token.Name, family.Name, "personal_current_live"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "id=?", "profile_id=? AND item_id=?"}},
	}}
}
