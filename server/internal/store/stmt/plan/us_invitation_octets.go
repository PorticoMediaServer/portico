package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact octet preflight registrations for the 550-552 invitation source and
// retained-result reads. The writer definitions own the exact SQL; these two
// closed shapes let Capture grammar-check the byte points and audit the native
// key/EQP. No entry grants SQL, a function allowlist or caller-supplied text.
const (
	usInvitationSourceOctetsSQL = `SELECT octet_length(code_hash),octet_length(role),octet_length(allowed_libraries),octet_length(state),octet_length(created_by),coalesce(octet_length(account_id),0),octet_length(bound_username),octet_length(max_streams_mode),octet_length(max_content_rating),coalesce(octet_length(notes),0)
FROM access_invitations INDEXED BY sqlite_autoindex_access_invitations_1 WHERE id=?`

	usInvitationReceiptOctetsSQL = `SELECT octet_length(invitation_id),octet_length(server_id),coalesce(octet_length(notes),0),octet_length(role),octet_length(state),coalesce(octet_length(account_id),0),octet_length(bound_username),octet_length(max_streams_mode),octet_length(max_content_rating),octet_length(source_code_hash),variable_bytes
FROM security_invitation_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`
)

type usInvitationOctetShape struct {
	params      []stmt.Param
	indexes     []string
	primaryKeys []string
	seeks       []string
}

func init() {
	source := usInvitationOctetShape{
		params:  []stmt.Param{{Name: "id", Type: stmt.Text}},
		indexes: []string{"sqlite_autoindex_access_invitations_1"},
		seeks:   []string{"id=?"},
	}
	receipt := usInvitationOctetShape{
		params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
			{Name: "sequence", Type: stmt.Integer}},
		primaryKeys: []string{"security_invitation_results"},
		seeks:       []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"},
	}
	for id, entry := range map[stmt.ID]struct {
		sql   string
		shape usInvitationOctetShape
		table string
		index string
		keys  []string
		want  string
	}{
		"us.invitation.source.octets": {usInvitationSourceOctetsSQL, source, "access_invitations",
			"sqlite_autoindex_access_invitations_1", []string{"id"},
			"SEARCH access_invitations USING INDEX sqlite_autoindex_access_invitations_1 (id=?)"},
		"us.invitation.receipt.octets": {usInvitationReceiptOctetsSQL, receipt, "security_invitation_results", "",
			[]string{"owner_id", "command_incarnation", "command_sequence"},
			"SEARCH security_invitation_results USING PRIMARY KEY (owner_id=? AND command_incarnation=? AND command_sequence=?)"},
	} {
		shape := entry.shape
		sqlText := entry.sql
		table, index, keys, want := entry.table, entry.index, entry.keys, entry.want
		pointOctets.add(octetShape{
			id:       id,
			sql:      sqlText,
			mismatch: "US invitation byte preflight requires the exact closed point read",
			definition: func(d stmt.Definition) bool {
				return usInvitationOctetDefinition(d, sqlText, shape)
			},
			audit: func(ctx context.Context, conn *sql.Conn) error {
				return auditClosedOctetKey(ctx, conn, table, index, keys)
			},
			point: func(eqp Rows) error {
				if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 {
					return fmt.Errorf("US invitation preflight: exact point SEARCH required")
				}
				detail, ok := eqp.Values[0][3].(string)
				if !ok || !strings.EqualFold(detail, want) {
					return fmt.Errorf("US invitation preflight: exact point SEARCH required")
				}
				return nil
			},
		})
	}
}

func usInvitationOctetDefinition(d stmt.Definition, sqlText string, shape usInvitationOctetShape) bool {
	if d.SQL != sqlText || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 ||
		d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || d.Plan.NativeRead != nil || len(d.Cohorts) == 0 {
		return false
	}
	if !slices.Equal(d.Plan.Indexes, shape.indexes) || !slices.Equal(d.Plan.Seeks, shape.seeks) ||
		!slices.Equal(d.Plan.PrimaryKeys, shape.primaryKeys) {
		return false
	}
	if len(d.Params) != len(shape.params) {
		return false
	}
	for n, want := range shape.params {
		got := d.Params[n]
		if got.Name != want.Name || got.Type != want.Type || got.Nullable {
			return false
		}
	}
	return true
}
