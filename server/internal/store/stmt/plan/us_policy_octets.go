package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usMemberPolicyOctetsSQL = `SELECT octet_length(m.role),octet_length(m.allowed_libraries) FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
WHERE m.account_id=? AND m.incarnation=?`
const usProfilePolicyOctetsSQL = `SELECT octet_length(m.role),octet_length(m.allowed_libraries),coalesce(octet_length(p.allowed_libraries),0) FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=m.account_id
WHERE m.account_id=? AND m.incarnation=? AND p.id=? AND p.lifetime=?`

func init() {
	pointOctets.add(octetShape{id: "us.policy.member.octets", sql: usMemberPolicyOctetsSQL,
		mismatch:   "US member byte preflight requires the exact closed read definition",
		definition: func(d stmt.Definition) bool { return usPolicyOctetDefinition(d, false) },
		audit:      func(ctx context.Context, c *sql.Conn) error { return auditUSPolicyOctetKey(ctx, c, false) },
		point:      func(rows Rows) error { return checkUSPolicyOctetPoint(rows, false) }})
	pointOctets.add(octetShape{id: "us.policy.profile.octets", sql: usProfilePolicyOctetsSQL,
		mismatch:   "US profile byte preflight requires the exact closed read definition",
		definition: func(d stmt.Definition) bool { return usPolicyOctetDefinition(d, true) },
		audit:      func(ctx context.Context, c *sql.Conn) error { return auditUSPolicyOctetKey(ctx, c, true) },
		point:      func(rows Rows) error { return checkUSPolicyOctetPoint(rows, true) }})
}

func usPolicyOctetDefinition(d stmt.Definition, profile bool) bool {
	params := []string{"account", "member_incarnation"}
	indexes := []string{"sqlite_autoindex_direct_memberships_1"}
	seeks := []string{"account_id=?"}
	pins := []stmt.AutoIndexBinding{{Table: "direct_memberships", Name: indexes[0], Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}}
	if profile {
		params = append(params, "profile", "profile_lifetime")
		indexes = append(indexes, "sqlite_autoindex_direct_profiles_2")
		seeks = append(seeks, "account_id=? AND id=?")
		pins = append(pins, stmt.AutoIndexBinding{Table: "direct_profiles", Name: indexes[1], Origin: "u", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}})
	}
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(params) || !slices.Equal(d.Plan.Indexes, indexes) || !slices.Equal(d.Plan.Seeks, seeks) ||
		len(d.Plan.AutoIndexes) != len(pins) || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for n, p := range d.Params {
		if p.Name != params[n] || p.Type != stmt.Text || p.Nullable {
			return false
		}
	}
	for n, pin := range d.Plan.AutoIndexes {
		want := pins[n]
		if pin.Table != want.Table || pin.Name != want.Name || pin.Origin != want.Origin || !slices.Equal(pin.Columns, want.Columns) {
			return false
		}
	}
	return true
}

func auditUSPolicyOctetKey(ctx context.Context, conn *sql.Conn, profile bool) error {
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("US preflight: unexpected native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("US preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("US preflight: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("US preflight: unshadowed native octet_length required")
	}
	keys := []struct {
		table, index, origin string
		columns              []string
	}{
		{"direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", []string{"account_id"}},
	}
	if profile {
		keys = append(keys, struct {
			table, index, origin string
			columns              []string
		}{"direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", []string{"account_id", "id"}})
	}
	for _, key := range keys {
		// These identifiers come only from the closed constants above.
		indexes, err := query(ctx, conn, "PRAGMA main.index_list("+key.table+")")
		if err != nil {
			return err
		}
		found := false
		for _, row := range indexes.Values {
			if len(row) != 5 {
				return fmt.Errorf("US preflight: unexpected native index metadata")
			}
			if row[1] == key.index {
				if row[2] != int64(1) || row[3] != key.origin || row[4] != int64(0) {
					return fmt.Errorf("US preflight: exact nonpartial unique key required")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("US preflight: pinned native unique key missing")
		}
		info, err := query(ctx, conn, "PRAGMA main.index_info("+key.index+")")
		if err != nil {
			return err
		}
		if len(info.Values) != len(key.columns) {
			return fmt.Errorf("US preflight: native key width changed")
		}
		for n, column := range key.columns {
			if len(info.Values[n]) != 3 || info.Values[n][2] != column {
				return fmt.Errorf("US preflight: native key column changed")
			}
		}
	}
	return nil
}

func checkUSPolicyOctetPoint(eqp Rows, profile bool) error {
	want := []string{"SEARCH m USING INDEX sqlite_autoindex_direct_memberships_1 (account_id=?)"}
	if profile {
		want = append(want, "SEARCH p USING INDEX sqlite_autoindex_direct_profiles_2 (account_id=? AND id=?)")
	}
	if len(eqp.Values) != len(want) {
		return fmt.Errorf("US preflight: exact unique point SEARCH required")
	}
	for n, row := range eqp.Values {
		if len(row) != 4 || row[3] != want[n] {
			return fmt.Errorf("US preflight: exact unique point SEARCH required")
		}
	}
	return nil
}
