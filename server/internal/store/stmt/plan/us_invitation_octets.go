package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"slices"
	"strings"
)

const usInvitationSourceOctetsSQL = `SELECT octet_length(i.id),coalesce(octet_length(i.notes),0),octet_length(i.code_hash),octet_length(i.role),octet_length(i.allowed_libraries),octet_length(i.state),coalesce(octet_length(i.account_id),0),octet_length(i.bound_username),octet_length(i.max_streams_mode),octet_length(i.max_content_rating) FROM access_invitations AS i INDEXED BY sqlite_autoindex_access_invitations_1 WHERE i.id=? AND i.revision=?`

func init() {
	pointOctets.add(octetShape{id: "us.invitation.source.octets", sql: usInvitationSourceOctetsSQL, mismatch: "US invitation byte preflight requires the exact closed read definition", definition: usInvitationOctetDefinition, audit: auditUSInvitationOctetKey, point: func(rows Rows) error {
		if len(rows.Values) != 1 || len(rows.Values[0]) != 4 || rows.Values[0][3] != "SEARCH i USING INDEX sqlite_autoindex_access_invitations_1 (id=?)" {
			return fmt.Errorf("US invitation preflight: exact unique point SEARCH required")
		}
		return nil
	}})
}
func usInvitationOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || d.Params[0] != (stmt.Param{Name: "invitation", Type: stmt.Text}) || d.Params[1] != (stmt.Param{Name: "revision", Type: stmt.Integer}) || !slices.Equal(d.Plan.Indexes, []string{"sqlite_autoindex_access_invitations_1"}) || !slices.Equal(d.Plan.Seeks, []string{"id=?"}) || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	b := d.Plan.AutoIndexes[0]
	return b.Table == "access_invitations" && b.Name == "sqlite_autoindex_access_invitations_1" && b.Origin == "pk" && slices.Equal(b.Columns, []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}})
}
func auditUSInvitationOctetKey(ctx context.Context, c *sql.Conn) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	count := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("US invitation preflight: native function metadata")
		}
		name, ok := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !ok || !arityOK {
			return fmt.Errorf("US invitation preflight: native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("US invitation preflight: unshadowed native octet_length required")
			}
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("US invitation preflight: unshadowed native octet_length required")
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list(access_invitations)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("US invitation preflight: native index metadata")
		}
		if row[1] == "sqlite_autoindex_access_invitations_1" {
			if row[2] != int64(1) || row[3] != "pk" || row[4] != int64(0) {
				return fmt.Errorf("US invitation preflight: exact nonpartial PK required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("US invitation preflight: native PK missing")
	}
	info, err := query(ctx, c, "PRAGMA main.index_info(sqlite_autoindex_access_invitations_1)")
	if err != nil {
		return err
	}
	if len(info.Values) != 1 || len(info.Values[0]) != 3 || info.Values[0][2] != "id" {
		return fmt.Errorf("US invitation preflight: exact native id key required")
	}
	return nil
}
