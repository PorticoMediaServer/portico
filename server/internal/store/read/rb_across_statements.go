package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBAcrossDefinitions is the cross-library ready inventory. It reuses the
// exact catalog_entities title/added/content/scalar indexes and adds no new
// index, table, expression or dynamic SQL identifier.
func RBAcrossDefinitions() []stmt.Definition {
	return append(RBDirectoryDefinitions(), rbAcrossDefinitions()...)
}

func rbAcrossDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sqlText string, params []stmt.Param, present, empty []any, index string) {
		out = append(out, stmt.Definition{ID: statementID(name), Owner: "store/read", Kind: stmt.Read,
			CallSites: []string{"store/read/rb_across.go:rbAcrossFetch"}, SQL: sqlText, Params: params,
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{index}}})
	}
	// Title order: the same dual title points and ASCII NOCASE tie the one
	// library reader uses. The equality tail and next-key seek are disjoint.
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "title.ignore"
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "title.keep"
		}
		for _, desc := range []bool{false, true} {
			direction, order, op := "asc", "ASC", ">"
			if desc {
				direction, order, op = "desc", "DESC", "<"
			}
			base := "SELECT e.id,e." + key + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age FROM catalog_entities AS e INDEXED BY " + index + " WHERE e.library_id=? AND e.kind=?"
			end := " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
			args := []any{int64(1), int64(1)}
			empty := []any{int64(999999), int64(1)}
			add("rb.across."+variant+"."+direction+".first", base+end,
				[]stmt.Param{i("library"), i("kind"), i("limit")}, append(args, int64(MaxRaw+1)), append(empty, int64(MaxRaw+1)), index)
			add("rb.across."+variant+"."+direction+".tail", base+" AND e."+key+" COLLATE NOCASE=? AND e.id"+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")},
				append(args, "same", int64(3), int64(MaxRaw+1)), append(empty, "same", int64(3), int64(MaxRaw+1)), index)
			add("rb.across."+variant+"."+direction+".next", base+" AND e."+key+" COLLATE NOCASE"+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), s("key"), i("limit")},
				append(args, "same", int64(MaxRaw+1)), append(empty, "same", int64(MaxRaw+1)), index)
		}
	}
	// Accepted chronology, including its undated bucket: NULL first ascending,
	// NULL last descending, exactly as the one-library chronology source.
	for _, clock := range []struct {
		name, column, index string
	}{
		{"added", "added_at", "catalog_entities_added_v1"},
		{"contentAdded", "last_content_added_at", "catalog_entities_content_added"},
	} {
		for _, desc := range []bool{false, true} {
			direction, order, op := "asc", "ASC", ">"
			if desc {
				direction, order, op = "desc", "DESC", "<"
			}
			base := "SELECT e.id,e." + clock.column + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age FROM catalog_entities AS e INDEXED BY " + clock.index + " WHERE e.library_id=? AND e.kind=?"
			end := " ORDER BY e." + clock.column + " " + order + ",e.id " + order + " LIMIT ?"
			prefix := "rb.across." + clock.name + "." + direction + "."
			args := []any{int64(1), int64(2)}
			empty := []any{int64(999999), int64(2)}
			add(prefix+"first", base+end, []stmt.Param{i("library"), i("kind"), i("limit")}, append(args, int64(MaxRaw+1)), append(empty, int64(MaxRaw+1)), clock.index)
			add(prefix+"tail", base+" AND e."+clock.column+"=? AND e.id"+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")},
				append(args, "2026-01-01T00:00:00.000000000Z", int64(3), int64(MaxRaw+1)),
				append(empty, "2026-01-01T00:00:00.000000000Z", int64(3), int64(MaxRaw+1)), clock.index)
			add(prefix+"next", base+" AND e."+clock.column+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), s("key"), i("limit")},
				append(args, "2026-01-01T00:00:00.000000000Z", int64(MaxRaw+1)),
				append(empty, "2026-01-01T00:00:00.000000000Z", int64(MaxRaw+1)), clock.index)
			add(prefix+"null_tail", base+" AND e."+clock.column+" IS NULL AND e.id"+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), i("after"), i("limit")},
				append(args, int64(0), int64(MaxRaw+1)), append(empty, int64(0), int64(MaxRaw+1)), clock.index)
			part, name := "values", "values"
			if desc {
				part, name = "nulls", "nulls"
			}
			add(prefix+name, base+" AND e."+clock.column+" IS "+map[string]string{"values": "NOT NULL", "nulls": "NULL"}[part]+end,
				[]stmt.Param{i("library"), i("kind"), i("limit")}, append(args, int64(MaxRaw+1)), append(empty, int64(MaxRaw+1)), clock.index)
		}
	}
	// Landed scalar orders reuse their exact maintained index and partial
	// predicate; missing values keep the same NULL bucket as the source order.
	for _, order := range rbScalarOrders() {
		for _, desc := range []bool{false, true} {
			direction, orderBy, op := "asc", "ASC", ">"
			if desc {
				direction, orderBy, op = "desc", "DESC", "<"
			}
			base := "SELECT e.id,e." + order.column + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age FROM catalog_entities AS e INDEXED BY " + order.index + " WHERE e.library_id=? AND e.kind=?"
			if order.predicate != "" {
				base += " AND " + order.predicate
			}
			end := " ORDER BY e." + order.column + " " + orderBy + ",e.id " + orderBy + " LIMIT ?"
			prefix := "rb.across.scalar." + string(order.id) + "." + direction + "."
			key := func(v any) []any { return []any{v} }
			keyValue := any(int64(1))
			switch order.typ {
			case stmt.Real:
				keyValue = float64(1)
			case stmt.Text:
				keyValue = "a"
			}
			args := []any{int64(1), order.kind}
			empty := []any{int64(999999), order.kind}
			add(prefix+"first", base+end, []stmt.Param{i("library"), i("kind"), i("limit")}, append(args, int64(MaxRaw+1)), append(empty, int64(MaxRaw+1)), order.index)
			add(prefix+"tail", base+" AND e."+order.column+"=? AND e.id"+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), {Name: "key", Type: order.typ}, i("after"), i("limit")},
				append(append(append([]any{}, args...), key(keyValue)...), int64(3), int64(MaxRaw+1)),
				append(append(append([]any{}, empty...), key(keyValue)...), int64(3), int64(MaxRaw+1)), order.index)
			add(prefix+"next", base+" AND e."+order.column+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), {Name: "key", Type: order.typ}, i("limit")},
				append(append(append([]any{}, args...), key(keyValue)...), int64(MaxRaw+1)),
				append(append(append([]any{}, empty...), key(keyValue)...), int64(MaxRaw+1)), order.index)
			add(prefix+"null_tail", base+" AND e."+order.column+" IS NULL AND e.id"+op+"?"+end,
				[]stmt.Param{i("library"), i("kind"), i("after"), i("limit")},
				append(args, int64(0), int64(MaxRaw+1)), append(empty, int64(0), int64(MaxRaw+1)), order.index)
			part, name := "values", "values"
			if desc {
				part, name = "nulls", "nulls"
			}
			add(prefix+name, base+" AND e."+order.column+" IS "+map[string]string{"values": "NOT NULL", "nulls": "NULL"}[part]+end,
				[]stmt.Param{i("library"), i("kind"), i("limit")}, append(args, int64(MaxRaw+1)), append(empty, int64(MaxRaw+1)), order.index)
		}
	}
	return out
}
