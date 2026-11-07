package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usInvitationShortSourceOctetsSQL = `SELECT octet_length(s.invitation_id),octet_length(s.lookup_hash),octet_length(s.binding_hash),octet_length(s.server_id),octet_length(s.sign_in_kind),octet_length(s.purpose) FROM access_invitation_short_codes AS s INDEXED BY sqlite_autoindex_access_invitation_short_codes_1 WHERE s.invitation_id=?`

func init() {
	pointOctets.add(octetShape{id: "us.invitation.short.source.octets", sql: usInvitationShortSourceOctetsSQL, mismatch: "US short invitation preflight requires the exact closed read definition", definition: usInvitationShortOctetDefinition, audit: func(ctx context.Context, c *sql.Conn) error {
		return auditClosedOctetKey(ctx, c, "access_invitation_short_codes", "sqlite_autoindex_access_invitation_short_codes_1", []string{"invitation_id"})
	}, point: func(rows Rows) error {
		if len(rows.Values) != 1 || len(rows.Values[0]) != 4 || rows.Values[0][3] != "SEARCH s USING INDEX sqlite_autoindex_access_invitation_short_codes_1 (invitation_id=?)" {
			return fmt.Errorf("US short invitation preflight: exact unique point required")
		}
		return nil
	}})
}
func usInvitationShortOctetDefinition(d stmt.Definition) bool {
	want := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitation_short_codes_1"}, Seeks: []string{"invitation_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitation_short_codes", Name: "sqlite_autoindex_access_invitation_short_codes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "invitation_id", Collation: "BINARY"}}}}}
	return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "invitation", Type: stmt.Text}}) && reflect.DeepEqual(d.Plan, want)
}
