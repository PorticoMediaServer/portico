package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact octet preflight registrations for the US security read surface. Both
// the length point and its payload remain ordinary registered reads: Capture
// still audits the exact SQL, native keys, statistics and EQP before a receipt.
// No entry grants SQL, a function allowlist or caller-supplied text.
const usSecurityStatusOctetsSQL = `SELECT octet_length(f.id),octet_length(f.server_id),octet_length(f.account_id),octet_length(f.profile_id),octet_length(f.role),octet_length(f.authorization_horizon),octet_length(f.remember_until),octet_length(f.manage_until),octet_length(t.expires_at),octet_length(p.name),octet_length(p.art),octet_length(p.allowed_libraries),octet_length(d.id),octet_length(a.username),octet_length(d.name),octet_length(d.platform),octet_length(d.app),octet_length(d.app_version),octet_length(d.first_seen),octet_length(d.last_seen),octet_length(d.last_profile_id)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=f.account_id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.id=f.profile_id AND p.account_id=a.id
CROSS JOIN identity_devices AS d INDEXED BY sqlite_autoindex_identity_devices_1 ON d.id=f.device_id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0
AND f.authority='local' AND f.security_kind=0 AND d.authority='local' AND d.account_id=a.id
AND d.installation_id=? AND d.retired_ms=0 AND p.deleted=0`

const usSecurityMemberOctetsSQL = `SELECT octet_length(a.id),octet_length(a.username),octet_length(a.email),octet_length(a.display_name),octet_length(a.profile_id),octet_length(m.role),octet_length(m.disabled_reason),octet_length(m.allowed_libraries),octet_length(m.notes),octet_length(m.joined_via)
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`

const usSecurityMembersOctetsSQL = `SELECT octet_length(a.id),octet_length(a.username),octet_length(a.email),octet_length(a.display_name),octet_length(a.profile_id),octet_length(m.role),octet_length(m.disabled_reason),octet_length(m.allowed_libraries),octet_length(m.notes),octet_length(m.joined_via)
FROM accounts AS a
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id>? ORDER BY a.id LIMIT ?`

const usSecurityProfilesOctetsSQL = `SELECT octet_length(p.id),octet_length(p.name),octet_length(p.art),octet_length(p.allowed_libraries)
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`

const usSecurityDevicesOctetsSQL = `SELECT octet_length(d.id),octet_length(d.account_id),octet_length(a.username),octet_length(d.last_profile_id),octet_length(d.name),octet_length(d.platform),octet_length(d.app),octet_length(d.app_version),octet_length(d.first_seen),octet_length(d.last_seen)
FROM identity_devices AS d INDEXED BY identity_devices_people_activity
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=d.account_id
WHERE d.account_id=? AND d.authority='local' AND d.retired_ms=0 ORDER BY d.platform,d.last_seen DESC LIMIT ?`

