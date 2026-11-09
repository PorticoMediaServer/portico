package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only this task's compiled container-child byte ranges may preflight octets.
// This grants no arbitrary range, expression, identifier or SQL-function
// permission. The read package composes the identical SQL from the same finite
// source shapes; any drift refuses qualification by exact definition comparison.
type rbContainerMetricsOctetKey struct {
	sql   string
	text  bool
	alias string
}

type rbContainerMetricsOctetSource struct {
	name, from, where, scopeJoin, extra, index, order, scopeAlias, scopeColumn, lastColumn string
	keys                                                                                   []rbContainerMetricsOctetKey
	textFirst                                                                              bool
	partial                                                                                bool
	params                                                                                 []stmt.Param
}

func rbContainerMetricsOctetSources() []rbContainerMetricsOctetSource {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	entity := "catalog_entities AS e INDEXED BY "
	return []rbContainerMetricsOctetSource{
		{
			name: "show", from: entity + "catalog_entities_show_fk",
			where: "show_id IS NOT NULL", scopeJoin: "e.show_id=?",
			index: "catalog_entities_show_fk", order: "k0 ASC", scopeAlias: "e", scopeColumn: "show_id", lastColumn: "id", partial: true,
			keys:   []rbContainerMetricsOctetKey{{sql: "e.id", alias: "k0"}},
			params: []stmt.Param{i("show"), i("library")},
		},
		{
			name: "season", from: entity + "catalog_entities_parent",
			where: "parent_id IS NOT NULL", scopeJoin: "e.parent_id=?",
			index: "catalog_entities_parent", order: "k0 ASC", scopeAlias: "e", scopeColumn: "parent_id", lastColumn: "id", partial: true,
			keys:   []rbContainerMetricsOctetKey{{sql: "e.id", alias: "k0"}},
			params: []stmt.Param{i("parent"), i("library")},
		},
		{
			name: "album", from: entity + "catalog_entities_album",
			where: "album_id IS NOT NULL", scopeJoin: "e.album_id=?",
			index: "catalog_entities_album", order: "k0 ASC,k1 ASC,k2 ASC", scopeAlias: "e", scopeColumn: "album_id", lastColumn: "id", partial: true,
			keys: []rbContainerMetricsOctetKey{
				{sql: "COALESCE(e.disc_number,1)", alias: "k0"},
				{sql: "COALESCE(e.track_number,0)", alias: "k1"},
				{sql: "e.id", alias: "k2"},
			},
			params: []stmt.Param{i("album"), i("library")},
		},
		{
			name: "book", from: entity + "catalog_entities_book",
			where: "book_id IS NOT NULL", scopeJoin: "e.book_id=?",
			index: "catalog_entities_book", order: "k0 ASC,k1 ASC,k2 ASC", scopeAlias: "e", scopeColumn: "book_id", lastColumn: "id", partial: true,
			keys: []rbContainerMetricsOctetKey{
				{sql: "COALESCE(e.disc_number,1)", alias: "k0"},
				{sql: "COALESCE(e.part_number,0)", alias: "k1"},
				{sql: "e.id", alias: "k2"},
			},
			params: []stmt.Param{i("book"), i("library")},
		},
		{
			name: "collection", from: "catalog_collection_members AS m INDEXED BY catalog_collection_members_page",
			scopeJoin: "m.collection_id=?", extra: " CROSS JOIN catalog_entities AS e ON e.id=m.item_id",
			index: "catalog_collection_members_page", order: "k0 ASC,k1 ASC", scopeAlias: "m", scopeColumn: "collection_id", lastColumn: "item_id",
			keys:   []rbContainerMetricsOctetKey{{sql: "m.order_key", text: true, alias: "k0"}, {sql: "m.item_id", alias: "k1"}},
			params: []stmt.Param{i("collection"), i("library")}, textFirst: true,
		},
	}
}

func rbContainerMetricsOctetKeyColumns(spec rbContainerMetricsOctetSource) string {
	var out string
	for i, key := range spec.keys {
		if i > 0 {
			out += ","
		}
		out += key.sql + " AS " + key.alias
	}
	return out
}

func rbContainerMetricsOctetSelect(spec rbContainerMetricsOctetSource, preds []string, order string) string {
	where := "dp.id=? AND e.library_id=?"
	if spec.where != "" {
		where = "dp.id=? AND " + spec.where + " AND e.library_id=?"
	}
	for _, pred := range preds {
		where += " AND " + pred
	}
	return "SELECT e.id,octet_length(e.latest_aired_key),COALESCE(octet_length(p.last_played_at),0)," + rbContainerMetricsOctetKeyColumns(spec) +
		" FROM direct_profiles AS dp CROSS JOIN " + spec.from + " ON " + spec.scopeJoin + spec.extra +
		" LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=dp.id AND p.item_id=e.id AND p.item_public_id=e.public_id" +
		" WHERE " + where + " ORDER BY " + order + " LIMIT ?"
}

// rbContainerMetricsOctetLegOrder omits the equality-prefix keys so the forced
// index supplies the order without a temporary B-tree.
func rbContainerMetricsOctetLegOrder(spec rbContainerMetricsOctetSource, fixed int) string {
	var out string
	for i := fixed; i < len(spec.keys); i++ {
		if out != "" {
			out += ","
		}
		out += "k" + strconv.Itoa(i) + " ASC"
	}
	return out
}

