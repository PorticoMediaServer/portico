package read

import (
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RBNumericContextDefinitions is inert explicit harness inventory. Request
// values are bindings only: request fields can never select SQL, columns or an
// index. The year walk keeps the packet's primary rb.numeric_context.* names;
// the other finite source orders are secondary variants of the same complete
// library/kind walk.
func RBNumericContextDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbNumericContextDefinitions()...)
}

func rbNumericContextDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }

	columns := "e.id,e.year,e.release_key,e.community_rating_key,e.critic_rating_key,e.title,e.public_id,e.retired,e.admission_own_age,e.release_date,e.added_at,p.revision,p.item_public_id,p.library_id,p.favorite,p.watchlisted,p.rating"
	join := " FROM catalog_entities AS e INDEXED BY %s LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id"
	where := " WHERE e.library_id=? AND e.kind=?"

	type source struct {
		name, index, key, order string
		keyType                 stmt.Type
		partial                 string
	}
	sources := []source{
		{name: "", index: "catalog_entities_year", key: "e.year", order: "e.year", keyType: stmt.Integer},
		{name: "library", index: "catalog_entities_library", key: "e.id", order: "e.id", keyType: stmt.Integer},
		{name: "release", index: "catalog_entities_release", key: "e.release_key", order: "e.release_key", keyType: stmt.Text, partial: " AND kind IN(1,2,3,4,6,8)"},
		{name: "rating", index: "catalog_entities_rating", key: "e.community_rating_key", order: "e.community_rating_key", keyType: stmt.Real, partial: " AND kind IN(1,2,3,4,5,6,7,8,9,10,13)"},
		{name: "critic", index: "catalog_entities_critic", key: "e.critic_rating_key", order: "e.critic_rating_key", keyType: stmt.Real, partial: " AND kind IN(1,2,3,4,10)"},
	}
	var out []stmt.Definition
	add := func(id, sql string, params []stmt.Param, bindings []any, indexes []string, seeks []string) {
		empty := append([]any(nil), bindings...)
		// Every walk form binds the profile first and the library second.
		if len(empty) > 1 {
			empty[1] = int64(-1)
		}
		out = append(out, stmt.Definition{
			ID: readStatementID(id), Owner: "store/read", Kind: stmt.Read,
			CallSites: []string{"store/read/rb_numeric_context.go:RBNumericContextPreparation.window"},
			SQL:       sql, Params: params,
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}},
			Plan:    stmt.Plan{Hot: true, Indexes: indexes, Seeks: seeks},
		})
	}
	for _, src := range sources {
		prefix := "rb.numeric_context"
		if src.name != "" {
			prefix += "." + src.name
		}
		base := "SELECT " + columns + fmt.Sprintf(join, src.index) + where + src.partial
		end := " ORDER BY " + src.order + " ASC,e.id ASC LIMIT ?"
		indexes := []string{src.index, "personal_current_live"}
		firstSeek := "library_id=? AND kind=?"
		add(prefix+".first", base+end,
			[]stmt.Param{s("profile"), i("library"), i("kind"), i("limit")},
			[]any{"p", int64(1), int64(1), int64(rbNumericContextRaw)},
			indexes, []string{firstSeek, "profile_id=? AND item_id=?"})
		if src.name == "library" {
			// The entity id is unique, so the equality/next-key legs collapse to
			// one strictly-greater id seek and carry no unused key binding.
			add(prefix+".tail", base+" AND e.id>?"+end,
				[]stmt.Param{s("profile"), i("library"), i("kind"), i("after"), i("limit")},
				[]any{"p", int64(1), int64(1), int64(0), int64(rbNumericContextRaw)},
				indexes, []string{"id>?", "profile_id=? AND item_id=?"})
			add(prefix+".next", base+" AND e.id>?"+end,
				[]stmt.Param{s("profile"), i("library"), i("kind"), i("key"), i("limit")},
				[]any{"p", int64(1), int64(1), int64(1), int64(rbNumericContextRaw)},
				indexes, []string{"id>?", "profile_id=? AND item_id=?"})
			continue
		}
		walkSQL, nextSQL := src.key+"=? AND e.id>?", src.key+">?"
		walk, nextSeek := unqualifiedKey(src.key)+"=? AND id>?", unqualifiedKey(src.key)+">?"
		key := stmt.Param{Name: "key", Type: src.keyType}
		add(prefix+".tail", base+" AND "+walkSQL+end,
			[]stmt.Param{s("profile"), i("library"), i("kind"), key, i("after"), i("limit")},
			[]any{"p", int64(1), int64(1), numericContextKeyBinding(src.keyType), int64(0), int64(rbNumericContextRaw)},
			indexes, []string{walk, "profile_id=? AND item_id=?"})
		add(prefix+".next", base+" AND "+nextSQL+end,
			[]stmt.Param{s("profile"), i("library"), i("kind"), stmt.Param{Name: "key", Type: src.keyType}, i("limit")},
			[]any{"p", int64(1), int64(1), numericContextKeyBinding(src.keyType), int64(rbNumericContextRaw)},
			indexes, []string{nextSeek, "profile_id=? AND item_id=?"})
	}

	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}
	add("rb.numeric_context.scalar", "SELECT e.id,e.year,e.title,e.release_date,e.added_at,e.community_rating_key,e.critic_rating_key FROM catalog_entities AS e WHERE e.id=?",
		[]stmt.Param{i("entity")}, []any{int64(1)}, []string{"catalog_entities"}, []string{"rowid=?"})
	out[len(out)-1].Plan = point
	out[len(out)-1].Cohorts[1].Bindings = []any{int64(999999)}
	add("rb.numeric_context.scalar_octets", "SELECT octet_length(e.title),octet_length(e.release_date),octet_length(e.added_at) FROM catalog_entities AS e WHERE e.id=?",
		[]stmt.Param{i("entity")}, []any{int64(1)}, []string{"catalog_entities"}, []string{"rowid=?"})
	out[len(out)-1].Plan = point
	out[len(out)-1].Cohorts[1].Bindings = []any{int64(999999)}
	return out
}

func unqualifiedKey(key string) string {
	if len(key) > 2 && key[:2] == "e." {
		return key[2:]
	}
	return key
}

func numericContextKeyBinding(t stmt.Type) any {
	switch t {
	case stmt.Real:
		return float64(7)
	case stmt.Text:
		return "2026-01-02"
	default:
		return int64(2001)
	}
}