func init() {
	pointOctets.add(octetShape{
		id: "read.us.security.status_octets", sql: usSecurityStatusOctetsSQL,
		mismatch:   "US status byte preflight requires the exact closed read definition",
		definition: func(d stmt.Definition) bool { return usSecurityOctetDefinition(d, "status") },
		audit: func(ctx context.Context, c *sql.Conn) error {
			for _, item := range []struct{ table, index, column string }{
				{"authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash"},
				{"authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id"},
				{"accounts", "sqlite_autoindex_accounts_1", "id"},
				{"identity_devices", "sqlite_autoindex_identity_devices_1", "id"},
			} {
				if err := auditClosedOctetKey(ctx, c, item.table, item.index, []string{item.column}); err != nil {
					return err
				}
			}
			return auditUSSecurityUniqueKey(ctx, c, "direct_profiles", "sqlite_autoindex_direct_profiles_2", []string{"account_id", "id"})
		},
		point: func(eqp Rows) error {
			return checkUSSecurityOctetPoint(eqp,
				"SEARCH t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)",
				"SEARCH f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)",
				"SEARCH a USING INDEX sqlite_autoindex_accounts_1 (id=?)",
				"SEARCH p USING INDEX sqlite_autoindex_direct_profiles_2 (account_id=? AND id=?)",
				"SEARCH d USING INDEX sqlite_autoindex_identity_devices_1 (id=?)")
		},
	})
	pointOctets.add(octetShape{
		id: "read.us.security.member_octets", sql: usSecurityMemberOctetsSQL,
		mismatch:   "US member byte point requires the exact closed account/membership read",
		definition: func(d stmt.Definition) bool { return usSecurityOctetDefinition(d, "member") },
		audit: func(ctx context.Context, c *sql.Conn) error {
			if err := auditClosedOctetKey(ctx, c, "accounts", "sqlite_autoindex_accounts_1", []string{"id"}); err != nil {
				return err
			}
			return auditClosedOctetKey(ctx, c, "direct_memberships", "sqlite_autoindex_direct_memberships_1", []string{"account_id"})
		},
		point: func(eqp Rows) error {
			return checkUSSecurityOctetPoint(eqp,
				"SEARCH a USING INDEX sqlite_autoindex_accounts_1 (id=?)",
				"SEARCH m USING INDEX sqlite_autoindex_direct_memberships_1 (account_id=?)")
		},
	})
	pointOctets.add(octetShape{
		id: "read.us.security.members_octets", sql: usSecurityMembersOctetsSQL,
		mismatch:   "US member page byte preflight requires the exact closed keyset read",
		definition: func(d stmt.Definition) bool { return usSecurityOctetDefinition(d, "members") },
		audit: func(ctx context.Context, c *sql.Conn) error {
			if err := auditClosedOctetKey(ctx, c, "accounts", "sqlite_autoindex_accounts_1", []string{"id"}); err != nil {
				return err
			}
			return auditClosedOctetKey(ctx, c, "direct_memberships", "sqlite_autoindex_direct_memberships_1", []string{"account_id"})
		},
		point: func(eqp Rows) error {
			return checkUSSecurityOctetPoint(eqp,
				"SEARCH a USING INDEX sqlite_autoindex_accounts_1 (id>?)",
				"SEARCH m USING INDEX sqlite_autoindex_direct_memberships_1 (account_id=?)")
		},
	})
	pointOctets.add(octetShape{
		id: "read.us.security.profiles_octets", sql: usSecurityProfilesOctetsSQL,
		mismatch:   "US profile page byte preflight requires the exact closed position,id read",
		definition: func(d stmt.Definition) bool { return usSecurityOctetDefinition(d, "profiles") },
		audit: func(ctx context.Context, c *sql.Conn) error {
			return auditUSSecurityNamedIndex(ctx, c, "direct_profiles", "direct_profiles_account",
				[]string{"account_id", "deleted", "position", "id"}, []bool{false, false, false, false})
		},
		point: func(eqp Rows) error {
			return checkUSSecurityOctetPoint(eqp, "SEARCH p USING INDEX direct_profiles_account (account_id=? AND deleted=?)")
		},
	})
	pointOctets.add(octetShape{
		id: "read.us.security.devices_octets", sql: usSecurityDevicesOctetsSQL,
		mismatch:   "US device page byte preflight requires the exact closed people-activity read",
		definition: func(d stmt.Definition) bool { return usSecurityOctetDefinition(d, "devices") },
		audit: func(ctx context.Context, c *sql.Conn) error {
			if err := auditUSSecurityNamedIndex(ctx, c, "identity_devices", "identity_devices_people_activity",
				[]string{"account_id", "authority", "platform", "last_seen"}, []bool{false, false, false, true}); err != nil {
				return err
			}
			return auditClosedOctetKey(ctx, c, "accounts", "sqlite_autoindex_accounts_1", []string{"id"})
		},
		point: func(eqp Rows) error {
			return checkUSSecurityOctetPoint(eqp,
				"SEARCH d USING INDEX identity_devices_people_activity (account_id=? AND authority=?)",
				"SEARCH a USING INDEX sqlite_autoindex_accounts_1 (id=?)")
		},
	})
}

