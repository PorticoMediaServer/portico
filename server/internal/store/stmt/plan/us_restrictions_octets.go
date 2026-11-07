package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
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
