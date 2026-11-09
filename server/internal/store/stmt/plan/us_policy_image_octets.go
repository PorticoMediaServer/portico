package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed octet points for US-07's private 541 caller-snapshot loader. Each point
// returns INTEGER octet/count facts only; the loader charges the funded frame
// grant from them before any TEXT/BLOB Scan, JSON decode or DTO allocation.
const (
	usPolicyImageAccountOctetsSQL = `SELECT octet_length(a.username),octet_length(a.profile_id),octet_length(m.role),octet_length(m.allowed_libraries),m.revision,m.disabled
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`
	usPolicyImageFamilyOctetsSQL = `SELECT octet_length(f.role),octet_length(f.remember_until),octet_length(f.manage_until),octet_length(p.pin_hash),p.is_primary
FROM authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=f.account_id
WHERE f.id=? AND f.account_id=? AND p.id=?`
	usPolicyImagePolicyOctetsSQL = `SELECT octet_length(p.require_two_step),p.pin_remember_days,octet_length(s.sign_in_kind)
FROM identity_sign_in_policy AS p
CROSS JOIN identity_state AS s ON s.singleton=1
WHERE p.singleton=1`
	usPolicyImageHostedOctetsSQL    = `SELECT octet_length(hosted_account_id) FROM account_portico_links WHERE account_id=?`
	usPolicyImageCustodianOctetsSQL = `SELECT octet_length(custodian) FROM hosted_membership_sync WHERE singleton=1`
	usPolicyImageProfilesOctetsSQL  = `SELECT octet_length(p.id),octet_length(p.name),octet_length(p.art),coalesce(octet_length(p.allowed_libraries),0),p.position
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`
)

// usPolicyImageOctetShape pins the exact writer definition shape. A changed SQL
// string, parameter or plan declaration is a mismatch, never a silent reuse.
type usPolicyImageOctetShape struct {
	params      []string
	indexes     []string
	seeks       []string
	primaryKeys []string
	pins        []stmt.AutoIndexBinding
	tables      []string
	profiles    bool
	point       func(Rows) error
}