func usSecurityOctetDefinition(d stmt.Definition, kind string) bool {
	var sqlText string
	var params []stmt.Param
	var plan stmt.Plan
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		binding := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, column := range columns {
			binding.Columns = append(binding.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
		}
		return binding
	}
	token := key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "pk", "token_hash")
	family := key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id")
	account := key("accounts", "sqlite_autoindex_accounts_1", "pk", "id")
	member := key("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id")
	profile := key("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id")
	device := key("identity_devices", "sqlite_autoindex_identity_devices_1", "pk", "id")
	switch kind {
	case "status":
		sqlText = usSecurityStatusOctetsSQL
		params = []stmt.Param{text("token"), text("installation")}
		plan = stmt.Plan{Hot: true,
			Indexes:     []string{token.Name, family.Name, account.Name, profile.Name, device.Name},
			Seeks:       []string{"token_hash=?", "id=?", "account_id=? AND id=?"},
			AutoIndexes: []stmt.AutoIndexBinding{token, family, account, profile, device}}
	case "member":
		sqlText = usSecurityMemberOctetsSQL
		params = []stmt.Param{text("account")}
		plan = stmt.Plan{Hot: true, Indexes: []string{account.Name, member.Name},
			Seeks: []string{"id=?", "account_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{account, member}}
	case "members":
		sqlText = usSecurityMembersOctetsSQL
		params = []stmt.Param{text("after"), integer("limit")}
		plan = stmt.Plan{Hot: true, Indexes: []string{account.Name, member.Name},
			Seeks: []string{"id>?", "account_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{member}}
	case "profiles":
		sqlText = usSecurityProfilesOctetsSQL
		params = []stmt.Param{text("account"), integer("limit")}
		plan = stmt.Plan{Hot: true, Indexes: []string{"direct_profiles_account"}, Seeks: []string{"account_id=? AND deleted=?"}}
	case "devices":
		sqlText = usSecurityDevicesOctetsSQL
		params = []stmt.Param{text("account"), integer("limit")}
		plan = stmt.Plan{Hot: true, Indexes: []string{"identity_devices_people_activity", account.Name},
			Seeks: []string{"account_id=? AND authority=?", "id=?"}, AutoIndexes: []stmt.AutoIndexBinding{account}}
	default:
		return false
	}
	if d.SQL != sqlText || d.Kind != stmt.Read || d.Owner != "store/read US security read surface" ||
		len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 ||
		d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || d.Plan.NativeRead != nil {
		return false
	}
	for _, param := range params {
		if param.Nullable {
			return false
		}
	}
	return reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, plan)
}

func checkUSSecurityOctetPoint(eqp Rows, wants ...string) error {
	if len(eqp.Values) != len(wants) {
		return fmt.Errorf("US security preflight: exact point SEARCH sequence required")
	}
	for i, want := range wants {
		row := eqp.Values[i]
		if len(row) != 4 || row[3] != want {
			return fmt.Errorf("US security preflight: exact point SEARCH sequence required")
		}
	}
	return nil
}

// The shared closed-key audit covers generated PRIMARY KEY points only. The
// status profile key and the two page paths are UNIQUE/named keys, audited here
// with the same native metadata and no inferred or caller-supplied names.
func auditUSSecurityUniqueKey(ctx context.Context, conn *sql.Conn, table, index string, columns []string) error {
	if err := auditUSSecurityOctetBuiltin(ctx, conn); err != nil {
		return err
	}
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("US security preflight: unexpected native index metadata")
		}
		if row[1] == index {
			if row[2] != int64(1) || row[3] != "u" || row[4] != int64(0) {
				return fmt.Errorf("US security preflight: exact nonpartial unique key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("US security preflight: pinned native unique key missing")
	}
	info, err := query(ctx, conn, "PRAGMA main.index_info("+identifier(index)+")")
	if err != nil {
		return err
	}
	if len(info.Values) != len(columns) {
		return fmt.Errorf("US security preflight: native unique key width changed")
	}
	for n, column := range columns {
		if len(info.Values[n]) != 3 || info.Values[n][0] != int64(n) || info.Values[n][2] != column {
			return fmt.Errorf("US security preflight: native unique key column changed")
		}
	}
	return nil
}

func auditUSSecurityNamedIndex(ctx context.Context, conn *sql.Conn, table, index string, columns []string, descending []bool) error {
	if len(columns) != len(descending) {
		return fmt.Errorf("US security preflight: invalid compiled index shape")
	}
	if err := auditUSSecurityOctetBuiltin(ctx, conn); err != nil {
		return err
	}
	tables, err := query(ctx, conn, "PRAGMA main.table_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	if len(tables.Values) != 1 || len(tables.Values[0]) != 6 || tables.Values[0][0] != "main" ||
		tables.Values[0][1] != table || tables.Values[0][2] != "table" {
		return fmt.Errorf("US security preflight: ordinary MAIN table required")
	}
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("US security preflight: unexpected native index metadata")
		}
		if row[1] == index {
			if row[2] != int64(0) || row[3] != "c" || row[4] != int64(0) {
				return fmt.Errorf("US security preflight: exact nonpartial named index required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("US security preflight: pinned named index missing")
	}
	info, err := query(ctx, conn, "PRAGMA main.index_xinfo("+identifier(index)+")")
	if err != nil {
		return err
	}
	position := 0
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("US security preflight: invalid native index metadata")
		}
		if row[5] != int64(1) {
			continue
		}
		if position >= len(columns) || row[0] != int64(position) || row[2] != columns[position] ||
			row[3] != boolInt(descending[position]) || row[4] != "BINARY" {
			return fmt.Errorf("US security preflight: native index column changed")
		}
		position++
	}
	if position != len(columns) {
		return fmt.Errorf("US security preflight: native index width changed")
	}
	return nil
}

func auditUSSecurityOctetBuiltin(ctx context.Context, conn *sql.Conn) error {
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("US security preflight: invalid native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("US security preflight: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("US security preflight: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("US security preflight: unshadowed native octet_length required")
	}
	return nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