func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	p := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	for _, spec := range rbContainerMetricsOctetSources() {
		plan := stmt.Plan{Hot: true, Indexes: []string{spec.index, "personal_current_live", "sqlite_autoindex_direct_profiles_1"}, Seeks: []string{spec.scopeColumn + "=?", "profile_id=? AND item_id=?", "id=?"}}
		if spec.name == "collection" {
			plan.PrimaryKeys = []string{"e"}
		}
		type leg struct {
			name string
			pred []string
			kp   []stmt.Param
			kb   []any
		}
		var legs []leg
		if spec.textFirst {
			legs = append(legs, leg{name: "first"})
		}
		for level := 0; level < len(spec.keys); level++ {
			var preds []string
			var keyParams []stmt.Param
			var keyBinds []any
			for k := 0; k <= level; k++ {
				op := "="
				if k == level {
					op = ">"
				}
				preds = append(preds, spec.keys[k].sql+op+"?")
				name := "key" + strconv.Itoa(k)
				if spec.keys[k].text {
					keyParams = append(keyParams, stmt.Param{Name: name, Type: stmt.Text})
					keyBinds = append(keyBinds, "a")
				} else {
					keyParams = append(keyParams, i(name))
					keyBinds = append(keyBinds, int64(0))
				}
			}
			legs = append(legs, leg{name: "tail" + strconv.Itoa(level), pred: preds, kp: keyParams, kb: keyBinds})
		}
		for _, one := range legs {
			// SQL text order: child scope equality, profile PK bind, library.
			params := []stmt.Param{spec.params[0], p("profile"), spec.params[1]}
			params = append(params, one.kp...)
			params = append(params, i("limit"))
			order := spec.order
			if len(one.kp) > 0 {
				order = rbContainerMetricsOctetLegOrder(spec, len(one.kp)-1)
			}
			sqlText := rbContainerMetricsOctetSelect(spec, one.pred, order)
			id := stmt.ID("read.browse.rb.container_metrics.children." + spec.name + "." + one.name + ".bytes")
			legPlan := plan
			if spec.name == "album" || spec.name == "book" {
				fk := "album_id=?"
				if spec.name == "book" {
					fk = "book_id=?"
				}
				parts := []string{fk}
				for k := range one.kp {
					column, op := "<expr>", "=?"
					if k == 2 {
						column = "id"
					}
					if k == len(one.kp)-1 {
						op = ">?"
					}
					parts = append(parts, column+op)
				}
				legPlan.Waivers = append(legPlan.Waivers, stmt.Waiver{
					StatementID: id, Stage: "eqp", Form: "UNRECOGNIZED EQP",
					Match:  "SEARCH e USING INDEX " + spec.index + " (" + strings.Join(parts, " AND ") + ")",
					Reason: "reviewed expression-index keyset for the authored album/book child order",
				})
			}
			source := spec
			pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded container-child byte range required",
				definition: func(d stmt.Definition) bool {
					return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, legPlan)
				},
				audit: func(ctx context.Context, c *sql.Conn) error {
					return auditRBContainerMetricsIndex(ctx, c, source, len(source.keys))
				},
				point: func(rows Rows) error {
					for _, row := range rows.Values {
						if len(row) != 4 {
							continue
						}
						text, ok := row[3].(string)
						if !ok || !strings.HasPrefix(text, "SEARCH "+spec.scopeAlias+" USING ") {
							continue
						}
						// The planner may report a covering form of the same forced
						// index; both are the reviewed native range.
						if strings.Contains(text, "INDEX "+spec.index+" (") {
							return nil
						}
					}
					return fmt.Errorf("container-child byte range requires its exact indexed SEARCH")
				},
			})
		}
	}
}

// auditRBContainerMetricsIndex proves the compiled source's native index shape:
// the expected table, an ordinary (not auto) index, the expected partial form,
// the scope column first, the entity key last, and one key entry per order key.
func auditRBContainerMetricsIndex(ctx context.Context, c *sql.Conn, spec rbContainerMetricsOctetSource, keys int) error {
	table := "catalog_entities"
	if spec.name == "collection" {
		table = "catalog_collection_members"
	}
	rows, err := query(ctx, c, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range rows.Values {
		if len(row) != 5 || row[1] != spec.index {
			continue
		}
		if row[3] != "c" || (row[4] == int64(1)) != spec.partial {
			return fmt.Errorf("container-child source requires its complete native index form")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("container-child native source index absent")
	}
	rows, err = query(ctx, c, "PRAGMA main.index_xinfo("+identifier(spec.index)+")")
	if err != nil {
		return err
	}
	type keyColumn struct {
		name any
	}
	var key []keyColumn
	for _, row := range rows.Values {
		if len(row) != 6 {
			return fmt.Errorf("container-child source: invalid index metadata")
		}
		if row[5] != int64(1) {
			continue
		}
		key = append(key, keyColumn{row[2]})
	}
	if len(key) != keys+1 {
		return fmt.Errorf("container-child source native key incomplete")
	}
	if key[0].name != spec.scopeColumn || key[len(key)-1].name != spec.lastColumn {
		return fmt.Errorf("container-child source native key changed")
	}
	if spec.name == "collection" && key[1].name != "order_key" {
		return fmt.Errorf("container-child collection page key changed")
	}
	return nil
}
