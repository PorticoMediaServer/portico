package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// UO read byte preflights own their exact shapes here. The SQL text is repeated
// from the read package on purpose: plan's registry is the authority, so a
// drift between a read statement and its preflight refuses Capture instead of
// silently pairing a different projection.
type uoIndexKey struct {
	name, coll string
	desc       int64
}

type uoReadOctet struct {
	id      stmt.ID
	sql     string
	label   string
	owner   string
	params  []stmt.Param
	plan    stmt.Plan
	table   string
	index   string
	autoKey []string
	keys    []uoIndexKey
	partial bool
	ddl     string
	eqp     []string
	eqpRows int
}

func uoOctetAuto(table, name string, columns ...string) stmt.AutoIndexBinding {
	out := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
	for _, column := range columns {
		out.Columns = append(out.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return out
}

func addUOReadOctet(s uoReadOctet) {
	if s.id == "" || s.sql == "" || s.label == "" || s.owner == "" || s.table == "" || len(s.eqp) == 0 || s.eqpRows == 0 {
		panic("plan: incomplete UO read octet")
	}
	if s.autoKey == nil && s.index == "" {
		panic("plan: UO read octet requires a native key")
	}
	pointOctets.add(octetShape{
		id: s.id, sql: s.sql, mismatch: s.label + " requires the exact closed bounded point",
		definition: func(d stmt.Definition) bool {
			return d.ID == s.id && d.SQL == s.sql && d.Kind == stmt.Read && d.Owner == s.owner &&
				reflect.DeepEqual(d.Params, s.params) && reflect.DeepEqual(d.Plan, s.plan)
		},
		audit: func(ctx context.Context, c *sql.Conn) error { return auditUOReadOctet(ctx, c, s) },
		point: func(rows Rows) error { return checkUOReadOctet(rows, s) },
	})
}

func auditUOReadOctet(ctx context.Context, c *sql.Conn, s uoReadOctet) error {
	if err := uoAuditOctetLength(ctx, c); err != nil {
		return err
	}
	if s.autoKey != nil {
		return auditClosedOctetKey(ctx, c, s.table, s.index, s.autoKey)
	}
	return uoAuditIndex(ctx, c, s)
}

func uoAuditOctetLength(ctx context.Context, c *sql.Conn) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("UO read octet: invalid native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("UO read octet: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("UO read octet: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("UO read octet: unshadowed native octet_length required")
	}
	return nil
}

// uoAuditIndex proves the installed ordinary index has its exact key order,
// collations, DESC flags and (when partial) its exact predicate.
func uoAuditIndex(ctx context.Context, c *sql.Conn, s uoReadOctet) error {
	if err := mainBaseTable(ctx, c, s.table); err != nil {
		return err
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list("+identifier(s.table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("UO read octet: invalid native index metadata")
		}
		if row[1] != s.index {
			continue
		}
		unique, uniqueOK := row[2].(int64)
		partial, partialOK := row[4].(int64)
		if !uniqueOK || !partialOK || unique != 0 || row[3] != "c" || (partial == 1) != s.partial {
			return fmt.Errorf("UO read octet: ordinary %s index required", map[bool]string{true: "partial", false: "plain"}[s.partial])
		}
		found = true
	}
	if !found {
		return fmt.Errorf("UO read octet: sparse index absent")
	}
	keys, err := query(ctx, c, "PRAGMA main.index_xinfo("+identifier(s.index)+")")
	if err != nil {
		return err
	}
	n := 0
	for _, row := range keys.Values {
		if len(row) < 6 {
			return fmt.Errorf("UO read octet: invalid native index metadata")
		}
		if row[5] != int64(1) {
			continue
		}
		if n >= len(s.keys) || row[2] != s.keys[n].name || row[3] != s.keys[n].desc || row[4] != s.keys[n].coll {
			return fmt.Errorf("UO read octet: index key drifted")
		}
		n++
	}
	if n != len(s.keys) {
		return fmt.Errorf("UO read octet: index key incomplete")
	}
	ddl, err := query(ctx, c, "SELECT sql FROM main.sqlite_schema WHERE type='index' AND name=?", s.index)
	if err != nil {
		return err
	}
	if len(ddl.Values) != 1 || len(ddl.Values[0]) != 1 {
		return fmt.Errorf("UO read octet: index DDL absent")
	}
	text, ok := ddl.Values[0][0].(string)
	if !ok || uoNormalizeDDL(text) != uoNormalizeDDL(s.ddl) {
		return fmt.Errorf("UO read octet: index DDL drifted")
	}
	return nil
}

func uoNormalizeDDL(text string) string {
	return strings.ToLower(strings.TrimSuffix(strings.Join(strings.Fields(text), " "), ";"))
}

func checkUOReadOctet(rows Rows, s uoReadOctet) error {
	if len(rows.Values) != s.eqpRows {
		return fmt.Errorf("%s: exact %d-row point plan required", s.label, s.eqpRows)
	}
	details := make([]string, 0, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) != 4 {
			return fmt.Errorf("%s: invalid EQP", s.label)
		}
		detail, ok := row[3].(string)
		if !ok {
			return fmt.Errorf("%s: invalid EQP", s.label)
		}
		if strings.Contains(detail, "TEMP B-TREE") || strings.HasPrefix(detail, "SCAN ") {
			return fmt.Errorf("%s: indexed SEARCH required", s.label)
		}
		details = append(details, detail)
	}
	for _, want := range s.eqp {
		found := false
		for _, detail := range details {
			if strings.Contains(detail, want) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: required access %q absent", s.label, want)
		}
	}
	return nil
}

// uoActivityOctetSQL mirrors the read package's size projection byte for byte.
func uoActivityOctetSQL(column, index, variant string) string {
	seek := ""
	switch variant {
	case "same":
		seek = ` AND p.` + column + `=? AND p.item_public_id>?`
	case "older":
		seek = ` AND p.` + column + `<?`
	}
	return `SELECT p.item_public_id,e.id,octet_length(e.title)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_items AS p INDEXED BY ` + index + ` ON p.profile_id=f.profile_id
LEFT JOIN catalog_entities AS e ON e.id=p.item_id
WHERE t.token_hash=? AND p.activity_visible=1 AND p.` + column + ` IS NOT NULL` + seek + `
ORDER BY p.` + column + ` DESC,p.item_public_id ASC LIMIT ?`
}

func uoActivityHistoryOctetSQL(variant string) string {
	seek := ""
	switch variant {
	case "same":
		seek = ` AND h.occurred_at=? AND h.id<?`
	case "older":
		seek = ` AND h.occurred_at<?`
	}
	return `SELECT h.id,octet_length(h.occurred_at),octet_length(h.event_kind)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN play_history AS h INDEXED BY play_history_admin_user ON h.account_key=f.account_id
WHERE t.token_hash=? AND h.profile_id=f.profile_id AND h.membership_incarnation=f.command_member_incarnation AND h.profile_lifetime=f.command_profile_lifetime` + seek + `
ORDER BY h.occurred_at DESC,h.id DESC LIMIT ?`
}

func init() {
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	token := uoOctetAuto("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := uoOctetAuto("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	for _, f := range []struct{ name, column, index, ddl string }{
		{"recent", "last_played_at", "personal_current_recent", "create index personal_current_recent on personal_items(profile_id,activity_visible,last_played_at desc,item_public_id)"},
		{"activity", "activity_at", "personal_current_activity", "create index personal_current_activity on personal_items(profile_id,activity_visible,activity_at desc,item_public_id)"},
		{"completed", "last_completed_at", "personal_current_completed", "create index personal_current_completed on personal_items(profile_id,activity_visible,last_completed_at desc,item_public_id)"},
	} {
		for _, variant := range []string{"first", "same", "older"} {
			params := []stmt.Param{text("token"), integer("limit")}
			eqp := []string{"t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)", "f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)", "p USING INDEX " + f.index + " (", "e USING INTEGER PRIMARY KEY (rowid=?)"}
			switch variant {
			case "same":
				params = []stmt.Param{text("token"), text("time"), blob("public"), integer("limit")}
				eqp = append(eqp, "item_public_id>?")
			case "older":
				params = []stmt.Param{text("token"), text("time"), integer("limit")}
				eqp = append(eqp, f.column+"<?")
			}
			plan := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{token.Name, family.Name, f.index}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "profile_id=?"}}
			addUOReadOctet(uoReadOctet{
				id:  stmt.ID("read.foreground.uo.activity." + f.name + "." + variant + ".bytes"),
				sql: uoActivityOctetSQL(f.column, f.index, variant), label: "UO activity bytes", owner: "store/read UO activity",
				params: params, plan: plan, table: "personal_items", index: f.index,
				keys: []uoIndexKey{{"profile_id", "BINARY", 0}, {"activity_visible", "BINARY", 0}, {f.column, "BINARY", 1}, {"item_public_id", "BINARY", 0}},
				ddl:  f.ddl,
				eqp:  eqp, eqpRows: 4,
			})
		}
	}
	for _, variant := range []string{"first", "same", "older"} {
		params := []stmt.Param{text("token"), integer("limit")}
		eqp := []string{"t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)", "f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)", "h USING INDEX play_history_admin_user ("}
		switch variant {
		case "same":
			params = []stmt.Param{text("token"), text("time"), integer("id"), integer("limit")}
			eqp = append(eqp, "occurred_at=? AND id<?")
		case "older":
			params = []stmt.Param{text("token"), text("time"), integer("limit")}
			eqp = append(eqp, "occurred_at<?")
		}
		plan := stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "play_history_admin_user"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "account_key=?"}}
		addUOReadOctet(uoReadOctet{
			id:  stmt.ID("read.foreground.uo.activity.history." + variant + ".bytes"),
			sql: uoActivityHistoryOctetSQL(variant), label: "UO activity history bytes", owner: "store/read UO activity",
			params: params, plan: plan, table: "play_history", index: "play_history_admin_user",
			keys: []uoIndexKey{{"account_key", "BINARY", 0}, {"occurred_at", "BINARY", 1}, {"id", "BINARY", 1}},
			ddl:  "create index play_history_admin_user on play_history(account_key,occurred_at desc,id desc)",
			eqp:  eqp, eqpRows: 3,
		})
	}
}