func init() {
	account := usPolicyImageOctetShape{
		params:  []string{"account"},
		indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1"},
		seeks:   []string{"id=?", "account_id=?"},
		pins: []stmt.AutoIndexBinding{
			usPolicyImagePin("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			usPolicyImagePin("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		},
		tables: []string{"accounts", "direct_memberships"},
		point:  usPolicyImagePoint("sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1"),
	}
	family := usPolicyImageOctetShape{
		params:  []string{"family", "account", "profile"},
		indexes: []string{"sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_direct_profiles_2"},
		seeks:   []string{"id=?", "account_id=? AND id=?"},
		pins: []stmt.AutoIndexBinding{
			usPolicyImagePin("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
			usPolicyImagePin("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		},
		tables: []string{"authorization_session_families", "direct_profiles"},
		point:  usPolicyImagePoint("sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_direct_profiles_2"),
	}
	policy := usPolicyImageOctetShape{
		primaryKeys: []string{"identity_sign_in_policy", "identity_state"},
		seeks:       []string{"rowid=?"},
		tables:      []string{"identity_sign_in_policy", "identity_state"},
		point:       usPolicyImagePoint(),
	}
	hosted := usPolicyImageOctetShape{
		params: []string{"account"},
		seeks:  []string{"account_id=?"},
		tables: []string{"account_portico_links"},
		point:  usPolicyImagePoint(),
	}
	custodian := usPolicyImageOctetShape{
		seeks:  []string{"rowid=?"},
		tables: []string{"hosted_membership_sync"},
		point:  usPolicyImagePoint(),
	}
	profiles := usPolicyImageOctetShape{
		params:   []string{"account", "row_limit"},
		indexes:  []string{"direct_profiles_account"},
		seeks:    []string{"account_id=? AND deleted=?"},
		tables:   []string{"direct_profiles"},
		profiles: true,
		point:    usPolicyImageProfilesPoint,
	}
	for id, entry := range map[stmt.ID]struct {
		sql   string
		shape usPolicyImageOctetShape
	}{
		"us.policy.image.account_octets":   {usPolicyImageAccountOctetsSQL, account},
		"us.policy.image.family_octets":    {usPolicyImageFamilyOctetsSQL, family},
		"us.policy.image.policy_octets":    {usPolicyImagePolicyOctetsSQL, policy},
		"us.policy.image.hosted_octets":    {usPolicyImageHostedOctetsSQL, hosted},
		"us.policy.image.custodian_octets": {usPolicyImageCustodianOctetsSQL, custodian},
		"us.policy.image.profiles_octets":  {usPolicyImageProfilesOctetsSQL, profiles},
	} {
		shape := entry.shape
		sqlText := entry.sql
		pointOctets.add(octetShape{
			id:       id,
			sql:      sqlText,
			mismatch: "US policy image preflight requires the exact closed octet definition",
			definition: func(d stmt.Definition) bool {
				return usPolicyImageOctetDefinition(d, sqlText, shape)
			},
			audit: func(ctx context.Context, conn *sql.Conn) error {
				return auditUSPolicyImageOctet(ctx, conn, shape)
			},
			point: shape.point,
		})
	}
}

func usPolicyImagePin(table, index, origin string, columns ...string) stmt.AutoIndexBinding {
	binding := stmt.AutoIndexBinding{Table: table, Name: index, Origin: origin}
	for _, column := range columns {
		binding.Columns = append(binding.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return binding
}

func usPolicyImageOctetDefinition(d stmt.Definition, sqlText string, shape usPolicyImageOctetShape) bool {
	if d.SQL != sqlText || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 ||
		d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || d.Plan.NativeRead != nil || len(d.Cohorts) == 0 {
		return false
	}
	if !slices.Equal(d.Plan.Indexes, shape.indexes) || !slices.Equal(d.Plan.Seeks, shape.seeks) || !slices.Equal(d.Plan.PrimaryKeys, shape.primaryKeys) {
		return false
	}
	if len(d.Params) != len(shape.params) {
		return false
	}
	for n, name := range shape.params {
		param := d.Params[n]
		if param.Name != name || param.Nullable {
			return false
		}
		if name == "row_limit" {
			if param.Type != stmt.Integer {
				return false
			}
		} else if param.Type != stmt.Text {
			return false
		}
	}
	if len(d.Plan.AutoIndexes) != len(shape.pins) {
		return false
	}
	for n, want := range shape.pins {
		got := d.Plan.AutoIndexes[n]
		if got.Table != want.Table || got.Name != want.Name || got.Origin != want.Origin || !slices.Equal(got.Columns, want.Columns) {
			return false
		}
	}
	return true
}

// auditUSPolicyImageOctet proves the builtin octet_length is unshadowed and that
// each named key is the ordinary MAIN table/index the preflight selects.
func auditUSPolicyImageOctet(ctx context.Context, conn *sql.Conn, shape usPolicyImageOctetShape) error {
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("US policy image preflight: invalid native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("US policy image preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("US policy image preflight: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("US policy image preflight: unshadowed native octet_length required")
	}
	for _, table := range shape.tables {
		tables, err := query(ctx, conn, "PRAGMA main.table_list("+identifier(table)+")")
		if err != nil {
			return err
		}
		if len(tables.Values) != 1 || len(tables.Values[0]) != 6 ||
			tables.Values[0][0] != "main" || tables.Values[0][1] != table || tables.Values[0][2] != "table" {
			return fmt.Errorf("US policy image preflight: ordinary MAIN table required")
		}
	}
	if shape.profiles {
		indexes, err := query(ctx, conn, "PRAGMA main.index_list(direct_profiles)")
		if err != nil {
			return err
		}
		found := false
		for _, row := range indexes.Values {
			if len(row) != 5 {
				return fmt.Errorf("US policy image preflight: invalid native index metadata")
			}
			if row[1] == "direct_profiles_account" {
				if row[2] != int64(0) || row[3] != "c" || row[4] != int64(0) {
					return fmt.Errorf("US policy image preflight: exact nonpartial live profile index required")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("US policy image preflight: live profile index absent")
		}
		info, err := query(ctx, conn, "PRAGMA main.index_info(direct_profiles_account)")
		if err != nil {
			return err
		}
		columns := []string{"account_id", "deleted", "position", "id"}
		if len(info.Values) != len(columns) {
			return fmt.Errorf("US policy image preflight: live profile index width changed")
		}
		for n, column := range columns {
			if len(info.Values[n]) != 3 || info.Values[n][0] != int64(n) || info.Values[n][2] != column {
				return fmt.Errorf("US policy image preflight: live profile index column changed")
			}
		}
	}
	return nil
}

// usPolicyImagePoint requires a SEARCH-only point for each named index. An empty
// list still requires every EQP row to be a SEARCH (no table scan).
func usPolicyImagePoint(indexes ...string) func(Rows) error {
	return func(eqp Rows) error {
		if len(eqp.Values) == 0 {
			return fmt.Errorf("US policy image preflight: INTEGER point required")
		}
		for _, row := range eqp.Values {
			if len(row) != 4 {
				return fmt.Errorf("US policy image preflight: invalid EQP shape")
			}
			detail, ok := row[3].(string)
			if !ok || !strings.HasPrefix(strings.ToUpper(detail), "SEARCH ") {
				return fmt.Errorf("US policy image preflight: SEARCH-only point required")
			}
			if strings.Contains(strings.ToUpper(detail), "TEMP B-TREE") {
				return fmt.Errorf("US policy image preflight: no temporary sort")
			}
		}
		for _, index := range indexes {
			found := false
			for _, row := range eqp.Values {
				if detail, ok := row[3].(string); ok && strings.Contains(detail, index) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("US policy image preflight: pinned index %s not used", index)
			}
		}
		return nil
	}
}

func usPolicyImageProfilesPoint(eqp Rows) error {
	if len(eqp.Values) != 1 {
		return fmt.Errorf("US policy image preflight: one ordered index point required")
	}
	row := eqp.Values[0]
	if len(row) != 4 {
		return fmt.Errorf("US policy image preflight: invalid EQP shape")
	}
	detail, ok := row[3].(string)
	if !ok || !strings.Contains(detail, "USING INDEX direct_profiles_account") || !strings.HasPrefix(strings.ToUpper(detail), "SEARCH ") {
		return fmt.Errorf("US policy image preflight: live profile index point required")
	}
	if strings.Contains(strings.ToUpper(detail), "TEMP B-TREE") {
		return fmt.Errorf("US policy image preflight: ordered index must not sort")
	}
	return nil
}
