package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only the compiled recent-personal byte ranges below may preflight large
// personal-order lastPlayed octets. They grant no arbitrary range, expression,
// identifier or SQL-function permission. personalRating and playCount keys are
// fixed-width integers, so their recipes copy no variable source bytes.
const (
	rbPersonalPrepareRecentTablesSQL             = " FROM personal_items AS p INDEXED BY personal_current_recent INNER JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE "
	rbPersonalPrepareRecentFirstPredicateSQL     = "p.profile_id=? AND p.last_played_at>'' AND e.library_id=? AND e.kind=?"
	rbPersonalPrepareRecentTailPredicateSQL      = "p.profile_id=? AND p.last_played_at>'' AND e.library_id=? AND e.kind=? AND p.activity_visible=? AND p.last_played_at<?"
	rbPersonalPrepareRecentTailEqualPredicateSQL = "p.profile_id=? AND p.last_played_at>'' AND e.library_id=? AND e.kind=? AND p.activity_visible=? AND p.last_played_at=? AND p.item_public_id>?"
	rbPersonalPrepareRecentNextPredicateSQL      = "p.profile_id=? AND p.last_played_at>'' AND e.library_id=? AND e.kind=? AND p.activity_visible>?"
	rbPersonalPrepareRecentOrderSQL              = " ORDER BY p.activity_visible ASC,p.last_played_at DESC,p.item_public_id ASC LIMIT ?"
	rbPersonalPrepareRecentFirstSQL              = "SELECT e.id,octet_length(p.last_played_at)" + rbPersonalPrepareRecentTablesSQL + rbPersonalPrepareRecentFirstPredicateSQL + rbPersonalPrepareRecentOrderSQL
	rbPersonalPrepareRecentTailSQL               = "SELECT e.id,octet_length(p.last_played_at)" + rbPersonalPrepareRecentTablesSQL + rbPersonalPrepareRecentTailPredicateSQL + rbPersonalPrepareRecentOrderSQL
	rbPersonalPrepareRecentTailEqualSQL          = "SELECT e.id,octet_length(p.last_played_at)" + rbPersonalPrepareRecentTablesSQL + rbPersonalPrepareRecentTailEqualPredicateSQL + rbPersonalPrepareRecentOrderSQL
	rbPersonalPrepareRecentNextSQL               = "SELECT e.id,octet_length(p.last_played_at)" + rbPersonalPrepareRecentTablesSQL + rbPersonalPrepareRecentNextPredicateSQL + rbPersonalPrepareRecentOrderSQL
)

func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	for _, leg := range []struct {
		name   string
		sql    string
		params []stmt.Param
	}{
		{"first", rbPersonalPrepareRecentFirstSQL, []stmt.Param{s("profile"), i("library"), i("kind"), i("limit")}},
		{"tail", rbPersonalPrepareRecentTailSQL, []stmt.Param{s("profile"), i("library"), i("kind"), i("after_activity"), s("after_text"), i("limit")}},
		{"tail_equal", rbPersonalPrepareRecentTailEqualSQL, []stmt.Param{s("profile"), i("library"), i("kind"), i("after_activity"), s("after_text"), b("public_id"), i("limit")}},
		{"next", rbPersonalPrepareRecentNextSQL, []stmt.Param{s("profile"), i("library"), i("kind"), i("after_activity"), i("limit")}},
	} {
		id := stmt.ID("read.browse.rb.personal_prepare.recent." + leg.name + ".bytes")
		proof := stmt.Plan{Hot: true, Indexes: []string{"personal_current_recent"}, Seeks: []string{"profile_id=?", "rowid=?"}}
		pointOctets.add(octetShape{id: id, sql: leg.sql, mismatch: "exact bounded personal-order recent byte-source range required",
			definition: func(d stmt.Definition) bool {
				return d.ID == id && d.SQL == leg.sql && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, leg.params) && reflect.DeepEqual(d.Plan, proof)
			},
			audit: func(ctx context.Context, c *sql.Conn) error {
				return auditRBPersonalPrepareRecentIndex(ctx, c)
			},
			point: func(rows Rows) error {
				if len(rows.Values) == 0 || len(rows.Values[0]) != 4 {
					return fmt.Errorf("personal-order recent byte source requires its indexed SEARCH")
				}
				text, ok := rows.Values[0][3].(string)
				if !ok || !strings.HasPrefix(text, "SEARCH p USING INDEX personal_current_recent (") {
					return fmt.Errorf("personal-order recent byte source requires its exact indexed SEARCH")
				}
				for _, row := range rows.Values[1:] {
					if len(row) != 4 {
						return fmt.Errorf("personal-order recent byte source: invalid EQP")
					}
					detail, ok := row[3].(string)
					if !ok || !strings.HasPrefix(detail, "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)") {
						return fmt.Errorf("personal-order recent byte source: unexpected access %s", detail)
					}
				}
				return nil
			},
		})
	}
}

// auditRBPersonalPrepareRecentIndex proves the complete native current-recent
// key: profile, visibility, last-played DESC, public identity.
func auditRBPersonalPrepareRecentIndex(ctx context.Context, c *sql.Conn) error {
	indexes, err := query(ctx, c, "PRAGMA main.index_list(personal_items)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("personal-order recent: invalid index metadata")
		}
		if row[1] == "personal_current_recent" {
			if row[2] != int64(0) || row[3] != "c" || row[4] != int64(0) {
				return fmt.Errorf("personal-order recent requires the complete native index")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("personal-order recent native source index absent")
	}
	rows, err := query(ctx, c, "PRAGMA main.index_xinfo(personal_current_recent)")
	if err != nil {
		return err
	}
	names := []string{"profile_id", "activity_visible", "last_played_at", "item_public_id"}
	descs := []int64{0, 0, 1, 0}
	n := 0
	for _, row := range rows.Values {
		if len(row) != 6 {
			return fmt.Errorf("personal-order recent: invalid index metadata")
		}
		if row[5] != int64(1) {
			continue
		}
		if n >= len(names) || row[2] != names[n] || row[3] != descs[n] || row[4] != "BINARY" {
			return fmt.Errorf("personal-order recent native key changed")
		}
		n++
	}
	if n != len(names) {
		return fmt.Errorf("personal-order recent native key incomplete")
	}
	return nil
}
