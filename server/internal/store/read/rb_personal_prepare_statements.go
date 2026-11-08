package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert definitions; the caller must Capture the actual Reader before use.
// Callers also compose RBPersonalTitleFilterDefinitions(): this recipe reuses
// its dual title byte ranges for the title-default remainder.
//
// The valued recipes walk the maintained current-personal value indexes with a
// private (value, item_public_id) cursor. Their natural order ties equal values
// by item_public_id, so the complete valued set is reduced and sorted once by
// entity ID; a range cursor is never mistaken for the public order. The title
// remainder is the existing library/kind dual-title order with the current
// personal row joined by the unique (profile,item) point.
func RBPersonalPrepareDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbPersonalPrepareDefinitions()...)
}

var rbPersonalPrepareZeroPublic = make([]byte, 16)

func rbPersonalPrepareDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, present, empty []any, indexes, pks, seeks []string) {
		out = append(out, stmt.Definition{ID: readStatementID("rb.personal_prepare." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_personal_prepare.go:RBPersonalPreparation.Advance"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: indexes, PrimaryKeys: pks, Seeks: seeks}})
	}
	entity := "e.retired,e.revision,e.admission_own_age"
	join := " FROM personal_items AS p INDEXED BY "
	entityJoin := " JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE p.profile_id=? AND "
	scope := " AND e.library_id=? AND e.kind=? ORDER BY "
	limit := " LIMIT ?"

	// personalRating: current rating index (profile,rating,item_public_id).
	add("rating.first",
		"SELECT e.id,p.rating,p.item_public_id,"+entity+join+"personal_current_rating"+entityJoin+"p.rating>0"+scope+"p.rating ASC,p.item_public_id ASC"+limit,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_rating"}, []string{"e"}, []string{"profile_id=? AND rating>?", "rowid=?"})
	add("rating.tail",
		"SELECT e.id,p.rating,p.item_public_id,"+entity+join+"personal_current_rating"+entityJoin+"p.rating>0 AND (p.rating>? OR (p.rating=? AND p.item_public_id>?))"+scope+"p.rating ASC,p.item_public_id ASC"+limit,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("after"), b("public_id"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(0), rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(0), rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_rating"}, []string{"e"}, []string{"profile_id=? AND rating>?", "rowid=?"})

	// playCount: current plays index (profile,play_count DESC,item_public_id).
	add("plays.first",
		"SELECT e.id,p.play_count,p.item_public_id,"+entity+join+"personal_current_plays"+entityJoin+"p.play_count>0"+scope+"p.play_count DESC,p.item_public_id ASC"+limit,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_plays"}, []string{"e"}, []string{"profile_id=? AND play_count>?", "rowid=?"})
	add("plays.tail",
		"SELECT e.id,p.play_count,p.item_public_id,"+entity+join+"personal_current_plays"+entityJoin+"p.play_count>0 AND (p.play_count<? OR (p.play_count=? AND p.item_public_id>?))"+scope+"p.play_count DESC,p.item_public_id ASC"+limit,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("after"), b("public_id"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(0), rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(0), rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_plays"}, []string{"e"}, []string{"profile_id=? AND play_count>?", "rowid=?"})

	// lastPlayed: current recent index (profile,activity_visible,last_played_at
	// DESC,item_public_id). Hidden activity rows keep their last-played value;
	// the public order never reads activity_visible. The .bytes pairs preflight
	// the exact variable key bytes before hydration.
	recentFrom := " FROM personal_items AS p INDEXED BY personal_current_recent JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE p.profile_id=? AND p.last_played_at>'' AND e.library_id=? AND e.kind=?"
	recentOrder := " ORDER BY p.activity_visible ASC,p.last_played_at DESC,p.item_public_id ASC LIMIT ?"
	add("recent.first",
		"SELECT e.id,p.last_played_at,p.activity_visible,p.item_public_id,"+entity+recentFrom+recentOrder,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_recent"}, []string{"e"}, []string{"profile_id=?", "rowid=?"})
	add("recent.tail",
		"SELECT e.id,p.last_played_at,p.activity_visible,p.item_public_id,"+entity+recentFrom+" AND (p.activity_visible>? OR (p.activity_visible=? AND (p.last_played_at<? OR (p.last_played_at=? AND p.item_public_id>?))))"+recentOrder,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("after_activity"), s("after_text"), b("public_id"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(0), "2026-01-01T00:00:00Z", rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(0), "2026-01-01T00:00:00Z", rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_recent"}, []string{"e"}, []string{"profile_id=?", "rowid=?"})
	add("recent.first.bytes",
		"SELECT e.id,octet_length(p.last_played_at)"+recentFrom+recentOrder,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_recent"}, nil, []string{"profile_id=?", "rowid=?"})
	add("recent.tail.bytes",
		"SELECT e.id,octet_length(p.last_played_at)"+recentFrom+" AND (p.activity_visible>? OR (p.activity_visible=? AND (p.last_played_at<? OR (p.last_played_at=? AND p.item_public_id>?))))"+recentOrder,
		[]stmt.Param{s("profile"), i("library"), i("kind"), i("after_activity"), s("after_text"), b("public_id"), i("limit")},
		[]any{"p", int64(1), int64(1), int64(0), "2026-01-01T00:00:00Z", rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]any{"missing", int64(999999), int64(1), int64(0), "2026-01-01T00:00:00Z", rbPersonalPrepareZeroPublic, int64(rbPersonalPrepareRaw + 1)},
		[]string{"personal_current_recent"}, nil, []string{"profile_id=?", "rowid=?"})

	// The title-default remainder: complete library/kind title order with the
	// current personal row joined by its unique point. The Go reducer skips the
	// rows valued for the requested order, so one statement serves all three.
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		columns := "e.id,e." + key + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age,p.rating,p.play_count,p.last_played_at"
		from := " FROM catalog_entities AS e INDEXED BY " + index + " LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id AND p.item_public_id=e.public_id WHERE e.library_id=? AND e.kind=?"
		order := " ORDER BY e." + key + " COLLATE NOCASE ASC,e.id ASC LIMIT ?"
		seek := "library_id=? AND kind=?"
		for _, part := range []string{"first", "tail", "next"} {
			where, params, present, empty := from, []stmt.Param{s("profile"), i("library"), i("kind")}, []any{"p", int64(1), int64(1)}, []any{"missing", int64(999999), int64(1)}
			if part == "tail" {
				where += " AND e." + key + " COLLATE NOCASE=? AND e.id>?"
				params = append(params, s("key"), i("after"))
				present = append(present, "same", int64(3))
				empty = append(empty, "same", int64(3))
				seek += " AND " + key + "=? AND id>?"
			}
			if part == "next" {
				where += " AND e." + key + " COLLATE NOCASE>?"
				params = append(params, s("key"))
				present = append(present, "same")
				empty = append(empty, "same")
				seek += " AND " + key + ">?"
			}
			params = append(params, i("limit"))
			present = append(present, int64(rbPersonalPrepareRaw+1))
			empty = append(empty, int64(rbPersonalPrepareRaw+1))
			add("title."+variant+part, "SELECT "+columns+where+order, params, present, empty,
				[]string{index, "personal_current_live"}, []string{"e"}, []string{seek, "profile_id=? AND item_id=?"})
		}
	}
	return out
}
