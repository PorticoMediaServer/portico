package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The three closed UO flag-membership byte ranges. Each flag owns its own fixed
// sparse statement and its own partial index; no caller-selected column,
// predicate, identifier or SQL function participates.
type uoMembershipOctet struct {
	name, column, index string
}

var uoMembershipOctets = []uoMembershipOctet{
	{name: "watchlist", column: "watchlisted", index: "personal_current_watchlist"},
	{name: "favorite", column: "favorite", index: "personal_current_favorite"},
	{name: "not-interested", column: "not_interested", index: "personal_current_not_interested"},
}

func uoMembershipOctetSQL(m uoMembershipOctet, tail, lengths bool) string {
	columns := "p.item_public_id,e.id,'',e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind"
	if lengths {
		columns = "p.item_public_id,e.id,octet_length(e.title)"
	}
	seek := ""
	if tail {
		seek = " AND p.item_public_id>?"
	}
	return "SELECT " + columns + `
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_items AS p INDEXED BY ` + m.index + ` ON p.profile_id=f.profile_id
LEFT JOIN catalog_entities AS e ON e.id=p.item_id
WHERE t.token_hash=? AND ` + m.column + `=1` + seek + `
ORDER BY p.item_public_id ASC LIMIT 64`
}

func init() {
	for _, m := range uoMembershipOctets {
		for _, tail := range []bool{false, true} {
			variant := "first"
			if tail {
				variant = "tail"
			}
			params := []stmt.Param{{Name: "token", Type: stmt.Text}}
			seek := "profile_id=?"
			if tail {
				params = append(params, stmt.Param{Name: "after", Type: stmt.Blob})
				seek = "profile_id=? AND item_public_id>?"
			}
			token := stmt.AutoIndexBinding{Table: "authorization_family_tokens", Name: "sqlite_autoindex_authorization_family_tokens_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "token_hash", Collation: "BINARY"}}}
			family := stmt.AutoIndexBinding{Table: "authorization_session_families", Name: "sqlite_autoindex_authorization_session_families_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
			name := "foreground.uo.membership." + m.name + "." + variant
			id := stmt.ID("read." + name + ".bytes")
			sqlText := uoMembershipOctetSQL(m, tail, true)
			proof := stmt.Plan{
				Hot: true, PrimaryKeys: []string{"e"},
				Indexes:     []string{token.Name, family.Name, m.index},
				AutoIndexes: []stmt.AutoIndexBinding{token, family},
				Seeks:       []string{"token_hash=?", "id=?", seek},
			}
			membership := m
			pointOctets.add(octetShape{
				id: id, sql: sqlText, mismatch: "exact bounded UO membership byte range required",
				definition: func(d stmt.Definition) bool {
					return d.ID == id && d.SQL == sqlText && d.Owner == "store/read UO membership" && d.Kind == stmt.Read &&
						reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
				},
				audit: func(ctx context.Context, c *sql.Conn) error {
					return auditUOMembershipIndex(ctx, c, membership.index, membership.column)
				},
				point: func(rows Rows) error { return uoMembershipOctetPoint(rows, membership.index, tail) },
			})
		}
	}
}

// uoMembershipOctetPoint requires the exact token/family derivation, the sparse
// partial-index SEARCH and the entity point probe. A plan that cannot use the
// partial index (a scan, another index, a temporary B-tree for the public-ID
// order or a missing raw keyset seek) is refused.
func uoMembershipOctetPoint(rows Rows, index string, tail bool) error {
	if len(rows.Values) != 4 {
		return fmt.Errorf("UO membership bytes require the token, family, sparse seek and entity point")
	}
	token, family, raw, entity := false, false, false, false
	for _, row := range rows.Values {
		if len(row) != 4 {
			return fmt.Errorf("UO membership bytes: invalid EQP")
		}
		detail, ok := row[3].(string)
		if !ok {
			return fmt.Errorf("UO membership bytes: invalid EQP")
		}
		if strings.Contains(detail, "TEMP B-TREE") || strings.HasPrefix(detail, "SCAN ") {
			return fmt.Errorf("UO membership bytes require a sparse indexed SEARCH")
		}
		switch {
		case strings.HasPrefix(detail, "SEARCH t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)"):
			token = true
		case strings.HasPrefix(detail, "SEARCH f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)"):
			family = true
		case strings.HasPrefix(detail, "SEARCH p USING INDEX "+index+" ("):
			if tail && !strings.Contains(detail, "profile_id=? AND item_public_id>?") {
				return fmt.Errorf("UO membership bytes require the public-ID keyset range")
			}
			if !tail && !strings.Contains(detail, "profile_id=?") {
				return fmt.Errorf("UO membership bytes require the own-profile seek")
			}
			raw = true
		case strings.HasPrefix(detail, "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)"):
			entity = true
		default:
			return fmt.Errorf("UO membership bytes: unexpected access %s", detail)
		}
	}
	if !token || !family || !raw || !entity {
		return fmt.Errorf("UO membership bytes require the closed bounded point plan")
	}
	return nil
}

// auditUOMembershipIndex proves the installed partial index has its exact
// column order, BINARY collations and exact (single-conjunct) predicate. A
// missing, non-partial, differently keyed or wrong-predicate index is refused.
func auditUOMembershipIndex(ctx context.Context, c *sql.Conn, index, column string) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("UO membership bytes: invalid native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("UO membership bytes: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("UO membership bytes: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("UO membership bytes: unshadowed native octet_length required")
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list(personal_items)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("UO membership bytes: invalid native index metadata")
		}
		if row[1] != index {
			continue
		}
		if row[2] != int64(0) || row[3] != "c" || row[4] != int64(1) {
			return fmt.Errorf("UO membership bytes require one ordinary partial sparse index")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("UO membership sparse index absent")
	}
	keys, err := query(ctx, c, "PRAGMA main.index_xinfo("+identifier(index)+")")
	if err != nil {
		return err
	}
	names := []string{"profile_id", "item_public_id"}
	n := 0
	for _, row := range keys.Values {
		if len(row) < 6 {
			return fmt.Errorf("UO membership bytes: invalid native index metadata")
		}
		if row[5] != int64(1) {
			continue
		}
		if n >= len(names) || row[2] != names[n] || row[3] != int64(0) || row[4] != "BINARY" {
			return fmt.Errorf("UO membership sparse index key drifted")
		}
		n++
	}
	if n != len(names) {
		return fmt.Errorf("UO membership sparse index key incomplete")
	}
	ddl, err := query(ctx, c, "SELECT sql FROM main.sqlite_schema WHERE type='index' AND name=?", index)
	if err != nil {
		return err
	}
	if len(ddl.Values) != 1 || len(ddl.Values[0]) != 1 {
		return fmt.Errorf("UO membership sparse index DDL absent")
	}
	text, ok := ddl.Values[0][0].(string)
	if !ok || !uoMembershipIndexDDL(text, index, column) {
		return fmt.Errorf("UO membership sparse index predicate drifted")
	}
	return nil
}

func uoMembershipIndexDDL(text, index, column string) bool {
	want := "create index " + index + " on personal_items(profile_id,item_public_id) where " + column + "=1"
	got := strings.ToLower(strings.TrimSuffix(strings.Join(strings.Fields(text), " "), ";"))
	return got == want
}
