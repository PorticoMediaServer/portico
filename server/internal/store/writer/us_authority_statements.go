package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usAuthorityCredential    stmt.ID = "us.authority.credential"
	usAuthorityResetSession  stmt.ID = "us.authority.reset_session"
	usAuthorityCurrentServer stmt.ID = "us.authority.current_server"
	usAuthorityPrincipalPIN  stmt.ID = "us.authority.principal_pin"
	usAuthorityFamily        stmt.ID = "us.authority.family"
	usAuthorityPrincipal     stmt.ID = "us.authority.principal"
	usAuthorityState         stmt.ID = "us.authority.state"
	usAuthorityDevice        stmt.ID = "us.authority.device"
	usAuthorityInstallation  stmt.ID = "us.authority.installation"
	usAuthorityGrant         stmt.ID = "us.authority.grant"
	usAuthorityWaiting       stmt.ID = "us.authority.waiting"
	usAuthorityFence         stmt.ID = "us.authority.fence"
)

// Owner/main-profile recipe only, unwired. Every string is compared to an owned
// witness in SQLite; only integers leave SQLite. In particular token/family
// timestamps are checked against the exact parsed preparation, not copied from
// an unbounded source before resource admission. No authorization_access view,
// caller role, JSON permission document, family scan or function hook is used.
func usAuthorityDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	pin := func(table, index, origin string, columns ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: index, Origin: origin}
		for _, column := range columns {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
		}
		return b
	}
	point := func(table, index, origin, column string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{column + "=?"}, AutoIndexes: []stmt.AutoIndexBinding{pin(table, index, origin, column)}}
	}
	familyPlan := point("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "pk", "token_hash")
	familyPlan.Indexes = append(familyPlan.Indexes, "sqlite_autoindex_authorization_session_families_1")
	familyPlan.Seeks = append(familyPlan.Seeks, "id=?")
	familyPlan.AutoIndexes = append(familyPlan.AutoIndexes, pin("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"))
	principalPlan := point("accounts", "sqlite_autoindex_accounts_1", "pk", "id")
	principalPlan.Indexes = append(principalPlan.Indexes, "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_2")
	principalPlan.Seeks = append(principalPlan.Seeks, "account_id=?", "account_id=? AND id=?")
	principalPlan.AutoIndexes = append(principalPlan.AutoIndexes, pin("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"), pin("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"))
	defs := []stmt.Definition{
		{ID: usAuthorityFamily, SQL: `SELECT f.control_variable_bytes,f.pin_gate FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
WHERE t.token_hash=? AND f.id=? AND t.generation=? AND t.retired=0 AND t.expires_at=?
AND f.server_id=? AND f.account_id=? AND f.profile_id=? AND f.authority='local' AND f.role='owner' AND f.security_kind=0
AND f.epoch=? AND f.current_generation=t.generation AND f.authority_generation=? AND f.revoked=0
AND f.command_member_incarnation=? AND f.command_profile_lifetime=? AND f.device_id=?
AND f.authorization_horizon=? AND f.remember_until=? AND f.manage_until=? AND f.pin_lineage_id=?`,
			Params: []stmt.Param{text("token"), text("family"), integer("generation"), text("token_expiry"), text("server"), text("account"), text("profile"), integer("epoch"), integer("authority_generation"), text("member"), text("lifetime"), text("device"), text("horizon"), text("remember"), text("manage"), text("lineage")}, Plan: familyPlan},
		{ID: usAuthorityPrincipal, SQL: `SELECT 1 FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id
WHERE a.id=? AND a.epoch=? AND a.must_change=0 AND m.incarnation=? AND m.role='owner' AND m.disabled=0
AND p.id=? AND p.lifetime=? AND p.deleted=0 AND p.is_primary=1 AND a.profile_id=p.id AND p.pin_hash IS NULL`,
			Params: []stmt.Param{text("account"), integer("epoch"), text("member"), text("profile"), text("lifetime")}, Plan: principalPlan},
		{ID: usAuthorityState, SQL: `SELECT 1 FROM identity_state WHERE singleton=1 AND sign_in_kind='local' AND authority_generation=? AND transition_id IS NULL`, Params: []stmt.Param{integer("generation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}}},
		{ID: usAuthorityDevice, SQL: `SELECT 1 FROM identity_devices INDEXED BY sqlite_autoindex_identity_devices_1 WHERE id=? AND authority='local' AND account_id=? AND installation_id=? AND retired_ms=0`, Params: []stmt.Param{text("device"), text("account"), text("installation")}, Plan: point("identity_devices", "sqlite_autoindex_identity_devices_1", "pk", "id")},
		{ID: usAuthorityInstallation, SQL: `SELECT 1 FROM identity_installations INDEXED BY sqlite_autoindex_identity_installations_1 WHERE installation_hash=? AND incarnation=? AND retired=0`, Params: []stmt.Param{text("installation_hash"), text("incarnation")}, Plan: point("identity_installations", "sqlite_autoindex_identity_installations_1", "pk", "installation_hash")},
		{ID: usAuthorityGrant, SQL: `SELECT 1 FROM authorization_family_grants INDEXED BY sqlite_autoindex_authorization_family_grants_1 WHERE family_id=?`, Params: []stmt.Param{text("family")}, Plan: point("authorization_family_grants", "sqlite_autoindex_authorization_family_grants_1", "pk", "family_id")},
		{ID: usAuthorityResetSession, SQL: `SELECT 1 FROM reset_file_sessions INDEXED BY sqlite_autoindex_reset_file_sessions_1 WHERE family_id=?`, Params: []stmt.Param{text("family")}, Plan: point("reset_file_sessions", "sqlite_autoindex_reset_file_sessions_1", "pk", "family_id")},
		{ID: usAuthorityWaiting, SQL: `SELECT 1 FROM identity_move_waiting INDEXED BY sqlite_autoindex_identity_move_waiting_1 WHERE account_id=? AND state='waiting'`, Params: []stmt.Param{text("account")}, Plan: point("identity_move_waiting", "sqlite_autoindex_identity_move_waiting_1", "pk", "account_id")},
		{ID: usAuthorityFence, SQL: `SELECT 1 FROM authorization_family_security_fences AS w
CROSS JOIN identity_security_fences AS s ON s.id=w.fence_id AND s.scope_kind=w.scope_kind
WHERE w.family_id=? AND w.scope_kind=? AND s.account_id=? AND s.member_incarnation=?
AND s.profile_id=? AND s.profile_lifetime=? AND s.device_id=? AND s.lineage_id=?
AND s.denied=0 AND w.confirmed_generation=s.generation`, Params: []stmt.Param{text("family"), text("kind"), text("account"), text("member"), text("profile"), text("lifetime"), text("device"), text("lineage")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"w", "s"}, Seeks: []string{"family_id=? AND scope_kind=?", "rowid=?"}}},
	}
	defs = append(defs, stmt.Definition{ID: usAuthorityCurrentServer, SQL: `SELECT 1 FROM configuration INDEXED BY sqlite_autoindex_configuration_1 WHERE key='id' AND value=?`, Params: []stmt.Param{text("server")}, Plan: point("configuration", "sqlite_autoindex_configuration_1", "pk", "key")})
	defs = append(defs, stmt.Definition{ID: usAuthorityCredential, SQL: `SELECT 1 FROM api_credentials INDEXED BY sqlite_autoindex_api_credentials_3 WHERE family_id=?`, Params: []stmt.Param{text("family")}, Plan: point("api_credentials", "sqlite_autoindex_api_credentials_3", "u", "family_id")})
	// The second closed point distinguishes protected from absent principals
	// without copying an unbounded PIN hash or extending the SQL grammar.
	protected := defs[1]
	protected.ID = usAuthorityPrincipalPIN
	protected.SQL = strings.Replace(protected.SQL, "p.pin_hash IS NULL", "p.pin_hash IS NOT NULL", 1)
	defs = append(defs, protected)
	cohorts := map[stmt.ID][]any{
		usAuthorityCredential:    {"issuer-family"},
		usAuthorityCurrentServer: {"server"},
		usAuthorityFamily:        {"UwZhzs548lfFuN7ngGv2H3QswZNRnNtn9rugLkKkejA", "issuer-family", int64(1), "2026-11-08T00:00:00Z", "server", "issuer", "issuer-profile", int64(1), int64(1), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "issuer-device", "2026-11-08T00:00:00Z", "", "", "issuer-lineage"},
		usAuthorityPrincipal:     {"issuer", int64(1), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		usAuthorityState:         {int64(1)},
		usAuthorityDevice:        {"issuer-device", "issuer", "issuer-installation"},
		usAuthorityInstallation:  {"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "cccccccccccccccccccccccccccccccc"},
		usAuthorityGrant:         {"issuer-family"},
		usAuthorityResetSession:  {"issuer-family"},
		usAuthorityWaiting:       {"issuer"},
		usAuthorityFence:         {"issuer-family", "device", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", "", "issuer-device", ""},
	}
	cohorts[usAuthorityPrincipalPIN] = cohorts[usAuthorityPrincipal]
	for n := range defs {
		defs[n].Cohorts = []stmt.Cohort{{Name: "closed_point", Bindings: cohorts[defs[n].ID]}}
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US direct owner authority recipe"
		defs[n].CallSites = []string{"store/writer/us_authority.go"}
	}
	return defs
}
