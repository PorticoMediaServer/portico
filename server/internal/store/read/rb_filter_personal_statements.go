package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert definitions; the caller must Capture the actual reader before use.
func RBPersonalTitleFilterDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var defs []stmt.Definition
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, desc := range []bool{false, true} {
			direction, op, order := "asc.", ">", "ASC"
			if desc {
				direction, op, order = "desc.", "<", "DESC"
			}
			for _, part := range []string{"first", "tail", "next"} {
				where := " WHERE e.library_id=? AND e.kind=?"
				seek := "library_id=? AND kind=?"
				params := []stmt.Param{i("library"), i("kind")}
				bindings := []any{int64(1), int64(1)}
				if part == "tail" {
					where += " AND e." + key + " COLLATE NOCASE=? AND e.id" + op + "?"
					seek = key + "=? AND id" + op + "?"
					params = append(params, s("key"), i("after"))
					bindings = append(bindings, "same", int64(3))
				}
				if part == "next" {
					where += " AND e." + key + " COLLATE NOCASE" + op + "?"
					seek = key + op + "?"
					params = append(params, s("key"))
					bindings = append(bindings, "same")
				}
				params = append(params, i("limit"))
				bindings = append(bindings, int64(MaxRaw))
				end := " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
				from := " FROM catalog_entities AS e INDEXED BY " + index
				for _, lengths := range []bool{false, true} {
					name := "rb.filter.personal.title." + variant + direction + part
					columns := "e.id,e." + key + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age,p.revision,p.item_public_id,p.library_id,p.favorite,p.watchlisted,p.rating"
					p, b := append([]stmt.Param{s("profile")}, params...), append([]any{"p"}, bindings...)
					join := " LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id"
					proof := stmt.Plan{Hot: true, Indexes: []string{index, "personal_current_live"}, Seeks: []string{seek, "profile_id=? AND item_id=?"}}
					if lengths {
						name += ".bytes"
						columns = "e.id,octet_length(e." + key + "),octet_length(e.title)"
						p, b = params, bindings
						join = ""
						proof = stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
					}
					empty := append([]any(nil), b...)
					at := 0
					if !lengths {
						at = 1
					}
					empty[at] = int64(-1)
					defs = append(defs, stmt.Definition{ID: readStatementID(name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_filter_personal_window.go:BrowseReader.PersonalTitleFilterWindow"}, SQL: "SELECT " + columns + from + join + where + end, Params: p, Cohorts: []stmt.Cohort{{Name: "present", Bindings: b}, {Name: "empty", Bindings: empty}}, Plan: proof})
				}
			}
		}
	}
	return defs
}
