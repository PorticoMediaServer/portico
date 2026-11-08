package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these compiled bounded title-source ranges may preflight octets. This
// grants no arbitrary range, expression, identifier or SQL-function permission.
func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
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
				if part == "tail" {
					where += " AND e." + key + " COLLATE NOCASE=? AND e.id" + op + "?"
					seek = key + "=? AND id" + op + "?"
					params = append(params, s("key"), i("after"))
				}
				if part == "next" {
					where += " AND e." + key + " COLLATE NOCASE" + op + "?"
					seek = key + op + "?"
					params = append(params, s("key"))
				}
				params = append(params, i("limit"))
				id := stmt.ID("read.browse.rb.filter.personal.title." + variant + direction + part + ".bytes")
				sqlText := "SELECT e.id,octet_length(e." + key + "),octet_length(e.title) FROM catalog_entities AS e INDEXED BY " + index + where + " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
				proof := stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
				pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded personal title byte-source range required",
					definition: func(d stmt.Definition) bool {
						return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
					},
					audit: func(ctx context.Context, c *sql.Conn) error { return auditRBPersonalTitleIndex(ctx, c, index, key) },
					point: func(rows Rows) error {
						if len(rows.Values) != 1 || len(rows.Values[0]) != 4 {
							return fmt.Errorf("personal title byte source requires one indexed SEARCH")
						}
						text, ok := rows.Values[0][3].(string)
						if !ok || !strings.HasPrefix(text, "SEARCH e USING INDEX "+index+" (") {
							return fmt.Errorf("personal title byte source requires its exact indexed SEARCH")
						}
						return nil
					},
				})
			}
		}
	}
}

func auditRBPersonalTitleIndex(ctx context.Context, c *sql.Conn, index, key string) error {
	rows, err := query(ctx, c, "PRAGMA main.index_list(catalog_entities)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range rows.Values {
		if row[1] == index {
			if row[2] != int64(0) || row[3] != "c" || row[4] != int64(0) {
				return fmt.Errorf("personal title source requires the complete native index")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("personal title native source index absent")
	}
	rows, err = query(ctx, c, "PRAGMA main.index_xinfo("+identifier(index)+")")
	if err != nil {
		return err
	}
	names := []string{"library_id", "kind", key, "id"}
	collations := []string{"BINARY", "BINARY", "NOCASE", "BINARY"}
	n := 0
	for _, row := range rows.Values {
		if row[5] != int64(1) {
			continue
		}
		if n >= len(names) || row[2] != names[n] || row[3] != int64(0) || row[4] != collations[n] {
			return fmt.Errorf("personal title source native key changed")
		}
		n++
	}
	if n != len(names) {
		return fmt.Errorf("personal title source native key incomplete")
	}
	return nil
}
