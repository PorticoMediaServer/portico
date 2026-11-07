package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"slices"
	"strings"
)

const usRestrictionSourceOctetsSQL = `SELECT octet_length(r.profile_id),octet_length(r.rating_system),octet_length(r.maximum_age_rating),octet_length(r.blocked_labels) FROM profile_restrictions AS r INDEXED BY sqlite_autoindex_profile_restrictions_1 WHERE r.profile_id=?`

func init() {
	pointOctets.add(octetShape{id: "us.restrictions.source.octets", sql: usRestrictionSourceOctetsSQL, mismatch: "US restriction byte preflight requires the exact closed read definition", definition: usRestrictionOctetDefinition, audit: auditUSRestrictionOctetKey, point: func(rows Rows) error {
		if len(rows.Values) != 1 || len(rows.Values[0]) != 4 || rows.Values[0][3] != "SEARCH r USING INDEX sqlite_autoindex_profile_restrictions_1 (profile_id=?)" {
			return fmt.Errorf("US restriction preflight: exact unique point SEARCH required")
		}
		return nil
	}})
}
func usRestrictionOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 1 || d.Params[0] != (stmt.Param{Name: "profile", Type: stmt.Text}) || !slices.Equal(d.Plan.Indexes, []string{"sqlite_autoindex_profile_restrictions_1"}) || !slices.Equal(d.Plan.Seeks, []string{"profile_id=?"}) || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	b := d.Plan.AutoIndexes[0]
	return b.Table == "profile_restrictions" && b.Name == "sqlite_autoindex_profile_restrictions_1" && b.Origin == "pk" && slices.Equal(b.Columns, []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}})
}
func auditUSRestrictionOctetKey(ctx context.Context, c *sql.Conn) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	count := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("US restriction preflight: native function metadata")
		}
		name, ok := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !ok || !arityOK {
			return fmt.Errorf("US restriction preflight: native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("US restriction preflight: unshadowed native octet_length required")
			}
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("US restriction preflight: unshadowed native octet_length required")
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list(profile_restrictions)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("US restriction preflight: native index metadata")
		}
		if row[1] == "sqlite_autoindex_profile_restrictions_1" {
			if row[2] != int64(1) || row[3] != "pk" || row[4] != int64(0) {
				return fmt.Errorf("US restriction preflight: exact nonpartial PK required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("US restriction preflight: native PK missing")
	}
	info, err := query(ctx, c, "PRAGMA main.index_info(sqlite_autoindex_profile_restrictions_1)")
	if err != nil {
		return err
	}
	if len(info.Values) != 1 || len(info.Values[0]) != 3 || info.Values[0][2] != "profile_id" {
		return fmt.Errorf("US restriction preflight: exact native profile_id key required")
	}
	return nil
}

const usRestrictionTargetOctetsSQL = `SELECT octet_length(m.role) FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=m.account_id CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=m.account_id WHERE m.account_id=? AND m.incarnation=? AND m.revision=? AND a.epoch=? AND m.disabled=0 AND p.id=? AND p.lifetime=? AND p.revision=? AND p.deleted=0`

func init() {
	pointOctets.add(octetShape{id: "us.restrictions.target.octets", sql: usRestrictionTargetOctetsSQL, mismatch: "US restriction target preflight requires the exact closed read definition", definition: usRestrictionTargetOctetDefinition, audit: func(ctx context.Context, c *sql.Conn) error { return auditUSPolicyOctetKey(ctx, c, true) }, point: func(rows Rows) error {
		want := []string{"SEARCH m USING INDEX sqlite_autoindex_direct_memberships_1 (account_id=?)", "SEARCH a USING INDEX sqlite_autoindex_accounts_1 (id=?)", "SEARCH p USING INDEX sqlite_autoindex_direct_profiles_2 (account_id=? AND id=?)"}
		if len(rows.Values) != len(want) {
			return fmt.Errorf("US restriction target preflight: exact unique points required")
		}
		for n, row := range rows.Values {
			if len(row) != 4 || row[3] != want[n] {
				return fmt.Errorf("US restriction target preflight: exact unique points required")
			}
		}
		return nil
	}})
}
func usRestrictionTargetOctetDefinition(d stmt.Definition) bool {
	params := []stmt.Param{{Name: "account", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}, {Name: "epoch", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "lifetime", Type: stmt.Text}, {Name: "profile_revision", Type: stmt.Integer}}
	plan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=?", "id=?", "account_id=? AND id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}, {Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}, {Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}}}
	return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, plan)
}
