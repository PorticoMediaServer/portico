package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these compiled bounded selection title-source ranges may preflight
// octets. This grants no arbitrary range, expression, identifier or SQL
// function permission.
func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, part := range []string{"first", "tail", "next"} {
			where := " WHERE e.library_id=? AND e.kind=?"
			seek := "library_id=? AND kind=?"
			params := []stmt.Param{i("library"), i("kind")}
			if part == "tail" {
				where += " AND e." + key + " COLLATE NOCASE=? AND e.id>?"
				seek = key + "=? AND id>?"
				params = append(params, s("key"), i("after"))
			}
			if part == "next" {
				where += " AND e." + key + " COLLATE NOCASE>?"
				seek = key + ">?"
				params = append(params, s("key"))
			}
			params = append(params, i("limit"))
			id := stmt.ID("read.browse.rb.selection_prepare.title." + variant + part + ".bytes")
			sqlText := "SELECT e.id,octet_length(e." + key + "),octet_length(e.title) FROM catalog_entities AS e INDEXED BY " + index + where + " ORDER BY e." + key + " COLLATE NOCASE ASC,e.id ASC LIMIT ?"
			proof := stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
			pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded selection title byte-source range required",
				definition: func(d stmt.Definition) bool {
					return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
				},
				audit: func(ctx context.Context, c *sql.Conn) error { return auditRBPersonalTitleIndex(ctx, c, index, key) },
				point: func(rows Rows) error {
					if len(rows.Values) != 1 || len(rows.Values[0]) != 4 {
						return fmt.Errorf("selection title byte source requires one indexed SEARCH")
					}
					text, ok := rows.Values[0][3].(string)
					if !ok || !strings.HasPrefix(text, "SEARCH e USING ") || !strings.Contains(text, "INDEX "+index+" (") {
						return fmt.Errorf("relationship/selection title byte source requires its exact indexed SEARCH")
					}
					return nil
				},
			})
		}
	}
}
